package restore

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/emersion/go-vcard"

	"github.com/tonimelisma/pimvault/internal/gpeople"
)

// googleContact is an archived contact read for writing into Google
// Contacts.
type googleContact struct {
	uid        string
	emails     []string
	categories []string
	person     *gpeople.NewPerson
	photo      *contactPhoto
}

// readGoogleContact converts a vCard into a Google contact. CATEGORIES
// become contact groups (D67), set by the caller.
func readGoogleContact(raw []byte) (*googleContact, error) {
	card, err := vcard.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("parse vcard: %w", err)
	}

	p := &gpeople.NewPerson{}

	identity(card, p)
	contactPoints(card, p)
	events(card, p)

	if notes := card.Values(vcard.FieldNote); len(notes) > 0 {
		p.Biographies = []gpeople.Value{{Value: strings.Join(notes, "\n\n")}}
	}

	for _, r := range card[vcard.FieldRelated] {
		p.Relations = append(p.Relations, gpeople.NewRelation{Person: r.Value, Type: googleRelationType(r.Params.Types())})
	}

	messaging(card, p)

	return &googleContact{
		uid: card.Value(vcard.FieldUID), emails: lower(card.Values(vcard.FieldEmail)), categories: card.Values(vcard.FieldCategories),
		person: p, photo: photoOf(card),
	}, nil
}

// identity sets names, nicknames, the organization and the gender.
func identity(card vcard.Card, p *gpeople.NewPerson) {
	if n := card.Get(vcard.FieldName); n != nil && strings.Trim(n.Value, ";") != "" {
		parts := splitStructured(n.Value, nameParts)
		p.Names = []gpeople.NewName{{
			FamilyName: parts[0], GivenName: parts[1], MiddleName: parts[2], HonorificPrefix: parts[3], HonorificSuffix: parts[4],
		}}
	} else if fn := card.Value(vcard.FieldFormattedName); fn != "" {
		p.Names = []gpeople.NewName{{UnstructuredName: fn}}
	}

	for _, v := range card.Values(vcard.FieldNickname) {
		p.Nicknames = append(p.Nicknames, gpeople.Value{Value: v})
	}

	if org, title := card.Get(vcard.FieldOrganization), card.Value(vcard.FieldTitle); org != nil || title != "" {
		o := gpeople.NewOrganization{Title: title}
		if org != nil {
			parts := splitStructured(org.Value, orgParts)
			o.Name, o.Department = parts[0], parts[1]
		}

		p.Organizations = []gpeople.NewOrganization{o}
	}

	if g := card.Value(vcard.FieldGender); g != "" {
		p.Genders = []gpeople.Value{{Value: cmp.Or(map[string]string{"M": "male", "F": "female"}[strings.SplitN(g, ";", 2)[0]], g)}}
	}
}

// contactPoints sets email addresses, phone numbers, postal addresses,
// URLs and calendar addresses.
func contactPoints(card vcard.Card, p *gpeople.NewPerson) {
	for _, e := range card[vcard.FieldEmail] {
		p.EmailAddresses = append(p.EmailAddresses, gpeople.Value{Value: e.Value, Type: kindType(e.Params)})
	}

	for _, tel := range card[vcard.FieldTelephone] {
		p.PhoneNumbers = append(p.PhoneNumbers, gpeople.Value{Value: tel.Value, Type: googlePhoneType(tel.Params.Types())})
	}

	for _, adr := range card[vcard.FieldAddress] {
		a := splitStructured(adr.Value, adrParts)
		p.Addresses = append(p.Addresses, gpeople.NewAddress{
			PoBox: a[0], ExtendedAddress: a[1], StreetAddress: a[2], City: a[3], Region: a[4], PostalCode: a[5], Country: a[6],
			Type: kindType(adr.Params),
		})
	}

	for _, u := range card[vcard.FieldURL] {
		p.URLs = append(p.URLs, gpeople.Value{Value: u.Value, Type: kindType(u.Params)})
	}

	for _, c := range card.Values(vcard.FieldCalendarURI) {
		p.CalendarURLs = append(p.CalendarURLs, gpeople.NewCalendarURL{URL: c})
	}
}

