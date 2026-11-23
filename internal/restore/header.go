package restore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/mailheader"
)

// headerDate returns a message's Date, if it has one that parses.
func headerDate(h mailheader.Header) (time.Time, bool) {
	t, err := mail.ParseDate(h.Get("date"))

	return t, err == nil
}

// received returns the date of the newest Received field that has one: the
// time the message reached its last server.
func received(h mailheader.Header) (time.Time, bool) {
	for _, v := range h.Values("received") {
		if i := strings.LastIndexByte(v, ';'); i >= 0 {
			if t, err := mail.ParseDate(strings.TrimSpace(v[i+1:])); err == nil {
				return t, true
			}
		}
	}

	return time.Time{}, false
}

// repaired returns the message as it is written into an account (D184). A
// message without a Date that parses gets one from its newest dated
// Received field, else from modified, the archive file's modification
// time; fileDated reports the latter. A message without a Message-ID gets
// one derived from its content. The fields replaced are removed. The
// archive keeps the message as it is.
func repaired(raw []byte, modified func() (time.Time, error)) (out []byte, fileDated bool, err error) {
	h := mailheader.Parse(raw)

	var (
		add     []string
		replace = map[string]bool{}
	)

	if _, dated := headerDate(h); !dated {
		t, found := received(h)
		if !found {
			if t, err = modified(); err != nil {
				return nil, false, fmt.Errorf("date message without one: %w", err)
			}

			t, fileDated = t.UTC(), true
		}

		add = append(add, "Date: "+t.Format(time.RFC1123Z))
		replace["date"] = true
	}

	if messageID(h) == "" {
		sum := sha256.Sum256(raw)
		add = append(add, "Message-ID: <"+hex.EncodeToString(sum[:16])+"@pimvault.invalid>")
		replace["message-id"] = true
	}

	if len(add) == 0 {
		return raw, false, nil
	}

	var b bytes.Buffer

	for _, f := range add {
		b.WriteString(f + h.EOL)
	}

	if len(h.Fields) == 0 {
		b.WriteString(h.EOL)
		b.Write(raw)

		return b.Bytes(), fileDated, nil
	}

	pos := 0

	for _, f := range h.Fields {
		if replace[f.Name] {
			b.Write(raw[pos:f.Start])
			pos = f.End
		}
	}

	b.Write(raw[pos:])

	return b.Bytes(), fileDated, nil
}

// readMessage reads an archived message for writing: repaired (D184), with
// the keys a copy of it may have in the account, the repaired message's
// first. A copy written before the repair carries the archived message's
// key, if it has one. A date taken from the file is left out of the key:
// the file's time changes when it is written again, its content does not.
func readMessage(store archive.Store, folder, id string) (raw []byte, keys []mailKey, err error) {
	archived, err := store.Read(folder, id)
	if err != nil {
		return nil, nil, err
	}

	raw, fileDated, err := repaired(archived, func() (time.Time, error) { return store.ModTime(folder, id) })
	if err != nil {
		return nil, nil, err
	}

	key, _ := keyOf(raw)
	if fileDated {
		key.date = ""
	}

	keys = []mailKey{key}

	if archivedKey, ok := keyOf(archived); ok && archivedKey != key {
		keys = append(keys, archivedKey)
	}

	return raw, keys, nil
}

// presentAs reports whether a copy of a message is there under one of its
// keys: written by this run, or found by lookup.
func presentAs(keys []mailKey, written map[mailKey]bool, lookup func(mailKey) (bool, error)) (bool, error) {
	for _, key := range keys {
		if written[key] {
			return true, nil
		}

		if found, err := lookup(key); err != nil || found {
			return found, err
		}
	}

	return false, nil
}

// messageID returns a message's Message-ID with the whitespace folding left
// in it removed, or "".
func messageID(h mailheader.Header) string {
	return strings.Join(strings.Fields(h.Get("message-id")), "")
}
