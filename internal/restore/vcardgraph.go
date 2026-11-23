package restore

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/emersion/go-vcard"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// Outlook's limits on repeated contact fields.
const (
	maxEmails = 3
	maxPhones = 2
	maxIMs    = 3
)

// Parts of structured vCard values (RFC 6350 sections 6.2.2, 6.3.1, 6.6.4).
const (
	nameParts = 5 // family, given, additional, prefix, suffix
	adrParts  = 7 // PO box, extended, street, locality, region, postal code, country
	orgParts  = 2 // name, unit
)

// vCard parameter values.
const (
	typeHome  = "home"
	typeWork  = "work"
	valueText = "text"
)

// contactPhoto is a photo taken from a vCard's PHOTO data URI.
type contactPhoto struct {
	data        []byte
	contentType string
}

// parsedContact is an archived contact read for writing elsewhere.
type parsedContact struct {
	uid     string
	emails  []string
	contact *graph.NewContact
	photo   *contactPhoto
}

// outlookContact converts a vCard into an Outlook contact. Values Outlook
// has no place for are appended to the notes as "Label: value" lines (D68).
func outlookContact(raw []byte) (*parsedContact, error) {
	card, err := vcard.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("parse vcard: %w", err)
	}

	c := &graph.NewContact{
		DisplayName: card.Value(vcard.FieldFormattedName),
		NickName:    card.Value(vcard.FieldNickname),
		JobTitle:    card.Value(vcard.FieldTitle),
		Categories:  card.Values(vcard.FieldCategories), // D67
	}

	var extra []string

	note := func(label, value string) {
		if value != "" {
			extra = append(extra, label+": "+value)
		}
	}

	if n := card.Get(vcard.FieldName); n != nil {
		p := splitStructured(n.Value, nameParts)
		c.Surname, c.GivenName, c.MiddleName, c.Title, c.Generation = p[0], p[1], p[2], p[3], p[4]
	}

	if org := card.Get(vcard.FieldOrganization); org != nil {
		p := splitStructured(org.Value, orgParts)
		c.CompanyName, c.Department = p[0], p[1]
	}

	emails := card.Values(vcard.FieldEmail)
	for i, e := range emails {
		if i < maxEmails {
			c.EmailAddresses = append(c.EmailAddresses, graph.EmailAddress{Address: e})
		} else {
			note("Email", e)
		}
	}

	phones(card, c, note)
	addresses(card, c, note)
	links(card, c, note)
	dates(card, c, note)
	related(card, c, note)

	note("Gender", card.Value(vcard.FieldGender))

	c.PersonalNotes = card.Value(vcard.FieldNote)
	if len(extra) > 0 {
		c.PersonalNotes = strings.TrimSpace(c.PersonalNotes + "\n\n" + strings.Join(extra, "\n"))
	}

	return &parsedContact{uid: card.Value(vcard.FieldUID), emails: lower(emails), contact: c, photo: photoOf(card)}, nil
}

// phones maps TEL by type: one mobile, two work and two home numbers.
func phones(card vcard.Card, c *graph.NewContact, note func(label, value string)) {
	for _, tel := range card[vcard.FieldTelephone] {
		types := tel.Params.Types()
		work := slices.Contains(types, typeWork)

		switch {
		case slices.Contains(types, "fax") || slices.Contains(types, "pager"):
			note("Phone ("+strings.Join(types, ", ")+")", tel.Value)
		case slices.Contains(types, "cell") && c.MobilePhone == "":
			c.MobilePhone = tel.Value
		case work && len(c.BusinessPhones) < maxPhones:
			c.BusinessPhones = append(c.BusinessPhones, tel.Value)
		case !work && len(c.HomePhones) < maxPhones:
			c.HomePhones = append(c.HomePhones, tel.Value)
		default:
			note("Phone", tel.Value)
		}
	}
}

