// Package restore writes an account's archive into an account: the same one
// (restore) or another (migrate) (D12–D14, D76). Every
// run merges into what the target has and skips items it already has
// (D60), so an interrupted run is resumed by running it again.
package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/mail"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/googleapi"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/mailheader"
)

// Result counts what one data type's run did (D155). Present items were
// already in the target; Skipped items cannot go into it (such as mail over
// a size cap) and stay in the archive.
type Result struct {
	Written, Present, Skipped, Failed int
}

// Planned receives, in a dry run, each item and what would happen to it
// (D61): "write" or "present".
type Planned func(path, action string)

// Dry-run actions.
const (
	ActionWrite   = "write"
	ActionPresent = "present"
)

// mailKey is what makes two messages copies of each other (D37): the same
// Message-ID, Date and From. An empty date is unknown and matches any. A
// message without a Message-ID has no key; restore gives the copy it writes
// one (D184).
type mailKey struct {
	messageID string
	date      string
	from      string
}

// keyOf reads a message's key from its headers. ok is false when it has no
// Message-ID.
func keyOf(raw []byte) (key mailKey, ok bool) {
	h := mailheader.Parse(raw)

	// A msg-id holds no whitespace: what is there comes from folding.
	id := messageID(h)
	if id == "" {
		return mailKey{}, false
	}

	return mailKey{messageID: id, date: normalDate(h.Get("date")), from: normalFrom(h.Get("from"))}, true
}

// normalDate makes equal dates compare equal however they are written: the
// instant in UTC, or the trimmed header when it does not parse.
func normalDate(value string) string {
	if t, err := mail.ParseDate(value); err == nil {
		return t.UTC().Format(time.RFC3339)
	}

	return strings.TrimSpace(value)
}

// normalFrom compares From by address, ignoring the display name and case,
// or by the trimmed header when it does not parse. A display name in any
// charset parses: only the address is kept.
func normalFrom(value string) string {
	if a, err := anyCharset().Parse(value); err == nil {
		return strings.ToLower(a.Address)
	}

	// A decoded display name may not parse ("Doe, John <j@x>"); the
	// address in angle brackets still names the sender.
	if m := angleAddress.FindStringSubmatch(value); m != nil {
		return strings.ToLower(m[1])
	}

	return strings.TrimSpace(value)
}

// angleAddress finds the address in "name <address>".
var angleAddress = regexp.MustCompile(`<([^<>\s@]+@[^<>\s]+)>\s*$`)

// subjectOf returns a message's subject as normalSubject makes it.
func subjectOf(raw []byte) string {
	return normalSubject(mailheader.Parse(raw).Get("subject"))
}

// normalSubject decodes a subject where Go can and collapses its
// whitespace, so a folded or encoded subject compares equal to the decoded
// one Gmail shows.
func normalSubject(value string) string {
	if decoded, err := new(mime.WordDecoder).DecodeHeader(value); err == nil {
		value = decoded
	}

	return strings.Join(strings.Fields(value), " ")
}

// anyCharset returns a parser for addresses whose display names are
// encoded in charsets Go does not decode.
func anyCharset() *mail.AddressParser {
	return &mail.AddressParser{WordDecoder: &mime.WordDecoder{
		CharsetReader: func(_ string, input io.Reader) (io.Reader, error) { return input, nil },
	}}
}

// validMessageID reports whether id is a msg-id as RFC 5322 section 3.6.4
// defines it, without the obsolete forms: "<" dot-atom-text "@"
// (dot-atom-text or "[" dtext "]") ">".
func validMessageID(id string) bool {
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, ">") {
		return false
	}

	inner := id[1 : len(id)-1]

	left, right, ok := strings.Cut(inner, "@")
	if !ok || !dotAtom(left) {
		return false
	}

	if literal, isLiteral := strings.CutPrefix(right, "["); isLiteral {
		literal, ok = strings.CutSuffix(literal, "]")

		return ok && !strings.ContainsFunc(literal, func(r rune) bool { return r < '!' || r > '~' || strings.ContainsRune("[]\\", r) })
	}

	return dotAtom(right)
}

// hasEncodedWord reports whether a Message-ID holds an RFC 2047
// encoded-word, as some spam's does (F84): one that decoding changes, so
// "=?" and "?=" in another shape do not count.
func hasEncodedWord(id string) bool {
	return decodeWords(id) != strings.Join(strings.Fields(id), "")
}

