package restore

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gmail"
)

// GmailMail writes archived mail into a Gmail account. Directories map by
// role (D59): Inbox, Sent and Drafts to Gmail's own, Archive to no label, a
// directory named like a system label to it (D162), every other directory
// to a label of the same name, created if missing. A
// message archived in several directories is written once with all their
// labels. Messages are inserted (D55) as read (D153), drafts with the
// DRAFT label (D165), with a missing Date or Message-ID filled in (D184).
type GmailMail struct {
	Client *gmail.Client
	Source archive.Store
	Logger *slog.Logger
	// DryRun writes nothing and reports each item to Planned instead.
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
}

// gmailRun is the working state of one Run.
type gmailRun struct {
	*GmailMail

	res Result
	// labels maps lowercased label names to IDs, including those created by
	// this run; refused holds the names Gmail would not create.
	labels  map[string]string
	refused map[string]error
	// renameFailed holds the names whose "(restored)" label could not be
	// had either.
	renameFailed map[string]error
	// written holds the keys of messages this run wrote (or would write):
	// later archive copies of them are already there.
	written map[mailKey]bool
}

// Run writes every archived message the account does not have yet.
func (g *GmailMail) Run(ctx context.Context) (Result, error) {
	have, err := g.Source.Scan()
	if err != nil {
		return Result{}, err
	}

	userLabels, err := g.Client.UserLabels(ctx)
	if err != nil {
		return Result{}, err
	}

	r := &gmailRun{
		GmailMail: g, labels: make(map[string]string, len(userLabels)),
		refused: map[string]error{}, renameFailed: map[string]error{}, written: map[mailKey]bool{},
	}
	for id, name := range userLabels {
		r.labels[strings.ToLower(name)] = id
	}

	ids := slices.Sorted(maps.Keys(have))

	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			return r.res, fmt.Errorf("restore mail: %w", err)
		}

		g.Progress.Report(i, len(ids))

		folders := slices.Sorted(slices.Values(have[id]))
		if err := r.restore(ctx, folders, id); err != nil {
			r.res.Failed++
			g.Logger.Error("restore message", slog.String("folder", folders[0]), slog.String("message", id), slog.String("error", err.Error()))
		}
	}

	g.Progress.Report(len(ids), len(ids))

	return r.res, nil
}

// restore writes one message, archived in folders, unless the account has
// it.
func (r *gmailRun) restore(ctx context.Context, folders []string, id string) error {
	raw, keys, err := readMessage(r.Source, folders[0], id)
	if err != nil {
		return err
	}

	subject, spamTrash := subjectOf(raw), inSpamOrTrash(folders)

	present, err := presentAs(keys, r.written, func(key mailKey) (bool, error) {
		return r.present(ctx, key, subject, spamTrash)
	})
	if err != nil {
		return err
	}

	item := path.Join(folders[0], id)

	switch {
	case present && r.DryRun:
		r.Planned(item, ActionPresent)
	case present:
	case r.DryRun:
		r.Planned(item, ActionWrite)
	default:
		var labels []string

		labels, err = r.labelsFor(ctx, folders)
		if err == nil {
			err = r.Client.Insert(ctx, raw, labels)
		}
	}

	if err != nil {
		return err
	}

	if present {
		r.res.Present++

		return nil
	}

	r.res.Written++
	r.written[keys[0]] = true

	return nil
}

// inSpamOrTrash reports whether a message goes to Spam or Trash (D162),
// where its copy is looked for too.
func inSpamOrTrash(folders []string) bool {
	for _, f := range folders {
		if id, ok := gmail.SystemLabel(f); ok && (id == gmail.LabelSpam || id == gmail.LabelTrash) {
			return true
		}
	}

	return false
}

