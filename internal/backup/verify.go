package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gcal"
	"github.com/tonimelisma/pimvault/internal/gmail"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/mailheader"
)

// Difference is an archive directory whose item count differs from the
// account's (D81).
type Difference struct {
	Path             string
	Account, Archive int
}

// Check is the result of verifying one data type of an account: how many
// directories were compared, those whose counts differ, and how many could
// not be counted because reading them failed. Uncounted directories are not
// compared: their count stays unknown.
type Check struct {
	Checked     int
	Differences []Difference
	Failed      int
}

// Unreadable is an archive file that cannot be read or parsed, and why.
type Unreadable struct {
	Path   string
	Reason string
}

// census holds the account's item count per archive directory, and the
// directories whose count is unknown because reading them failed.
type census struct {
	counts  map[string]int
	unknown map[string]bool
}

func newCensus() census {
	return census{counts: make(map[string]int), unknown: make(map[string]bool)}
}

// add counts an item in a directory. An item whose ID cannot name a file
// can never be archived (backup counts it as failed), so it is left out.
func (c census) add(path, id string, logger *slog.Logger) {
	if !archive.ValidID(id) {
		logger.Warn("item cannot be archived: id is not usable as a file name", slog.String("folder", path), slog.String("item", id))

		return
	}

	c.counts[path]++
}

// compare checks a census against the files a store holds, without
// writing.
func compare(store archive.Store, c census) (Check, error) {
	have, err := store.Scan()
	if err != nil {
		return Check{}, err
	}

	archived := make(map[string]int)

	for _, folders := range have {
		for _, f := range folders {
			archived[f]++
		}
	}

	paths := make(map[string]bool)
	for p := range c.counts {
		paths[p] = true
	}

	for p := range archived {
		paths[p] = true
	}

	check := Check{Failed: len(c.unknown)}

	for _, p := range slices.Sorted(maps.Keys(paths)) {
		if c.unknown[p] {
			continue
		}

		check.Checked++

		if c.counts[p] != archived[p] {
			check.Differences = append(check.Differences, Difference{Path: p, Account: c.counts[p], Archive: archived[p]})
		}
	}

	return check, nil
}

// Verify compares the account's messages per folder with the archive.
func (g *GmailMail) Verify(ctx context.Context) (Check, error) {
	labels, err := g.Client.UserLabels(ctx)
	if err != nil {
		return Check{}, err
	}

	folders, err := listFolders(ctx, g.Client, labels)
	if err != nil {
		return Check{}, err
	}

	c := newCensus()

	// Every folder a message could go to is compared, empty ones too.
	for _, labelID := range append([]string{"", gmail.LabelInbox, gmail.LabelDraft, gmail.LabelSent}, slices.Collect(maps.Keys(labels))...) {
		c.counts[folderOrGone([]string{labelID}, labels)] = 0
	}

	for id, folder := range folders {
		if folder != "" {
			c.add(folder, id, g.Logger)
		}
	}

	return compare(g.Store, c)
}

// Verify compares the account's messages per folder with the archive.
func (o *OutlookMail) Verify(ctx context.Context) (Check, error) {
	folders, err := o.Client.MailFolders(ctx)
	if err != nil {
		return Check{}, err
	}

	c := newCensus()

	for _, f := range folders {
		if err := ctx.Err(); err != nil {
			return Check{}, fmt.Errorf("verify mail: %w", err)
		}

		delta, err := o.Client.MessageDelta(ctx, f.ID, "")
		if errors.Is(err, graph.ErrDeltaExpired) {
			// A listing in progress can expire; it is started again once,
			// as backup does.
			delta, err = o.Client.MessageDelta(ctx, f.ID, "")
		}

		if err != nil {
			c.unknown[f.Path] = true
			o.Logger.Error("verify folder", slog.String("folder", f.Path), slog.String("error", err.Error()))

			continue
		}

		c.counts[f.Path] = 0 // an empty folder is compared too

		for _, id := range delta.Added {
			c.add(f.Path, id, o.Logger)
		}
	}

	return compare(o.Store, c)
}

// verifyCollections compares each collection's items with the archive. It
// reads them the way a full backup does, rendering each item, so that items
// the backup leaves out are left out here too. A collection that cannot be
// read, or has items that cannot be rendered, is not compared.
func verifyCollections(ctx context.Context, src collectionSource, store archive.Store, logger *slog.Logger) (Check, error) {
	colls, err := src.collections(ctx)
	if err != nil {
		return Check{}, err
	}

	c := newCensus()

	for _, coll := range colls {
		if err := ctx.Err(); err != nil {
			return Check{}, fmt.Errorf("verify collections: %w", err)
		}

		l, err := src.full(ctx, coll)
		if err != nil || len(l.failed) > 0 {
			c.unknown[coll.Path] = true

			if err != nil {
				logger.Error("verify collection", slog.String("collection", coll.Path), slog.String("error", err.Error()))
			}

			continue
		}

		c.counts[coll.Path] = 0

		for id, data := range l.items {
			if data != nil {
				c.add(coll.Path, id, logger)
			}
		}
	}

	return compare(store, c)
}

// Verify compares the account's calendars with the archive.
func (g *GoogleCalendars) Verify(ctx context.Context) (Check, error) {
	src := &gcalSource{GoogleCalendars: g, calendars: make(map[string]*gcal.Calendar)}

	return verifyCollections(ctx, src, g.Store, g.Logger)
}

