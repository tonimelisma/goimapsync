package gpeople_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/emersion/go-vcard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/gpeople"
)

var update = flag.Bool("update", false, "rewrite golden files") //nolint:gochecknoglobals // test flag.

// person decodes a contact the way the People API sends it.
func person(t *testing.T, js string) *gpeople.Person {
	t.Helper()

	var p gpeople.Person
	require.NoError(t, json.Unmarshal([]byte(js), &p))

	return &p
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name+".vcf")
	if *update {
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}

	want, err := os.ReadFile(path) //nolint:gosec // G304: test data path.
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

const full = `{
  "resourceName": "people/c123",
  "metadata": {"sources": [{"type": "CONTACT", "updateTime": "2026-02-03T04:05:06.789Z"}]},
  "names": [{"displayName": "Dr. Ann B; Smith", "familyName": "Smith", "givenName": "Ann", "middleName": "B;C",
             "honorificPrefix": "Dr.", "metadata": {"primary": true}}],
  "nicknames": [{"value": "Annie"}],
  "emailAddresses": [{"value": "ann@work.example", "type": "work", "metadata": {"primary": true}},
                     {"value": "ann@home.example", "type": "home"}],
  "phoneNumbers": [{"value": "+358 40 123", "type": "mobile"}, {"value": "+358 9 555", "type": "workFax"}],
  "addresses": [{"streetAddress": "Main St 1\nFloor 2", "city": "Helsinki", "postalCode": "00100",
                 "country": "Finland", "type": "home"}],
  "organizations": [{"name": "Acme, Inc.", "department": "R&D", "title": "Engineer"}],
  "birthdays": [{"date": {"month": 5, "day": 17}}],
  "events": [{"date": {"year": 2010, "month": 6, "day": 1}, "type": "anniversary"}],
  "urls": [{"value": "https://ann.example", "type": "homePage"}],
  "biographies": [{"value": "Met at a conference.\r\nLikes tea."}],
  "relations": [{"person": "Bob", "type": "spouse"}],
  "imClients": [{"username": "ann@xmpp.example", "protocol": "xmpp"}],
  "genders": [{"value": "female"}],
  "memberships": [{"contactGroupMembership": {"contactGroupResourceName": "contactGroups/family"}},
                  {"contactGroupMembership": {"contactGroupResourceName": "contactGroups/work"}},
                  {"contactGroupMembership": {"contactGroupResourceName": "contactGroups/myContacts"}}]
}`

func TestEncode_Full(t *testing.T) {
	groups := map[string]string{"contactGroups/family": "Family, close", "contactGroups/work": "Work"}
	photo := &gpeople.Photo{Data: []byte("jpeg-bytes"), ContentType: "image/jpeg"}

	got, err := gpeople.Encode(person(t, full), groups, photo)
	require.NoError(t, err)
	golden(t, "full", got)

	card, err := vcard.NewDecoder(bytes.NewReader(got)).Decode()
	require.NoError(t, err)
	assert.Equal(t, []string{"Family, close", "Work"}, card.Values(vcard.FieldCategories), "one category per property")
	assert.Equal(t, "Met at a conference.\nLikes tea.", card.Value(vcard.FieldNote))
	assert.Equal(t, "Smith", card.Name().FamilyName)
	assert.Contains(t, string(got), "N:Smith;Ann;B\\;C;Dr.;\r\n", "a semicolon inside a name part is escaped")
}

func TestEncode_NameFallbacks(t *testing.T) {
	for name, tc := range map[string]struct{ js, fn string }{
		"organization": {`{"resourceName": "people/c1", "organizations": [{"name": "Acme"}]}`, "Acme"},
		"email":        {`{"resourceName": "people/c2", "emailAddresses": [{"value": "x@example.com"}]}`, "x@example.com"},
		"nothing":      {`{"resourceName": "people/c3"}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := gpeople.Encode(person(t, tc.js), nil, nil)
			require.NoError(t, err)

			card, err := vcard.NewDecoder(bytes.NewReader(got)).Decode()
			require.NoError(t, err)
			assert.Equal(t, tc.fn, card.Value(vcard.FieldFormattedName))
		})
	}
}

func TestPersonID(t *testing.T) {
	assert.Equal(t, "c123", person(t, `{"resourceName": "people/c123"}`).ID())
}
