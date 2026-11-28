// Package vcardfile writes vCard 4.0 files (RFC 6350, D42). go-vcard (D88)
// parses them; its encoder is not used because it escapes commas in URI
// values (breaking data: photos) and cannot escape semicolons inside
// structured values.
package vcardfile

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"slices"
	"strings"
	"unicode/utf8"
)

// ProductID identifies pimvault in the files it writes.
const ProductID = "-//pimvault//pimvault//EN"

// foldAt is the longest line RFC 6350 recommends, in octets.
const foldAt = 75

// Param is a property parameter.
type Param struct {
	Name, Value string
}

// Type is a TYPE parameter.
func Type(value string) Param { return Param{"TYPE", value} }

// TextValue marks a property whose value is free text rather than its
// default value type (such as a date or URI).
func TextValue() Param { return Param{"VALUE", "text"} }

// Pref marks the preferred value of its kind.
func Pref() Param { return Param{"PREF", "1"} }

// Card is a vCard being built. Properties are written sorted by name, in the
// order they were added within a name, so the same contact always gives the
// same bytes.
type Card struct {
	props map[string][]string // name → content lines after the name
}

// New returns a card with VERSION, PRODID and UID.
func New(uid string) *Card {
	c := &Card{props: make(map[string][]string)}
	c.Text("PRODID", ProductID)
	c.Text("UID", uid)

	return c
}

// Text adds a text property unless value is empty.
func (c *Card) Text(name, value string, params ...Param) {
	if value != "" {
		c.add(name, escape(value), params)
	}
}

// SetText replaces a text property; an empty value is written as such.
func (c *Card) SetText(name, value string, params ...Param) {
	delete(c.props, name)
	c.add(name, escape(value), params)
}

// URI adds a property whose value is a URI, which is written unescaped.
func (c *Card) URI(name, value string, params ...Param) {
	if value != "" {
		c.add(name, strings.NewReplacer("\r", "", "\n", "").Replace(value), params)
	}
}

// Structured sets a property made of parts separated by semicolons, such as
// N, ADR or ORG. Nothing is written when every part is empty.
func (c *Card) Structured(name string, parts []string, params ...Param) {
	if strings.Join(parts, "") == "" {
		return
	}

	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = strings.ReplaceAll(escape(p), ";", `\;`)
	}

	c.add(name, strings.Join(escaped, ";"), params)
}

// Photo embeds a photo as a data URI (D151).
func (c *Card) Photo(data []byte, contentType string) {
	if len(data) > 0 {
		c.URI("PHOTO", "data:"+cmp.Or(contentType, "image/jpeg")+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
}

// Categories adds one CATEGORIES property per name, sorted (D67).
func (c *Card) Categories(names []string) {
	for _, name := range slices.Sorted(slices.Values(names)) {
		c.Text("CATEGORIES", name)
	}
}

func (c *Card) add(name string, value string, params []Param) {
	var b strings.Builder

	params = slices.Clone(params)
	slices.SortStableFunc(params, func(a, b Param) int { return cmp.Compare(a.Name, b.Name) })

	for _, p := range params {
		b.WriteString(";" + p.Name + "=" + paramValue(p.Value))
	}

	b.WriteString(":" + value)
	c.props[name] = append(c.props[name], b.String())
}

// Encode writes the card, folding long lines.
func (c *Card) Encode() []byte {
	var buf bytes.Buffer

	buf.WriteString("BEGIN:VCARD\r\nVERSION:4.0\r\n")

	for _, name := range slices.Sorted(mapKeys(c.props)) {
		for _, rest := range c.props[name] {
			fold(&buf, name+rest)
		}
	}

	buf.WriteString("END:VCARD\r\n")

	return buf.Bytes()
}

// escape writes a text value: backslash, comma and line breaks are escaped
// (RFC 6350 section 3.4). Semicolons are escaped only inside structured
// values, where they separate the parts.
func escape(v string) string {
	return strings.NewReplacer(`\`, `\\`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`).Replace(v)
}

// paramValue quotes a parameter value that contains a separator; double
// quotes and line breaks cannot appear in one and become single quotes and
// spaces.
func paramValue(v string) string {
	v = strings.NewReplacer(`"`, "'", "\r\n", " ", "\n", " ", "\r", " ").Replace(v)
	if strings.ContainsAny(v, ":;,") {
		return `"` + v + `"`
	}

	return v
}

// fold writes a content line in pieces of at most 75 octets, continuing
// each with CRLF and a space, without splitting a UTF-8 character.
func fold(buf *bytes.Buffer, line string) {
	limit := foldAt

	for len(line) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}

		buf.WriteString(line[:cut] + "\r\n ")
		line = line[cut:]
		limit = foldAt - 1 // the leading space counts
	}

	buf.WriteString(line + "\r\n")
}

func mapKeys(m map[string][]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
