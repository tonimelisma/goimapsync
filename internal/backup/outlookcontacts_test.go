package backup_test

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
	"github.com/tonimelisma/pimvault/internal/state"
)

type outlookContactsFixture struct {
	srv   *graphtest.Server
	root  string
	run   *backup.OutlookContacts
	state *state.Collections
}

func newOutlookContactsFixture(t *testing.T) *outlookContactsFixture {
	t.Helper()

	srv := graphtest.New(t)
	root := filepath.Join(t.TempDir(), "contacts")

	return &outlookContactsFixture{
		srv:  srv,
		root: root,
		run: &backup.OutlookContacts{
			Client: graph.New(http.DefaultClient, srv.URL),
			Store:  archive.NewStore(root, ".vcf").KeepingAside(root + "-aside"),
			Logger: slog.New(slog.DiscardHandler),
		},
	}
}

func (f *outlookContactsFixture) backup(t *testing.T) backup.Result {
	t.Helper()

	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)
	require.NotNil(t, next)
	f.state = next

	return res
}

// read returns a contact file with its lines unfolded.
func (f *outlookContactsFixture) read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(path)))
	require.NoError(t, err)

	return strings.ReplaceAll(string(data), "\r\n ", "")
}

func outlookContact(id, name string) graph.Contact {
	return graph.Contact{ID: id, DisplayName: name, GivenName: name, LastModifiedDateTime: "2026-02-01T00:00:00Z"}
}

func TestOutlookContacts_FirstBackup(t *testing.T) {
	f := newOutlookContactsFixture(t)
	f.srv.PageSize = 1
	f.srv.AddContactFolder("cf-friends", "Friends", graph.DefaultContactFolder)
	f.srv.AddContactFolder("cf-close", "Close", "cf-friends")

	ann := outlookContact("c-ann", "Ann")
	ann.Surname = "Smith"
	ann.EmailAddresses = []graph.EmailAddress{{Name: "Ann", Address: "ann@example.com"}}
	ann.MobilePhone = "+358 40 1"
	ann.HomeAddress = graph.PhysicalAddress{Street: "Katu 1", City: "Helsinki", CountryOrRegion: "Finland"}
	ann.Birthday = "1996-11-16T11:59:00Z"
	ann.Categories = []string{"Family"}
	f.srv.PutContact(graph.DefaultContactFolder, ann)

	jpeg := []byte("\xff\xd8\xff\xe0 photo")
	f.srv.SetContactPhoto("c-ann", jpeg)
	f.srv.PutContact(graph.DefaultContactFolder, graph.Contact{ID: "c-acme", CompanyName: "Acme"})
	f.srv.PutContact("cf-friends", outlookContact("c-bob", "Bob"))
	f.srv.PutContact("cf-close", outlookContact("c-cid", "Cid"))

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 4, Downloaded: 4}, res)
	assert.Equal(t, []string{"Contacts/c-acme.vcf", "Contacts/c-ann.vcf", "Friends/Close/c-cid.vcf", "Friends/c-bob.vcf"},
		listFiles(t, f.root))

	got := f.read(t, "Contacts/c-ann.vcf")
	for _, want := range []string{
		"FN:Ann\r\n", "N:Smith;Ann;;;\r\n", "EMAIL:ann@example.com\r\n", "TEL;TYPE=cell:+358 40 1\r\n",
		"ADR;TYPE=home:;;Katu 1;Helsinki;;;Finland\r\n", "BDAY:19961116\r\n", "CATEGORIES:Family\r\n",
		"PHOTO:data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg) + "\r\n", "UID:c-ann\r\n",
	} {
		assert.Contains(t, got, want)
	}

	assert.Contains(t, f.read(t, "Contacts/c-acme.vcf"), "FN:Acme\r\n", "FN falls back to the company")
}

func TestOutlookContacts_Changes(t *testing.T) {
	f := newOutlookContactsFixture(t)
	f.srv.AddContactFolder("cf-friends", "Friends", graph.DefaultContactFolder)
	f.srv.PutContact(graph.DefaultContactFolder, outlookContact("c-ann", "Ann"))
	f.srv.PutContact(graph.DefaultContactFolder, outlookContact("c-bob", "Bob"))
	f.srv.PutContact("cf-friends", outlookContact("c-cid", "Cid"))
	f.backup(t)

	f.srv.PutContact(graph.DefaultContactFolder, outlookContact("c-ann", "Ann Smith"))
	f.srv.DeleteContact("c-bob")
	f.srv.PutContact(graph.DefaultContactFolder, outlookContact("c-cid", "Cid"))

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 2, Downloaded: 2, Deleted: 2}, res)
	assert.Equal(t, []string{"Contacts/c-ann.vcf", "Contacts/c-cid.vcf"}, listFiles(t, f.root))
	assert.Contains(t, f.read(t, "Contacts/c-ann.vcf"), "FN:Ann Smith\r\n")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 2}, res)
}

func TestOutlookContacts_ExpiredDeltaAndRename(t *testing.T) {
	f := newOutlookContactsFixture(t)
	f.srv.AddContactFolder("cf-friends", "Friends", graph.DefaultContactFolder)
	f.srv.PutContact(graph.DefaultContactFolder, outlookContact("c-ann", "Ann"))
	f.srv.PutContact("cf-friends", outlookContact("c-bob", "Bob"))
	f.backup(t)

	f.srv.ExpireDelta()

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2}, res, "a full listing rewrites nothing that did not change")

	f.srv.AddContactFolder("cf-friends", "Pals", graph.DefaultContactFolder)

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1, Deleted: 1}, res)
	assert.Equal(t, []string{"Contacts/c-ann.vcf", "Pals/c-bob.vcf"}, listFiles(t, f.root))
}