// addresses maps ADR by type: one home, one work and one other address.
func addresses(card vcard.Card, c *graph.NewContact, note func(label, value string)) {
	for _, adr := range card[vcard.FieldAddress] {
		p := splitStructured(adr.Value, adrParts)
		a := &graph.PhysicalAddress{
			Street: strings.Join(nonEmpty(p[0], p[1], p[2]), "\n"),
			City:   p[3], State: p[4], PostalCode: p[5], CountryOrRegion: p[6],
		}

		slot := &c.OtherAddress

		switch {
		case adr.Params.HasType(typeHome):
			slot = &c.HomeAddress
		case adr.Params.HasType(typeWork):
			slot = &c.BusinessAddress
		}

		if *slot != nil {
			note("Address", strings.Join(nonEmpty(a.Street, a.City, a.State, a.PostalCode, a.CountryOrRegion), ", "))

			continue
		}

		*slot = a
	}
}

// links maps URL and IMPP; extras and calendar addresses go to the notes.
func links(card vcard.Card, c *graph.NewContact, note func(label, value string)) {
	for i, u := range card.Values(vcard.FieldURL) {
		if i == 0 {
			c.BusinessHomePage = u
		} else {
			note("Website", u)
		}
	}

	for i, im := range card.Values(vcard.FieldIMPP) {
		if i < maxIMs {
			c.IMAddresses = append(c.IMAddresses, im)
		} else {
			note("IM", im)
		}
	}

	note("Calendar", card.Value(vcard.FieldCalendarURI))
}

// dates maps BDAY; a birthday without a year, and anniversaries, go to the
// notes because Outlook needs a full date.
func dates(card vcard.Card, c *graph.NewContact, note func(label, value string)) {
	if b := card.Get(vcard.FieldBirthday); b != nil {
		v := b.Value
		if len(v) == len("20060102") && b.Params.Get("VALUE") != valueText {
			c.Birthday = v[0:4] + "-" + v[4:6] + "-" + v[6:8] + "T11:59:00Z" // Outlook's own form (F46)
		} else {
			note("Birthday", v)
		}
	}

	note("Anniversary", card.Value(vcard.FieldAnniversary))
}

// related maps RELATED: spouse and children have fields; others go to notes.
func related(card vcard.Card, c *graph.NewContact, note func(label, value string)) {
	for _, r := range card[vcard.FieldRelated] {
		switch {
		case r.Params.HasType("spouse") && c.SpouseName == "":
			c.SpouseName = r.Value
		case r.Params.HasType("child"):
			c.Children = append(c.Children, r.Value)
		default:
			label := "Related"
			if types := r.Params.Types(); len(types) > 0 {
				label += " (" + strings.Join(types, ", ") + ")"
			}

			note(label, r.Value)
		}
	}
}

// photoOf reads a PHOTO data URI.
func photoOf(card vcard.Card) *contactPhoto {
	uri, isData := strings.CutPrefix(card.Value(vcard.FieldPhoto), "data:")

	meta, data, ok := strings.Cut(uri, ",")
	if !isData || !ok {
		return nil
	}

	contentType, isBase64 := strings.CutSuffix(meta, ";base64")
	if !isBase64 {
		return nil
	}

	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil
	}

	return &contactPhoto{data: decoded, contentType: contentType}
}

// splitStructured splits a structured value at semicolons that are not
// escaped and unescapes "\;" (go-vcard has already unescaped the rest). The
// result has at least n parts; missing ones are empty.
func splitStructured(v string, n int) []string {
	var (
		parts []string
		b     strings.Builder
	)

	for i := 0; i < len(v); i++ {
		switch {
		case v[i] == '\\' && i+1 < len(v) && v[i+1] == ';':
			b.WriteByte(';')
			i++
		case v[i] == ';':
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteByte(v[i])
		}
	}

	parts = append(parts, b.String())
	for len(parts) < n {
		parts = append(parts, "")
	}

	return parts
}

func nonEmpty(values ...string) []string {
	return slices.DeleteFunc(values, func(s string) bool { return s == "" })
}

func lower(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.ToLower(v)
	}

	return out
}
