// Package mailheader reads a message's header block leniently: providers
// keep messages Go's net/mail refuses (F70), and pimvault restores and
// verifies them all the same. The header is everything before the first
// empty line; lines that are not fields are kept but ignored. A message
// whose first paragraph holds no field has no header.
package mailheader

import (
	"bytes"
	"strings"
)

// Header is a message's header block.
type Header struct {
	Raw    []byte
	Fields []Field
	// EOL is the message's line ending, for what is added to it.
	EOL string
}

// Field is one header field: Raw[Start:End], with its folded lines and the
// final line ending. Name is lowercased.
type Field struct {
	Name       string
	Start, End int
}

// Parse reads a message's header block.
func Parse(raw []byte) Header {
	h := Header{Raw: raw, EOL: "\r\n"}
	if i := bytes.IndexByte(raw, '\n'); i >= 0 && (i == 0 || raw[i-1] != '\r') {
		h.EOL = "\n"
	}

	for pos := 0; pos < len(raw); {
		next := len(raw)
		if i := bytes.IndexByte(raw[pos:], '\n'); i >= 0 {
			next = pos + i + 1
		}

		line := strings.TrimRight(string(raw[pos:next]), "\r\n")

		switch {
		case line == "":
			return h
		case line[0] == ' ' || line[0] == '\t':
			if n := len(h.Fields); n > 0 && h.Fields[n-1].End == pos {
				h.Fields[n-1].End = next
			}
		default:
			if name, _, ok := strings.Cut(line, ":"); ok && fieldName(name) {
				h.Fields = append(h.Fields, Field{Name: strings.ToLower(name), Start: pos, End: next})
			}
		}

		pos = next
	}

	return h
}

// fieldName reports whether name is a field name (RFC 5322 section 2.2):
// printable US-ASCII except colon, no spaces.
func fieldName(name string) bool {
	return name != "" && !strings.ContainsFunc(name, func(r rune) bool { return r < '!' || r > '~' })
}

// Values returns the unfolded, trimmed values of the fields with a name, in
// order.
func (h Header) Values(name string) []string {
	var vals []string

	for _, f := range h.Fields {
		if f.Name == name {
			_, value, _ := strings.Cut(string(h.Raw[f.Start:f.End]), ":")
			value = strings.NewReplacer("\r\n", "", "\n", "").Replace(value)
			vals = append(vals, strings.TrimSpace(value))
		}
	}

	return vals
}

// Get returns the value of the first field with a name, or "".
func (h Header) Get(name string) string {
	if vals := h.Values(name); len(vals) > 0 {
		return vals[0]
	}

	return ""
}
