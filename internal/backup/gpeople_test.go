package backup_test

import (
	"encoding/base64"
	"encoding/json"
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
	"github.com/tonimelisma/pimvault/internal/gpeople"
	"github.com/tonimelisma/pimvault/internal/gpeople/gpeopletest"
	"github.com/tonimelisma/pimvault/internal/state"
)

type gpeopleFixture struct {
	srv   *gpeopletest.Server
	root  string
	run   *backup.GoogleContacts
	state *state.Collections
}

func newGpeopleFixture(t *testing.T) *gpeopleFixture {
	t.Helper()

	srv := gpeopletest.New(t)
	root := filepath.Join(t.TempDir(), "contacts")

	return &gpeopleFixture{
		srv:  srv,
		root: root,
		run: &backup.GoogleContacts{
			Client: gpeople.New(http.DefaultClient, http.DefaultClient, srv.URL),
			Store:  archive.NewStore(root, ".vcf").KeepingAside(root + "-aside"),
			Logger: slog.New(slog.DiscardHandler),
		},
	}
}

func (f *gpeopleFixture) backup(t *testing.T) backup.Result {
	t.Helper()

	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)
	require.NotNil(t, next)
	f.state = next

	return res
}

// read returns a contact file with its lines unfolded.
func (f *gpeopleFixture) read(t *testing.T, id string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(f.root, archive.Contacts, id+".vcf")) //nolint:gosec // G304: test path.
	require.NoError(t, err)

	return strings.ReplaceAll(string(data), "\r\n ", "")
}

// contact builds a contact the way the People API sends it.
func contact(t *testing.T, id, name string, extra ...string) gpeople.Person {
	t.Helper()

	js := `{"resourceName": "people/` + id + `", "names": [{"displayName": "` + name + `"}]`
	for _, e := range extra {
		js += ", " + e
	}

	var p gpeople.Person
	require.NoError(t, json.Unmarshal([]byte(js+"}"), &p))

	return p
}

func photo(url string, isDefault bool) string {
	d := "false"
	if isDefault {
		d = "true"
	}

	return `"photos": [{"url": "` + url + `", "default": ` + d + `}]`
}

// Validates: PV-014, PV-061
func TestGoogleContacts_FirstBackup(t *testing.T) {
	f := newGpeopleFixture(t)
	f.srv.PageSize = 1
	f.srv.SetGroup("contactGroups/friends", "Friends")

	jpeg := []byte("\xff\xd8\xff\xe0 full-size photo")
	annPhoto := f.srv.SetPhoto("ann", jpeg)
	avatar := f.srv.SetPhoto("avatar", []byte("\xff\xd8 avatar"))

	f.srv.Put(contact(t, "c1", "Ann", photo(annPhoto, false),
		`"memberships": [{"contactGroupMembership": {"contactGroupResourceName": "contactGroups/friends"}},
		                 {"contactGroupMembership": {"contactGroupResourceName": "contactGroups/myContacts"}}]`))
	f.srv.Put(contact(t, "c2", "Bob", photo(avatar, true)))

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 2, Downloaded: 2}, res)
	assert.Equal(t, []string{"Contacts/c1.vcf", "Contacts/c2.vcf"}, listFiles(t, f.root))

	ann := f.read(t, "c1")
	assert.Contains(t, ann, "FN:Ann\r\n")
	assert.Contains(t, ann, "CATEGORIES:Friends\r\n")
	assert.Contains(t, ann, base64.StdEncoding.EncodeToString(jpeg), "the full-size photo, not the thumbnail")
	assert.NotContains(t, f.read(t, "c2"), "PHOTO", "default avatars are skipped (D151)")
}

// Validates: PV-102
func TestGoogleContacts_Changes(t *testing.T) {
	f := newGpeopleFixture(t)
	f.srv.Put(contact(t, "c1", "Ann"))
	f.srv.Put(contact(t, "c2", "Bob"))
	f.backup(t)

	f.srv.Put(contact(t, "c1", "Ann Smith"))
	f.srv.Delete("people/c2")
	f.srv.Put(contact(t, "c3", "Cid"))

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 2, Downloaded: 2, Deleted: 1}, res)
	assert.Equal(t, []string{"Contacts/c1.vcf", "Contacts/c3.vcf"}, listFiles(t, f.root))
	assert.Contains(t, f.read(t, "c1"), "FN:Ann Smith\r\n")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 2}, res)
}

func TestGoogleContacts_LabelRenameRewritesMembers(t *testing.T) {
	f := newGpeopleFixture(t)
	f.srv.SetGroup("contactGroups/friends", "Friends")
	f.srv.Put(contact(t, "c1", "Ann",
		`"memberships": [{"contactGroupMembership": {"contactGroupResourceName": "contactGroups/friends"}}]`))
	f.srv.Put(contact(t, "c2", "Bob"))
	f.backup(t)

	// The sync token reports no change for a label rename (F44).
	f.srv.SetGroup("contactGroups/friends", "Pals")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1}, res)
	assert.Contains(t, f.read(t, "c1"), "CATEGORIES:Pals\r\n")
}

// Validates: PV-103
func TestGoogleContacts_ExpiredToken(t *testing.T) {
	f := newGpeopleFixture(t)
	f.srv.Put(contact(t, "c1", "Ann"))
	f.srv.Put(contact(t, "c2", "Bob"))
	f.backup(t)

	f.srv.Delete("people/c2")
	f.srv.ExpireTokens()

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Deleted: 1}, res, "a full listing rewrites nothing that did not change")
	assert.Equal(t, []string{"Contacts/c1.vcf"}, listFiles(t, f.root))
}

func TestGoogleContacts_FailedPhotoIsReadAgain(t *testing.T) {
	f := newGpeopleFixture(t)
	f.srv.Put(contact(t, "c1", "Ann"))
	f.backup(t)

	f.srv.Put(contact(t, "c1", "Ann Smith", photo(f.srv.SetPhoto("ann", []byte("\xff\xd8 ann")), false)))
	f.srv.FailPhotos = http.StatusForbidden

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Failed: 1}, res)
	assert.Contains(t, f.read(t, "c1"), "FN:Ann\r\n", "the old file is kept")

	f.srv.FailPhotos = 0

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1}, res)
	assert.Contains(t, f.read(t, "c1"), "FN:Ann Smith\r\n")
	assert.Contains(t, f.read(t, "c1"), "PHOTO")
}

func TestGoogleContacts_GonePhotoIsSkipped(t *testing.T) {
	f := newGpeopleFixture(t)
	f.srv.Put(contact(t, "c1", "Ann", photo(f.srv.SetPhoto("ann", []byte("\xff\xd8 ann")), false)))
	f.srv.FailPhotos = http.StatusNotFound

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1}, res, "a photo that is gone does not hold the contact back")
	assert.NotContains(t, f.read(t, "c1"), "PHOTO")
}
