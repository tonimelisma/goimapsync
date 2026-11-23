package gpeople

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/tonimelisma/pimvault/internal/vcardfile"
)

// Encode renders a contact as vCard 4.0 with standard properties only
// (D150). groups maps contact group resource names to their names, which
// become CATEGORIES (D67); photo, when not nil, is embedded (D151).
func Encode(p *Person, groups map[string]string, photo *Photo) ([]byte, error) {
	card := vcardfile.New(p.ResourceName)

	names(card, p)
	communication(card, p)
	organization(card, p)
	dates(card, p)
	other(card, p)
	categories(card, p, groups)

	if photo != nil {
		card.Photo(photo.Data, photo.ContentType)
	}

	if err := revision(card, p); err != nil {
		return nil, err
	}

	return card.Encode(), nil
}

// communication sets EMAIL, TEL, ADR, URL and IMPP.
func communication(card *vcardfile.Card, p *Person) {
	for _, e := range p.EmailAddresses {
		card.Text("EMAIL", e.Value, params(kindTypes(e.Type), e.Metadata.Primary)...)
	}

	for _, ph := range p.PhoneNumbers {
		card.Text("TEL", ph.Value, params(phoneTypes(ph.Type), ph.Metadata.Primary)...)
	}

	for i := range p.Addresses {
		a := &p.Addresses[i]
		card.Structured("ADR", []string{a.PoBox, a.ExtendedAddress, a.StreetAddress, a.City, a.Region, a.PostalCode, a.Country},
			params(kindTypes(a.Type), false)...)
	}

	for _, u := range p.URLs {
		card.URI("URL", u.Value, params(kindTypes(u.Type), false)...)
	}

	for _, im := range p.IMClients {
		if im.Username != "" {
			card.URI("IMPP", cmp.Or(im.Protocol, "im")+":"+im.Username)
		}
	}

	for _, sip := range p.SIPAddresses {
		if sip.Value != "" {
			card.URI("IMPP", "sip:"+sip.Value)
		}
	}
}

// organization sets ORG and TITLE from the first organization.
func organization(card *vcardfile.Card, p *Person) {
	if len(p.Organizations) == 0 {
		return
	}

	o := p.Organizations[0]
	if o.Department != "" {
		card.Structured("ORG", []string{o.Name, o.Department})
	} else {
		card.Structured("ORG", []string{o.Name})
	}

	card.Text("TITLE", o.Title)
}

// other sets NICKNAME, NOTE, RELATED, GENDER and CALURI.
func other(card *vcardfile.Card, p *Person) {
	for _, n := range p.Nicknames {
		card.Text("NICKNAME", n.Value)
	}

	for _, b := range p.Biographies {
		card.Text("NOTE", b.Value)
	}

	for _, r := range p.Relations {
		ps := []vcardfile.Param{vcardfile.TextValue()}
		if t := relationType(r.Type); t != "" {
			ps = append(ps, vcardfile.Type(t))
		}

		card.Text("RELATED", r.Person, ps...)
	}

	if len(p.Genders) > 0 {
		card.Text("GENDER", map[string]string{"male": "M", "female": "F"}[p.Genders[0].Value])
	}

	for _, c := range p.CalendarURLs {
		card.URI("CALURI", c.URL)
	}
}

// categories sets CATEGORIES from the contact's own groups.
func categories(card *vcardfile.Card, p *Person, groups map[string]string) {
	var names []string

	for _, m := range p.Memberships {
		if name, ok := groups[m.ContactGroupMembership.ContactGroupResourceName]; ok {
			names = append(names, name)
		}
	}

	card.Categories(names)
}

// params turns types and a preferred flag into parameters.
func params(types []string, pref bool) []vcardfile.Param {
	ps := make([]vcardfile.Param, 0, len(types)+1)
	for _, t := range types {
		ps = append(ps, vcardfile.Type(t))
	}

	if pref {
		ps = append(ps, vcardfile.Pref())
	}

	return ps
}