// Verify compares the account's calendars with the archive.
func (o *OutlookCalendars) Verify(ctx context.Context) (Check, error) {
	return verifyCollections(ctx, &outlookCalSource{o}, o.Store, o.Logger)
}

// Verify compares the account's contacts with the archive.
func (g *GoogleContacts) Verify(ctx context.Context) (Check, error) {
	return verifyCollections(ctx, &gpeopleSource{GoogleContacts: g}, g.Store, g.Logger)
}

// Verify compares the account's contacts with the archive.
func (o *OutlookContacts) Verify(ctx context.Context) (Check, error) {
	return verifyCollections(ctx, &outlookContactSource{o}, o.Store, o.Logger)
}

// Verify compares the account's task lists with the archive.
func (g *GoogleTasks) Verify(ctx context.Context) (Check, error) {
	return verifyCollections(ctx, &gtasksSource{g}, g.Store, g.Logger)
}

// Verify compares the account's task lists with the archive.
func (o *OutlookTasks) Verify(ctx context.Context) (Check, error) {
	return verifyCollections(ctx, &outlookTaskSource{o}, o.Store, o.Logger)
}

// CheckFiles reads every file of a store and returns those that cannot be
// read or parsed, in path order (D190). It needs no account.
func CheckFiles(ctx context.Context, store archive.Store) ([]Unreadable, error) {
	have, err := store.Scan()
	if err != nil {
		return nil, err
	}

	var bad []Unreadable

	for id, folders := range have {
		for _, folder := range folders {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("check files: %w", err)
			}

			if err := checkFile(store, folder, id); err != nil {
				bad = append(bad, Unreadable{Path: filepath.ToSlash(store.FilePath(folder, id)), Reason: reason(err)})
			}
		}
	}

	slices.SortFunc(bad, func(a, b Unreadable) int { return strings.Compare(a.Path, b.Path) })

	return bad, nil
}

// reason is what is wrong with a file: the system's answer for a file that
// cannot be read, without the path the listing already shows.
func reason(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}

	return err.Error()
}

// checkFile reads one file to its end and checks its content: a message
// must have a header, an event or task file must be one iCalendar object
// with an event or task, a contact file one vCard.
func checkFile(store archive.Store, folder, id string) error {
	f, err := store.Open(folder, id)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read only.

	r := &countingReader{r: bufio.NewReader(f)}

	switch store.Ext() {
	case ".eml":
		err = checkMessage(r)
	case ".ics":
		err = checkCalendar(r)
	case ".vcf":
		err = checkCard(r)
	}

	if err == nil {
		_, err = io.Copy(io.Discard, r)
	}

	if err == nil && r.n == 0 {
		err = errors.New("empty file")
	}

	return err
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)

	return n, err //nolint:wrapcheck // a pass-through reader.
}

// checkMessage reads a message's first paragraph and checks that it holds
// a header field, as restore reads it.
func checkMessage(r io.Reader) error {
	var head bytes.Buffer

	br := bufio.NewReader(r)

	for {
		line, err := br.ReadBytes('\n')
		head.Write(line)

		if len(bytes.TrimRight(line, "\r\n")) == 0 || err != nil {
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("read message: %w", err)
			}

			break
		}
	}

	if head.Len() > 0 && len(mailheader.Parse(head.Bytes()).Fields) == 0 {
		return errors.New("no message header")
	}

	_, err := io.Copy(io.Discard, br)

	return err //nolint:wrapcheck // checkFile reports it.
}

// checkCalendar decodes one iCalendar object holding an event or a task,
// with nothing after it. The decoder takes a file cut short for a whole
// one, so the object's first and last lines are checked too.
func checkCalendar(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return err //nolint:wrapcheck // checkFile reports it.
	}

	if err = wholeObject(data, "VCALENDAR"); err != nil {
		return fmt.Errorf("parse iCalendar: %w", err)
	}

	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return fmt.Errorf("parse iCalendar: %w", err)
	}

	if !slices.ContainsFunc(cal.Children, func(c *ical.Component) bool { return c.Name == ical.CompEvent || c.Name == ical.CompToDo }) {
		return errors.New("parse iCalendar: no event or task")
	}

	return nil
}

// checkCard decodes one vCard with nothing after it, checking its first and
// last lines like checkCalendar.
func checkCard(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return err //nolint:wrapcheck // checkFile reports it.
	}

	if err = wholeObject(data, "VCARD"); err != nil {
		return fmt.Errorf("parse vCard: %w", err)
	}

	if _, err := vcard.NewDecoder(bytes.NewReader(data)).Decode(); err != nil {
		return fmt.Errorf("parse vCard: %w", err)
	}

	return nil
}

// wholeObject checks that data is one object of a kind: it begins with
// BEGIN:<kind>, holds no other, and ends with END:<kind>.
func wholeObject(data []byte, kind string) error {
	begins := 0

	var first, last string

	for line := range bytes.Lines(data) {
		text := strings.ToUpper(strings.TrimSpace(string(line)))
		if text == "" {
			continue
		}

		if first == "" {
			first = text
		}

		if text == "BEGIN:"+kind {
			begins++
		}

		last = text
	}

	switch {
	case first != "BEGIN:"+kind:
		return fmt.Errorf("does not begin with BEGIN:%s", kind)
	case begins > 1:
		return fmt.Errorf("more than one %s", kind)
	case last != "END:"+kind:
		return errors.New("cut short")
	default:
		return nil
	}
}
