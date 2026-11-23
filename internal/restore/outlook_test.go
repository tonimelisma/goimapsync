package restore_test

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
	"github.com/tonimelisma/pimvault/internal/restore"
	"github.com/tonimelisma/pimvault/internal/vcardfile"
)

func TestOutlookContacts_Restore(t *testing.T) {
	srv := graphtest.New(t)
	srv.PutContact(graph.DefaultContactFolder, graph.Contact{
		ID: "existing", DisplayName: "Bob", EmailAddresses: []graph.EmailAddress{{Address: "BOB@example.com"}},
	})

	source := archive.NewStore(filepath.Join(t.TempDir(), "contacts"), ".vcf")
	put := func(folder, id string, c *vcardfile.Card) { require.NoError(t, source.Write(folder, id, c.Encode())) }

	ann := vcardfile.New("people/c1")
	ann.SetText("FN", "Dr. Ann Smith")
	ann.Structured("N", []string{"Smith", "Ann", "B;C", "Dr.", ""})
	for _, e := range []string{"a1@example.com", "a2@example.com", "a3@example.com", "a4@example.com"} {
		ann.Text("EMAIL", e)
	}
	ann.Text("TEL", "+358 40 1", vcardfile.Type("cell"))
	ann.Text("TEL", "+358 9 2", vcardfile.Type("work"), vcardfile.Type("fax"))
	ann.Structured("ADR", []string{"", "", "Katu 1; C 12", "Helsinki", "", "00100", "Finland"}, vcardfile.Type("home"))
	ann.Structured("ORG", []string{"Kide Oy", "R&D"})
	ann.Text("BDAY", "19961116")
	ann.Text("RELATED", "Bob", vcardfile.TextValue(), vcardfile.Type("spouse"))
	ann.Text("RELATED", "Cid", vcardfile.TextValue(), vcardfile.Type("friend"))
	ann.Text("NOTE", "Line one\nline two, with comma")
	ann.Categories([]string{"Friends"})
	ann.Photo([]byte("\xff\xd8 photo"), "image/jpeg")
	put(archive.Contacts, "c1", ann)

	bob := vcardfile.New("people/c2")
	bob.SetText("FN", "Bob")
	bob.Text("EMAIL", "bob@example.com")
	put(archive.Contacts, "c2", bob)

	cid := vcardfile.New("people/c3")
	cid.SetText("FN", "Cid")
	cid.Text("BDAY", "--0517")
	put(archive.FolderPath([]string{"Friends", "Close"}, []string{archive.Contacts}), "c3", cid)

	// A Google archive into Outlook: a migrate, so Bob's address matches the
	// account's Bob (D41).
	run := &restore.OutlookContacts{
		Client: graph.New(http.DefaultClient, srv.URL), Source: source, Migrate: true, Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 2, Present: 1}, res)

	written := srv.WrittenContacts()
	require.Len(t, written, 2)

	got := written["c-new-1"]
	assert.Equal(t, map[string]any{
		"displayName": "Dr. Ann Smith", "surname": "Smith", "givenName": "Ann", "middleName": "B;C", "title": "Dr.",
		"emailAddresses": []any{
			map[string]any{"address": "a1@example.com"},
			map[string]any{"address": "a2@example.com"},
			map[string]any{"address": "a3@example.com"},
		},
		"mobilePhone": "+358 40 1",
		"homeAddress": map[string]any{"street": "Katu 1; C 12", "city": "Helsinki", "state": "", "postalCode": "00100", "countryOrRegion": "Finland"},
		"companyName": "Kide Oy", "department": "R&D",
		"birthday":   "1996-11-16T11:59:00Z",
		"spouseName": "Bob",
		"categories": []any{"Friends"},
		"personalNotes": "Line one\nline two, with comma\n\n" +
			"Email: a4@example.com\nPhone (work, fax): +358 9 2\nRelated (friend): Cid",
		"folder": graph.DefaultContactFolder,
		"singleValueExtendedProperties": []any{map[string]any{
			"id": "String {7a5d4f0e-2c1b-4a6e-9f3d-5b8c1e2a7d90} Name pimvaultSource", "value": "people/c1",
		}},
	}, got)
	assert.Equal(t, []byte("\xff\xd8 photo"), srv.ContactPhoto("c-new-1"))

	cidFields := written["c-new-2"]
	assert.Equal(t, "Birthday: --0517", cidFields["personalNotes"], "Outlook needs a year")

	folders := srv.ContactFolderNames()
	cidFolder, ok := cidFields["folder"].(string)
	require.True(t, ok)
	assert.Equal(t, "Close", folders[cidFolder])
	assert.ElementsMatch(t, []string{"Friends", "Close"}, []string{folders["cf-new-1"], folders["cf-new-2"]})

	// Running again writes nothing: Cid has no email address, but carries
	// the archive ID it was written from (D160).
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