// names sets FN and N from the primary name. FN is required; without a name
// it falls back to the organization, then an email address or phone number.
func names(card *vcardfile.Card, p *Person) {
	var fn string

	if len(p.Names) > 0 {
		n := p.Names[max(slices.IndexFunc(p.Names, func(n Name) bool { return n.Metadata.Primary }), 0)]
		fn = n.DisplayName
		card.Structured("N", []string{n.FamilyName, n.GivenName, n.MiddleName, n.HonorificPrefix, n.HonorificSuffix})
	}

	if fn == "" && len(p.Organizations) > 0 {
		fn = p.Organizations[0].Name
	}

	if fn == "" && len(p.EmailAddresses) > 0 {
		fn = p.EmailAddresses[0].Value
	}

	if fn == "" && len(p.PhoneNumbers) > 0 {
		fn = p.PhoneNumbers[0].Value
	}

	card.SetText("FN", fn)
}

// dates sets BDAY and ANNIVERSARY.
func dates(card *vcardfile.Card, p *Person) {
	for _, b := range p.Birthdays {
		if value := dateValue(b.Date); value != "" {
			card.Text("BDAY", value)

			break
		}

		if b.Text != "" {
			card.Text("BDAY", b.Text, vcardfile.TextValue())

			break
		}
	}

	// vCard allows one ANNIVERSARY (RFC 6350 section 6.2.6): the first.
	for _, e := range p.Events {
		if value := dateValue(e.Date); e.Type == "anniversary" && value != "" {
			card.Text("ANNIVERSARY", value)

			break
		}
	}
}

// dateValue writes a date as YYYYMMDD, or --MMDD without a year.
func dateValue(d *Date) string {
	switch {
	case d == nil || d.Month == 0 || d.Day == 0:
		return ""
	case d.Year == 0:
		return fmt.Sprintf("--%02d%02d", d.Month, d.Day)
	default:
		return fmt.Sprintf("%04d%02d%02d", d.Year, d.Month, d.Day)
	}
}

// revision sets REV from the contact's last update.
func revision(card *vcardfile.Card, p *Person) error {
	for _, s := range p.Metadata.Sources {
		if s.Type != "CONTACT" || s.UpdateTime == "" {
			continue
		}

		t, err := time.Parse(time.RFC3339, s.UpdateTime)
		if err != nil {
			return fmt.Errorf("parse update time of contact %s: %w", p.ResourceName, err)
		}

		card.Text("REV", t.UTC().Format("20060102T150405Z"))

		return nil
	}

	return nil
}

// vCard TYPE values (RFC 6350 sections 6.4.1 and 6.6.6).
const (
	typeHome    = "home"
	typeWork    = "work"
	typeVoice   = "voice"
	typeCell    = "cell"
	typeFax     = "fax"
	typePager   = "pager"
	typeParent  = "parent"
	typeSibling = "sibling"
)

// kindTypes maps Google's home/work types; other types have no vCard TYPE.
func kindTypes(t string) []string {
	switch t {
	case typeHome, typeWork:
		return []string{t}
	default:
		return nil
	}
}

// phoneTypes maps Google phone types to vCard TEL types.
func phoneTypes(t string) []string {
	switch t {
	case "home":
		return []string{typeHome, typeVoice}
	case "work":
		return []string{typeWork, typeVoice}
	case "mobile":
		return []string{typeCell}
	case "workMobile":
		return []string{typeWork, typeCell}
	case "homeFax":
		return []string{typeHome, typeFax}
	case "workFax":
		return []string{typeWork, typeFax}
	case "otherFax":
		return []string{typeFax}
	case "pager":
		return []string{typePager}
	case "workPager":
		return []string{typeWork, typePager}
	case "main":
		return []string{typeVoice}
	default:
		return nil
	}
}

// relationType maps Google relation types to vCard RELATED types (RFC 6350
// section 6.6.6); others have none.
func relationType(t string) string {
	switch t {
	case "spouse", "child", typeParent, typeSibling, "friend":
		return t
	case "partner", "domesticPartner":
		return "sweetheart"
	case "brother", "sister":
		return typeSibling
	case "father", "mother":
		return typeParent
	case "assistant", "manager":
		return "co-worker"
	case "relative":
		return "kin"
	default:
		return ""
	}
}