// sameMessageID reports whether two Message-IDs are the same once their
// encoded-words are decoded, so a copy matches whether Gmail gives its
// Message-ID as written or decoded, as it does display names (F52).
func sameMessageID(a, b string) bool {
	return decodeWords(a) == decodeWords(b)
}

// decodeWords returns a header value with its encoded-words decoded and
// its whitespace removed, or the value without whitespace when it does
// not decode.
func decodeWords(value string) string {
	value = strings.Join(strings.Fields(value), "")

	decoder := mime.WordDecoder{CharsetReader: func(_ string, input io.Reader) (io.Reader, error) { return input, nil }}
	if decoded, err := decoder.DecodeHeader(value); err == nil {
		return decoded
	}

	return value
}

// dotAtom reports whether s is dot-atom-text: atoms of atext joined by
// single dots.
func dotAtom(s string) bool {
	for atom := range strings.SplitSeq(s, ".") {
		if atom == "" || strings.ContainsFunc(atom, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r)
		}) {
			return false
		}
	}

	return true
}

// restoredName is the name a folder or label is written under when the
// target refuses its own or keeps it for itself (D185): "<name>
// (restored)", the name cut so that the whole fits the limit.
func restoredName(name string, limit nameLimit) string {
	const suffix = " (restored)"

	runes := []rune(name)
	runes = runes[:max(0, min(len(runes), limit.runes-len(suffix)))]

	for limit.bytes > 0 && len(runes) > 0 && len(string(runes))+len(suffix) > limit.bytes {
		runes = runes[:len(runes)-max(1, (len(string(runes))+len(suffix)-limit.bytes)/utf8.UTFMax)]
	}

	return string(runes) + suffix
}

// nameLimit is the longest folder or label name a target takes, in
// characters and, where known, in UTF-8 bytes (0: no byte limit known).
type nameLimit struct {
	runes, bytes int
}

// fits reports whether a name is within the limit.
func (l nameLimit) fits(name string) bool {
	return utf8.RuneCountInString(name) <= l.runes && (l.bytes == 0 || len(name) <= l.bytes)
}

// count records an item that is already there or would be written, and
// reports it in a dry run.
func (r *Result) count(dryRun bool, planned Planned, item string, present bool) {
	action := ActionWrite
	if present {
		action = ActionPresent
		r.Present++
	} else {
		r.Written++
	}

	if dryRun {
		planned(item, action)
	}
}

// itemRun is the working state of a run that writes archived items one by
// one.
type itemRun interface {
	// index reads what the account has.
	index(ctx context.Context) error
	// restore writes one item unless the account has it.
	restore(ctx context.Context, folder, id string) error
}

// runItems indexes the account, then restores every archived item of a
// store into it, counting into res.
func runItems(
	ctx context.Context, run itemRun, res *Result, store archive.Store, progress backup.Progress, logger *slog.Logger,
) (Result, error) {
	if err := run.index(ctx); err != nil {
		return Result{}, err
	}

	err := eachItem(ctx, store, progress, logger, res, func(folder, id string) error { return run.restore(ctx, folder, id) })

	return *res, err
}

// errStop marks an item error that ends the run: the items left count as
// failed.
var errStop = errors.New("run stopped")

// eachItem calls restore for every archived item of a store, in path
// order, reporting progress. A failed item is counted and logged; the run
// goes on with the rest.
func eachItem(
	ctx context.Context, store archive.Store, progress backup.Progress, logger *slog.Logger, res *Result,
	restore func(folder, id string) error,
) error {
	have, err := store.Scan()
	if err != nil {
		return err
	}

	var items []string

	for id, folders := range have {
		for _, folder := range folders {
			items = append(items, path.Join(folder, id))
		}
	}

	slices.Sort(items)

	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("restore: %w", err)
		}

		progress.Report(i, len(items))

		folder, id := path.Split(item)

		err := restore(strings.TrimSuffix(folder, "/"), id)
		if errors.Is(err, errStop) {
			res.Failed += len(items) - i

			return err
		}

		if err != nil {
			res.Failed++
			logger.Error("restore item", slog.String("item", item), slog.String("error", err.Error()))
		}
	}

	progress.Report(len(items), len(items))

	return nil
}

// gone treats deleting an item that is already gone as done: a rollback
// after a retried delete, or an occurrence canceled already.
func gone(err error) error {
	var apiErr *googleapi.APIError
	if errors.Is(err, graph.ErrNotFound) || errors.Is(err, googleapi.ErrNotFound) ||
		errors.As(err, &apiErr) && apiErr.Status == http.StatusGone {
		return nil
	}

	return err
}