func TestOutlookContacts_IdenticalContactIsAlreadyThere(t *testing.T) {
	// Outlook.com deletes a new contact identical to one it has (F67): one
	// the account has without an archive ID counts as already there (D176).
	srv := graphtest.New(t)
	srv.PutContact(graph.DefaultContactFolder, graph.Contact{ID: "old", DisplayName: "Dee", MobilePhone: "+358 40 9"})

	source := archive.NewStore(filepath.Join(t.TempDir(), "contacts"), ".vcf")

	dee := vcardfile.New("people/c9")
	dee.SetText("FN", "Dee")
	dee.Text("TEL", "+358 40 9", vcardfile.Type("cell"))
	require.NoError(t, source.Write(archive.Contacts, "c9", dee.Encode()))

	twin := vcardfile.New("people/c10")
	twin.SetText("FN", "Eve")
	require.NoError(t, source.Write(archive.Contacts, "c10", twin.Encode()))

	twin2 := vcardfile.New("people/c11")
	twin2.SetText("FN", "Eve")
	require.NoError(t, source.Write(archive.Contacts, "c11", twin2.Encode()))

	// Written all the same: Dee in another folder, and Dee with a photo,
	// which the comparison leaves out.
	require.NoError(t, source.Write("Family", "c12", dee.Encode()))

	withPhoto := vcardfile.New("people/c13")
	withPhoto.SetText("FN", "Dee")
	withPhoto.Text("TEL", "+358 40 9", vcardfile.Type("cell"))
	withPhoto.Photo([]byte("\xff\xd8 photo"), "image/jpeg")
	require.NoError(t, source.Write(archive.Contacts, "c13", withPhoto.Encode()))

	run := &restore.OutlookContacts{
		Client: graph.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	var planned []string

	run.DryRun = true
	run.Planned = func(item, action string) { planned = append(planned, action+" "+item) }

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 3, Present: 2}, res, "a dry run counts as the real run does")

	run.DryRun = false

	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 3, Present: 2}, res, "Dee is there; the second Eve is the first's twin")
	assert.Len(t, srv.WrittenContacts(), 3)
}

func TestOutlookContacts_ContactOutlookMovedOutIsAlreadyThere(t *testing.T) {
	srv := graphtest.New(t)
	source := archive.NewStore(filepath.Join(t.TempDir(), "contacts"), ".vcf")

	cid := vcardfile.New("people/c3")
	cid.SetText("FN", "Cid")
	cid.Text("TEL", "+358 40 3", vcardfile.Type("cell"))
	require.NoError(t, source.Write(archive.Contacts, "c3", cid.Encode()))

	run := &restore.OutlookContacts{
		Client: graph.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t, restore.Result{Written: 1}, res)

	// Outlook.com takes it for a duplicate and moves it out of the folders,
	// keeping its archive ID (F68): writing it again would be undone too.
	srv.HideContact("c-new-1")

	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 1}, res)
}

func TestOutlookContacts_FailedPhotoIsRetriedWhole(t *testing.T) {
	srv := graphtest.New(t)
	source := archive.NewStore(filepath.Join(t.TempDir(), "contacts"), ".vcf")

	ann := vcardfile.New("people/c1")
	ann.SetText("FN", "Ann")
	ann.Photo([]byte("\xff\xd8 photo"), "image/jpeg")
	require.NoError(t, source.Write(archive.Contacts, "c1", ann.Encode()))

	run := &restore.OutlookContacts{Client: graph.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	srv.FailWrites["PUT /contacts/c-new-1/photo/$value"] = http.StatusInternalServerError
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Failed: 1}, res)
	assert.Empty(t, srv.WrittenContacts(), "the contact without its photo is deleted again")

	clear(srv.FailWrites)
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)
	assert.Equal(t, []byte("\xff\xd8 photo"), srv.ContactPhoto("c-new-2"))
}
