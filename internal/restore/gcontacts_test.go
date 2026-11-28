package restore_test

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gpeople"
	"github.com/tonimelisma/pimvault/internal/gpeople/gpeopletest"
	"github.com/tonimelisma/pimvault/internal/restore"
	"github.com/tonimelisma/pimvault/internal/vcardfile"
)

// Validates: PV-158, PV-181
func TestGoogleContacts_Restore(t *testing.T) {
	srv := gpeopletest.New(t)
	srv.SetGroup("contactGroups/friends1", "Friends")

	var bob gpeople.Person
	bob.ResourceName = "people/existing"
	bob.EmailAddresses = append(bob.EmailAddresses, struct {
		Value    string                `json:"value"`
		Type     string                `json:"type"`
		Metadata gpeople.FieldMetadata `json:"metadata"`
	}{Value: "BOB@example.com"})
	srv.Put(bob)

	source := archive.NewStore(filepath.Join(t.TempDir(), "contacts"), ".vcf")
	put := func(folder, id string, c *vcardfile.Card) { require.NoError(t, source.Write(folder, id, c.Encode())) }

	ann := vcardfile.New("AAMk-ann")
	ann.SetText("FN", "Dr. Ann Smith")
	ann.Structured("N", []string{"Smith", "Ann", "B", "Dr.", ""})
	ann.Text("EMAIL", "ann@example.com", vcardfile.Type("work"))
	ann.Text("TEL", "+358 40 1", vcardfile.Type("cell"))
	ann.Text("TEL", "+358 9 2", vcardfile.Type("work"), vcardfile.Type("fax"))
	ann.Structured("ADR", []string{"", "", "Katu 1", "Helsinki", "", "00100", "Finland"}, vcardfile.Type("home"))
	ann.Structured("ORG", []string{"Kide Oy", "R&D"})
	ann.Text("TITLE", "Engineer")
	ann.Text("BDAY", "19961116")
	ann.Text("ANNIVERSARY", "--0517")
	ann.Text("RELATED", "Bob", vcardfile.TextValue(), vcardfile.Type("spouse"))
	ann.URI("IMPP", "sip:ann@sip.example.com")
	ann.URI("IMPP", "ann@im.example.com")
	ann.Text("NOTE", "Met at the conference")
	ann.Categories([]string{"Friends", "Book club"})
	ann.Photo([]byte("\xff\xd8 photo"), "image/jpeg")
	put(archive.FolderPath([]string{"Work", "Close"}, []string{archive.Contacts}), "ann", ann)

	copyOfBob := vcardfile.New("people/c2")
	copyOfBob.SetText("FN", "Bob")
	copyOfBob.Text("EMAIL", "bob@example.com")
	put(archive.Contacts, "c2", copyOfBob)

	cid := vcardfile.New("people/c3")
	cid.SetText("FN", "Cid")
	put(archive.Contacts, "c3", cid)

	run := &restore.GoogleContacts{
		Client: gpeople.New(http.DefaultClient, http.DefaultClient, srv.URL), Source: source, Migrate: true,
		Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 2, Present: 1}, res)

	groups := srv.Groups()
	assert.Equal(t, map[string]string{
		"contactGroups/friends1": "Friends", "contactGroups/g-new-2": "Book club", "contactGroups/g-new-3": "Work/Close",
	}, groups)

	written := srv.Written()
	require.Len(t, written, 2)

	assert.Equal(t, map[string]any{
		"names":          []any{map[string]any{"familyName": "Smith", "givenName": "Ann", "middleName": "B", "honorificPrefix": "Dr."}},
		"emailAddresses": []any{map[string]any{"value": "ann@example.com", "type": "work"}},
		"phoneNumbers": []any{
			map[string]any{"value": "+358 40 1", "type": "mobile"},
			map[string]any{"value": "+358 9 2", "type": "workFax"},
		},
		"addresses": []any{map[string]any{
			"streetAddress": "Katu 1", "city": "Helsinki", "postalCode": "00100", "country": "Finland", "type": "home",
		}},
		"organizations": []any{map[string]any{"name": "Kide Oy", "department": "R&D", "title": "Engineer"}},
		"birthdays":     []any{map[string]any{"date": map[string]any{"year": 1996.0, "month": 11.0, "day": 16.0}}},
		"events":        []any{map[string]any{"date": map[string]any{"year": 0.0, "month": 5.0, "day": 17.0}, "type": "anniversary"}},
		"biographies":   []any{map[string]any{"value": "Met at the conference"}},
		"relations":     []any{map[string]any{"person": "Bob", "type": "spouse"}},
		"sipAddresses":  []any{map[string]any{"value": "ann@sip.example.com"}},
		"imClients":     []any{map[string]any{"username": "ann@im.example.com"}},
		"memberships": []any{
			map[string]any{"contactGroupMembership": map[string]any{"contactGroupResourceName": "contactGroups/g-new-2"}},
			map[string]any{"contactGroupMembership": map[string]any{"contactGroupResourceName": "contactGroups/friends1"}},
			map[string]any{"contactGroupMembership": map[string]any{"contactGroupResourceName": "contactGroups/g-new-3"}},
		},
		"clientData": []any{map[string]any{"key": "pimvaultSource", "value": "AAMk-ann"}},
	}, written["people/c-new-2"])
	assert.Equal(t, []byte("\xff\xd8 photo"), srv.Uploaded("people/c-new-2"))
	assert.Equal(t, []any{map[string]any{"unstructuredName": "Cid"}}, written["people/c-new-1"]["names"])

	// Running again writes nothing: Cid has no email address but carries
	// the archive ID it was written from (D160).
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

func TestGoogleContacts_FailedPhotoIsRetriedWhole(t *testing.T) {
	srv := gpeopletest.New(t)
	source := archive.NewStore(filepath.Join(t.TempDir(), "contacts"), ".vcf")

	ann := vcardfile.New("c1")
	ann.SetText("FN", "Ann")
	ann.Photo([]byte("\xff\xd8 photo"), "image/jpeg")
	require.NoError(t, source.Write(archive.Contacts, "c1", ann.Encode()))

	run := &restore.GoogleContacts{
		Client: gpeople.New(http.DefaultClient, http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	srv.FailPhotoUploads = http.StatusInternalServerError
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Failed: 1}, res)
	assert.Empty(t, srv.Written())

	srv.FailPhotoUploads = 0
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)
}
