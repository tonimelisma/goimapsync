package graph

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/tonimelisma/pimvault/internal/vcardfile"
)

// vCard TYPE values (RFC 6350 section 6.4.1).
const (
	vcardHome  = "home"
	vcardWork  = "work"
	vcardVoice = "voice"
)

// EncodeContact renders a contact as vCard 4.0 with standard properties
// only (D150); photo, when not nil, is embedded (D151).
func EncodeContact(c *Contact, photo *Photo) ([]byte, error) {
	card := vcardfile.New(c.ID)

	card.SetText("FN", contactName(c))
	card.Structured("N", []string{c.Surname, c.GivenName, c.MiddleName, c.Title, c.Generation})
	card.Text("NICKNAME", c.NickName)

	for _, e := range c.EmailAddresses {
		card.Text("EMAIL", e.Address)
	}

	phones := []struct {
		numbers []string
		types   []string
	}{
		{c.BusinessPhones, []string{vcardWork, vcardVoice}},
		{c.HomePhones, []string{vcardHome, vcardVoice}},
		{[]string{c.MobilePhone}, []string{"cell"}},
	}

	for _, p := range phones {
		for _, n := range p.numbers {
			card.Text("TEL", n, types(p.types...)...)
		}
	}

	addresses := []struct {
		a     *PhysicalAddress
		types []string
	}{
		{&c.HomeAddress, []string{vcardHome}},
		{&c.BusinessAddress, []string{vcardWork}},
		{&c.OtherAddress, nil},
	}

	for _, a := range addresses {
		card.Structured("ADR", []string{"", "", a.a.Street, a.a.City, a.a.State, a.a.PostalCode, a.a.CountryOrRegion}, types(a.types...)...)
	}

	card.Structured("ORG", orgParts(c.CompanyName, c.Department))
	card.Text("TITLE", c.JobTitle)
	card.URI("URL", c.BusinessHomePage, vcardfile.Type(vcardWork))
	card.Text("NOTE", c.PersonalNotes)

	card.Text("RELATED", c.SpouseName, vcardfile.TextValue(), vcardfile.Type("spouse"))

	for _, child := range c.Children {
		card.Text("RELATED", child, vcardfile.TextValue(), vcardfile.Type("child"))
	}

	for _, im := range c.IMAddresses {
		if !strings.Contains(im, ":") {
			im = "im:" + im
		}

		card.URI("IMPP", im)
	}

	card.Categories(c.Categories)

	if photo != nil {
		card.Photo(photo.Data, photo.ContentType)
	}

	if err := contactDates(card, c); err != nil {
		return nil, err
	}

	return card.Encode(), nil
}

// contactName is FN: the display name, or else the name parts, company,
// email address or mobile number. FN is required.
func contactName(c *Contact) string {
	email := ""
	if len(c.EmailAddresses) > 0 {
		email = c.EmailAddresses[0].Address
	}

	return cmp.Or(
		c.DisplayName,
		strings.Join(strings.Fields(c.GivenName+" "+c.MiddleName+" "+c.Surname), " "),
		c.CompanyName,
		email,
		c.MobilePhone,
	)
}

// orgParts is ORG's company and department, or only the company.
func orgParts(company, department string) []string {
	if department == "" {
		return []string{company}
	}

	return []string{company, department}
}

func types(values ...string) []vcardfile.Param {
	params := make([]vcardfile.Param, len(values))
	for i, v := range values {
		params[i] = vcardfile.Type(v)
	}

	return params
}

// contactDates sets BDAY and REV. Outlook stores a birthday as an instant
// at 11:59 UTC on the day (F46); only the date is kept.
func contactDates(card *vcardfile.Card, c *Contact) error {
	if c.Birthday != "" {
		t, err := time.Parse(time.RFC3339, c.Birthday)
		if err != nil {
			return fmt.Errorf("parse birthday of contact %s: %w", c.ID, err)
		}

		card.Text("BDAY", t.UTC().Format("20060102"))
	}

	if c.LastModifiedDateTime != "" {
		t, err := time.Parse(time.RFC3339, c.LastModifiedDateTime)
		if err != nil {
			return fmt.Errorf("parse last change of contact %s: %w", c.ID, err)
		}

		card.Text("REV", t.UTC().Format("20060102T150405Z"))
	}

	return nil
}