// present reports whether the account has a copy of the message (D37).
// Gmail replaces a Message-ID that is not valid with its own (F52), so
// copies of such a message are looked for by date, sender and subject
// instead. Gmail's Message-ID search cannot find a valid Message-ID that
// holds an encoded-word (F84), so copies of such a message are looked for
// by date and sender and matched on the Message-ID itself (D192). A
// message of either kind that also has no valid date is never found. An
// unknown date matches any.
func (r *gmailRun) present(ctx context.Context, key mailKey, subject string, spamTrash bool) (bool, error) {
	var (
		copies []gmail.Headers
		err    error
	)

	valid := validMessageID(key.messageID)
	encoded := valid && hasEncodedWord(key.messageID)
	byID := valid && !encoded

	switch date, dateErr := time.Parse(time.RFC3339, key.date); {
	case byID:
		copies, err = r.Client.WithMessageID(ctx, key.messageID, spamTrash)
	case dateErr == nil:
		from := key.from
		if !strings.Contains(from, "@") || strings.ContainsAny(from, " \t\"()<>") {
			from = ""
		}

		copies, err = r.Client.Around(ctx, from, date, spamTrash)
	default:
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return slices.ContainsFunc(copies, func(c gmail.Headers) bool { return isCopy(c, key, subject, byID, encoded) }), nil
}

// isCopy reports whether a message Gmail found is a copy of the one with
// key and subject (D37): same Date and From, and the same Message-ID, which
// a Message-ID search already ensured (byID), which is compared when it
// holds an encoded-word (D192), or else the same subject (D167). An
// unknown date matches any.
func isCopy(c gmail.Headers, key mailKey, subject string, byID, encoded bool) bool {
	if key.date != "" && normalDate(c.Date) != key.date || normalFrom(c.From) != key.from {
		return false
	}

	switch {
	case byID:
		return true
	case encoded:
		return sameMessageID(c.MessageID, key.messageID)
	default:
		return normalSubject(c.Subject) == subject
	}
}

// labelsFor returns the label IDs for a message archived in folders,
// creating labels the account has none of that name for. A nested directory
// is a nested label, "Work/Projects" (D142).
func (r *gmailRun) labelsFor(ctx context.Context, folders []string) ([]string, error) {
	var ids []string

	for _, folder := range folders {
		switch folder {
		case archive.Inbox:
			ids = append(ids, gmail.LabelInbox)
		case archive.Sent:
			ids = append(ids, gmail.LabelSent)
		case archive.Drafts:
			ids = append(ids, gmail.LabelDraft)
		case archive.Archive:
		default:
			id, err := r.label(ctx, strings.Join(archive.FolderSegments(folder), "/"))
			if err != nil {
				return nil, err
			}

			ids = append(ids, id)
		}
	}

	return ids, nil
}

// The longest label name Gmail takes: 225 characters (F71) and 255 bytes,
// over which it answers 500 instead of refusing (F81).
const (
	gmailNameRunes = 225
	gmailNameBytes = 255
)

func gmailNameLimit() nameLimit {
	return nameLimit{runes: gmailNameRunes, bytes: gmailNameBytes}
}

// label returns the ID of the label with a name: Gmail's own for the names
// of its system labels ("Spam" is SPAM, whatever the case), else the user
// label of that name, created if missing. User label names match whatever
// their case, as in Gmail, with whitespace trimmed and collapsed, as Gmail
// creates them (F71). A name Gmail refuses, such as "Chats", is written as
// "<name> (restored)" (D185).
func (r *gmailRun) label(ctx context.Context, name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")

	if id, ok := gmail.SystemLabel(name); ok {
		return id, nil
	}

	if id, ok := r.labels[strings.ToLower(name)]; ok {
		return id, nil
	}

	// A rename that failed is not tried again, nor logged again.
	if err, ok := r.renameFailed[strings.ToLower(name)]; ok {
		return "", err
	}

	// A name over the limit is refused before Gmail answers 500 for it.
	if gmailNameLimit().fits(name) {
		id, err := r.userLabel(ctx, name)
		if !gmail.InvalidLabelName(err) {
			return id, err
		}
	}

	renamed := strings.Join(strings.Fields(restoredName(name, gmailNameLimit())), " ")
	r.Logger.Warn("Gmail refuses this label name; writing to another label",
		slog.String("label", name), slog.String("renamed", renamed))

	id, err := r.userLabel(ctx, renamed)
	if err != nil {
		r.renameFailed[strings.ToLower(name)] = err

		return "", err
	}

	r.labels[strings.ToLower(name)] = id

	return id, nil
}

// userLabel returns the ID of the user label with a name, created if
// missing. A name Gmail refuses is refused once per run.
func (r *gmailRun) userLabel(ctx context.Context, name string) (string, error) {
	if id, ok := r.labels[strings.ToLower(name)]; ok {
		return id, nil
	}

	if err, ok := r.refused[strings.ToLower(name)]; ok {
		return "", err
	}

	id, err := r.Client.CreateLabel(ctx, name)
	if err != nil {
		r.refused[strings.ToLower(name)] = err

		return "", err
	}

	r.labels[strings.ToLower(name)] = id

	return id, nil
}