// events sets the birthday and anniversaries.
func events(card vcard.Card, p *gpeople.NewPerson) {
	if b := card.Get(vcard.FieldBirthday); b != nil {
		p.Birthdays = []gpeople.NewDate{googleDate(b)}
	}

	for _, a := range card[vcard.FieldAnniversary] {
		d := googleDate(a)
		d.Type = "anniversary"
		p.Events = append(p.Events, d)
	}
}

// messaging maps IMPP: "sip:" addresses are SIP addresses, others instant
// messaging addresses with their scheme as the protocol.
func messaging(card vcard.Card, p *gpeople.NewPerson) {
	for _, im := range card.Values(vcard.FieldIMPP) {
		scheme, user, ok := strings.Cut(im, ":")

		switch {
		case ok && strings.EqualFold(scheme, "sip"):
			p.SIPAddresses = append(p.SIPAddresses, gpeople.Value{Value: user})
		case ok && scheme == "im":
			p.IMClients = append(p.IMClients, gpeople.NewIMClient{Username: user})
		case ok && !strings.Contains(scheme, "@"):
			p.IMClients = append(p.IMClients, gpeople.NewIMClient{Username: user, Protocol: scheme})
		default:
			p.IMClients = append(p.IMClients, gpeople.NewIMClient{Username: im})
		}
	}
}

// googleDate reads a date-and-or-time as a date: YYYYMMDD, YYYY-MM-DD,
// --MMDD or --MM-DD, else as text.
func googleDate(f *vcard.Field) gpeople.NewDate {
	v := strings.ReplaceAll(f.Value, "-", "")
	if i := strings.IndexByte(v, 'T'); i >= 0 {
		v = v[:i]
	}

	yearless := strings.HasPrefix(f.Value, "--")
	if f.Params.Get("VALUE") != valueText && (len(v) == len("20060102") && !yearless || len(v) == len("0102") && yearless) {
		year := 0
		if !yearless {
			year, _ = strconv.Atoi(v[:4]) //nolint:errcheck // checked with the rest below.
			v = v[4:]
		}

		month, monthErr := strconv.Atoi(v[:2])
		day, dayErr := strconv.Atoi(v[2:4])

		if monthErr == nil && dayErr == nil && month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			return gpeople.NewDate{Date: &gpeople.Date{Year: year, Month: month, Day: day}}
		}
	}

	return gpeople.NewDate{Text: f.Value}
}

// kindType is Google's type for a home or work value, or "".
func kindType(params vcard.Params) string {
	switch {
	case params.HasType(typeHome):
		return typeHome
	case params.HasType(typeWork):
		return typeWork
	default:
		return ""
	}
}

// googlePhoneType maps vCard TEL types to Google's phone types.
func googlePhoneType(types []string) string {
	has := func(t string) bool { return slices.Contains(types, t) }
	work, home := has(typeWork), has(typeHome)

	switch {
	case has("fax") && work:
		return "workFax"
	case has("fax") && home:
		return "homeFax"
	case has("fax"):
		return "otherFax"
	case has("pager") && work:
		return "workPager"
	case has("pager"):
		return "pager"
	case has("cell") && work:
		return "workMobile"
	case has("cell"):
		return "mobile"
	case work:
		return typeWork
	case home:
		return typeHome
	default:
		return ""
	}
}

// googleRelationType maps vCard RELATED types to Google's relation types;
// other types are kept as written, which Google shows as a custom type.
func googleRelationType(types []string) string {
	if len(types) == 0 {
		return ""
	}

	t := strings.ToLower(types[0])
	if g, ok := map[string]string{"sweetheart": "partner", "kin": "relative"}[t]; ok {
		return g
	}

	return t
}
