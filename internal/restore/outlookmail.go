package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/httpretry"
	"github.com/tonimelisma/pimvault/internal/mailheader"
	"github.com/tonimelisma/pimvault/internal/outlookimap"
)

// OutlookMail writes archived mail into an Outlook.com mailbox with IMAP
// APPEND (D56). Directories map by role (D59): Inbox, Sent, Drafts and
// Archive to Outlook's own folders, every other directory to a folder of
// the same name, created if missing. Messages are marked read (D153); a
// message larger than Outlook.com accepts is skipped and stays in the
// archive (D57). A throttled APPEND is waited out and tried again (F75).
// A missing Date or Message-ID is filled in (D184). Copies are found
// through Graph by Message-ID, Date and From (D37).
type OutlookMail struct {
	Graph *graph.Client
	// Open signs in to the mailbox over IMAP, before the first message is
	// written; a run that writes none does not call it.
	Open     func(ctx context.Context) (*outlookimap.Mailbox, error)
	Source   archive.Store
	Logger   *slog.Logger
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
	// Sleep waits out throttling; nil means a timer.
	Sleep httpretry.SleepFunc
}

// Throttling (F75): the server's suggested wait is cut to maxThrottleWait,
// and a message still throttled after throttleWaits waits stops the run, as
// every message after it would wait as long.
const (
	maxThrottleWait = 5 * time.Minute
	throttleWaits   = 5
)

type outlookMailRun struct {
	*OutlookMail

	res     Result
	mailbox *outlookimap.Mailbox
	// folders is what the account has: a copy in a folder backup leaves out
	// (Deleted Items, Junk Email) does not count, and a directory named
	// like one of Outlook.com's own folders is renamed (D185).
	folders graph.RestoreFolders
	// renamed maps the lowercased top-level directory names that are
	// written under another name to that name.
	renamed map[string]string
	// waits counts the current message's throttling waits.
	waits int
	// written holds the keys of messages this run wrote (or would write).
	written map[mailKey]bool
}

// Run writes every archived message the account does not have yet.
func (o *OutlookMail) Run(ctx context.Context) (res Result, err error) {
	r := &outlookMailRun{OutlookMail: o, written: map[mailKey]bool{}, renamed: map[string]string{}}

	if r.folders, err = o.Graph.RestoreFolders(ctx); err != nil {
		return Result{}, err
	}

	defer func() {
		if r.mailbox != nil {
			err = errors.Join(err, r.mailbox.Close())
		}
	}()

	err = eachItem(ctx, o.Source, o.Progress, o.Logger, &r.res, func(folder, id string) error {
		return r.restore(ctx, folder, id)
	})

	return r.res, err
}

func (r *outlookMailRun) restore(ctx context.Context, folder, id string) error {
	raw, keys, err := readMessage(r.Source, folder, id)
	if err != nil {
		return err
	}

	present, err := presentAs(keys, r.written, func(key mailKey) (bool, error) { return r.present(ctx, key) })
	if err != nil {
		return err
	}

	item := path.Join(folder, id)

	if present || r.DryRun {
		r.res.count(r.DryRun, r.Planned, item, present)

		if !present {
			r.written[keys[0]] = true
		}

		return nil
	}

	flags := []imap.Flag{imap.FlagSeen}
	if top, _, _ := strings.Cut(folder, "/"); top == archive.Drafts {
		flags = append(flags, imap.FlagDraft)
	}

	r.waits = 0
	err = r.append(ctx, folder, raw, flags, keys[0])
	if errors.Is(err, outlookimap.ErrTooBig) {
		r.res.Skipped++
		r.Logger.Warn("message larger than Outlook.com accepts; skipped, kept in the archive", slog.String("message", item))

		return nil
	}

	if err != nil {
		return err
	}

	r.res.Written++
	r.written[keys[0]] = true

	return nil
}

// present reports whether the mailbox has a copy of the message (D37),
// outside the folders backup leaves out. Graph finds no message by a
// Message-ID that is not valid (F58), so copies of such a message are
// looked for by date. A Date or From that does not parse cannot be
// compared; the headers that parse decide.
func (r *outlookMailRun) present(ctx context.Context, key mailKey) (bool, error) {
	var (
		copies []graph.MessageHeaders
		err    error
	)

	date, dateErr := time.Parse(time.RFC3339, key.date)

	switch {
	case validMessageID(key.messageID):
		copies, err = r.Graph.WithMessageID(ctx, key.messageID)
	case dateErr == nil:
		from := key.from
		if !strings.Contains(from, "@") || strings.ContainsAny(from, " '\"<>") {
			from = ""
		}

		copies, err = r.Graph.Around(ctx, from, date)
	default:
		return false, nil
	}

	if err != nil {
		return false, err
	}

	fromParses := strings.Contains(key.from, "@")

	for _, c := range copies {
		if r.folders.Skipped[c.FolderID] || strings.Join(strings.Fields(c.MessageID), "") != key.messageID {
			continue
		}

		if (dateErr != nil || sameDate(c.Date, key.date)) && (!fromParses || normalFrom(c.From) == key.from) {
			return true, nil
		}
	}

	return false, nil
}

