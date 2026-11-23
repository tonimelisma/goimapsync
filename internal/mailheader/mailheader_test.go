package mailheader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tonimelisma/pimvault/internal/mailheader"
)

func TestParseHeader(t *testing.T) {
	for name, tc := range map[string]struct {
		raw     string
		subject string
		fields  int
		eol     string
	}{
		"folded":            {raw: "Subject: a\r\n b\r\nX: y\r\n\r\nbody", subject: "a b", fields: 2, eol: "\r\n"},
		"bare LF":           {raw: "Subject: a\nX: y\n\nbody\r\n", subject: "a", fields: 2, eol: "\n"},
		"no body":           {raw: "Subject: a\r\n", subject: "a", fields: 1, eol: "\r\n"},
		"no final line end": {raw: "Subject: a", subject: "a", fields: 1, eol: "\r\n"},
		"no header":         {raw: "Hi there\r\n\r\nSubject: no\r\n", fields: 0, eol: "\r\n"},
		"empty":             {raw: "", fields: 0, eol: "\r\n"},
		// An mbox separator and a line without a colon are not fields;
		// the header still goes on.
		"stray lines": {raw: "From a@b Mon Mar  2 10:00:00 2026\r\nnot a field\r\nSubject: a\r\n\r\n", subject: "a", fields: 1, eol: "\r\n"},
		// A field name holds no spaces.
		"bad name": {raw: "Sub ject: no\r\nSubject: a\r\n\r\n", subject: "a", fields: 1, eol: "\r\n"},
	} {
		h := mailheader.Parse([]byte(tc.raw))
		assert.Equal(t, tc.subject, h.Get("subject"), name)
		assert.Len(t, h.Fields, tc.fields, name)
		assert.Equal(t, tc.eol, h.EOL, name)
	}
}