// sameDate compares a Date header, or Graph's RFC 3339 sent time standing
// in for one, with a key's date.
func sameDate(value, keyDate string) bool {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format(time.RFC3339) == keyDate
	}

	return normalDate(value) == keyDate
}

// append adds a message to the folder of an archive directory. When the
// connection breaks (Outlook.com ends IMAP sessions whose token expired),
// it signs in again once and, unless the message arrived after all,
// appends it again. When signing in again fails, the run stops.
func (r *outlookMailRun) append(ctx context.Context, dir string, raw []byte, flags []imap.Flag, key mailKey) error {
	if r.mailbox == nil {
		mailbox, err := r.Open(ctx)
		if err != nil {
			return fmt.Errorf("%w: %w", errStop, err)
		}

		r.mailbox = mailbox
	}

	err := r.appendWaiting(ctx, dir, raw, flags)
	if err == nil || outlookimap.Refused(err) || ctx.Err() != nil {
		return err
	}

	r.Logger.Debug("imap connection broke; signing in again", slog.String("error", err.Error()))
	_ = r.mailbox.Close() //nolint:errcheck // the connection is already broken.
	r.mailbox = nil

	mailbox, openErr := r.Open(ctx)
	if openErr != nil {
		return fmt.Errorf("%w: %w", errStop, errors.Join(err, openErr))
	}

	r.mailbox = mailbox

	arrived, presentErr := r.present(ctx, key)
	if presentErr != nil || arrived {
		return presentErr
	}

	return r.appendWaiting(ctx, dir, raw, flags)
}

// appendWaiting appends a message, waiting out throttling (F75).
func (r *outlookMailRun) appendWaiting(ctx context.Context, dir string, raw []byte, flags []imap.Flag) error {
	for ; ; r.waits++ {
		err := r.appendOnce(dir, raw, flags)

		wait, throttled := outlookimap.Throttled(err)
		if !throttled {
			return err
		}

		if r.waits == throttleWaits {
			return fmt.Errorf("%w: still throttled after %d waits: %w", errStop, r.waits, err)
		}

		wait = min(wait, maxThrottleWait)
		r.Logger.Warn("Outlook.com is throttling; waiting", slog.Duration("wait", wait))

		sleep := r.Sleep
		if sleep == nil {
			sleep = httpretry.Timer
		}

		if err := sleep(ctx, wait); err != nil {
			return fmt.Errorf("wait out throttling: %w", err)
		}
	}
}

func (r *outlookMailRun) appendOnce(dir string, raw []byte, flags []imap.Flag) error {
	target, err := r.folder(dir)
	if err != nil {
		return err
	}

	return r.mailbox.Append(target, raw, flags, dateOf(raw))
}

// folder returns the IMAP folder for an archive directory: the role
// folders are Outlook's own (D59), and folders inside them stay inside;
// other top-level directories are as topFolder says.
func (r *outlookMailRun) folder(dir string) (string, error) {
	top, rest, _ := strings.Cut(dir, "/")

	var path []string
	if rest != "" {
		path = archive.FolderSegments(rest)
	}

	switch top {
	case archive.Inbox:
		top = "INBOX"
	case archive.Sent:
		top = r.mailbox.Special(imap.MailboxAttrSent, "Sent")
	case archive.Drafts:
		top = r.mailbox.Special(imap.MailboxAttrDrafts, "Drafts")
	case archive.Archive:
		top = r.mailbox.Special(imap.MailboxAttrArchive, "Archive")
	default:
		path = archive.FolderSegments(dir)

		name, err := r.topFolder(path[0])
		if err != nil || len(path) == 1 {
			return name, err
		}

		path[0] = name

		return r.mailbox.Folder("", path)
	}

	if len(path) == 0 {
		return top, nil
	}

	return r.mailbox.Folder(top, path)
}

// outlookNameRunes is the longest folder name known to work: Graph took
// 256 characters (F72).
const outlookNameRunes = 256

// topFolder returns the IMAP folder a top-level directory is written to,
// created if missing: the folder of its name, unless Outlook.com has a
// folder of that name that is not a user folder (a well-known one such as
// Deleted Items, or one Graph hides, such as Notes) or refuses the name as
// reserved (F73). Then it is "<name> (restored)", logged once (D185).
func (r *outlookMailRun) topFolder(name string) (string, error) {
	lower := strings.ToLower(name)
	if renamed, ok := r.renamed[lower]; ok {
		return r.mailbox.Folder("", []string{renamed})
	}

	if !r.folders.System[lower] && (r.folders.User[lower] || !r.mailbox.Has(name)) {
		folder, err := r.mailbox.Folder("", []string{name})
		if !outlookimap.Reserved(err) {
			if err == nil {
				// Created by this run: a user folder from now on.
				r.folders.User[lower] = true
			}

			return folder, err
		}
	}

	renamed := restoredName(name, nameLimit{runes: outlookNameRunes})
	r.renamed[lower] = renamed
	r.Logger.Warn("Outlook.com keeps this folder name for itself; writing to another folder",
		slog.String("folder", name), slog.String("renamed", renamed))

	return r.mailbox.Folder("", []string{renamed})
}

// dateOf returns a message's Date header, or the zero time.
func dateOf(raw []byte) time.Time {
	t, _ := headerDate(mailheader.Parse(raw))

	return t
}
