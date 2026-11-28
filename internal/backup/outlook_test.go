package backup_test

import (
	"fmt"
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

type outlookFixture struct {
	srv   *graphtest.Server
	root  string
	run   *backup.OutlookMail
	state *state.OutlookMail
}

func newOutlookFixture(t *testing.T) *outlookFixture {
	t.Helper()

	srv := graphtest.New(t)
	root := filepath.Join(t.TempDir(), "mail")

	return &outlookFixture{
		srv:  srv,
		root: root,
		run: &backup.OutlookMail{
			Client: graph.New(http.DefaultClient, srv.URL),
			Store:  archive.NewStore(root, ".eml").KeepingAside(root + "-aside"),
			Logger: slog.New(slog.DiscardHandler),
		},
	}
}

func (f *outlookFixture) backup(t *testing.T) backup.Result {
	t.Helper()

	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)
	require.NotNil(t, next)
	f.state = next

	return res
}

func (f *outlookFixture) files(t *testing.T) []string {
	t.Helper()

	return listFiles(t, f.root)
}

// Validates: PV-004, PV-053, PV-059
func TestOutlookMail_FirstBackup(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.AddFolder("u-work", "Work", "")
	f.srv.AddFolder("u-proj", "Projects", "u-work")
	f.srv.Add("m1", "f-inbox", "one")
	f.srv.Add("m2", "f-sent", "two")
	f.srv.Add("m3", "u-proj", "three")
	f.srv.Add("m4", "f-deleted", "deleted")
	f.srv.Add("m5", "f-junk", "junk")
	f.srv.Add("m6", "f-archive", "six")
	f.srv.Add("m7", "f-drafts", "seven")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 5, Downloaded: 5}, res)
	assert.Equal(t, []string{
		"Archive/m6.eml", "Drafts/m7.eml", "Inbox/m1.eml", "Sent/m2.eml", "Work/Projects/m3.eml",
	}, f.files(t))

	data, err := os.ReadFile(filepath.Join(f.root, "Inbox", "m1.eml"))
	require.NoError(t, err)
	assert.Equal(t, "one", string(data))
}

// Validates: PV-101, PV-102
func TestOutlookMail_IncrementalMovesAndDeletes(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("m1", "f-inbox", "one")
	f.srv.Add("m2", "f-inbox", "two")
	f.srv.Add("m3", "f-inbox", "three")
	f.srv.Add("m4", "f-inbox", "four")
	f.backup(t)

	f.srv.Move("m1", "f-archive") // moved: file moves, no download
	f.srv.Move("m2", "f-deleted") // deleted in Outlook: file deleted
	f.srv.Delete("m3")            // permanently deleted
	f.srv.Add("m5", "f-sent", "five")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 3, Downloaded: 1, Moved: 1, Deleted: 2}, res)
	assert.Equal(t, []string{"Archive/m1.eml", "Inbox/m4.eml", "Sent/m5.eml"}, f.files(t))

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 3}, res)
}

// Validates: PV-101
func TestOutlookMail_FolderRenameMovesFiles(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.AddFolder("u-work", "Work", "")
	f.srv.Add("m1", "u-work", "one")
	f.backup(t)

	f.srv.RenameFolder("u-work", "Job")

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Moved: 1}, res)
	assert.Equal(t, []string{"Job/m1.eml"}, f.files(t))
}

func TestOutlookMail_DeletedFolderRemovesFiles(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.AddFolder("u-work", "Work", "")
	f.srv.Add("m1", "u-work", "one")
	f.srv.Add("m2", "f-inbox", "two")
	f.backup(t)

	// Deleting a folder in Outlook moves it into Deleted Items.
	f.srv.AddFolder("u-work", "Work", "f-deleted")

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Deleted: 1}, res)
	assert.Equal(t, []string{"Inbox/m2.eml"}, f.files(t))
}

// Validates: PV-103
func TestOutlookMail_ExpiredDeltaRelistsFolders(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("m1", "f-inbox", "one")
	f.srv.Add("m2", "f-inbox", "two")
	f.backup(t)

	f.srv.Delete("m2")
	f.srv.Add("m3", "f-inbox", "three")
	f.srv.ExpireDelta()

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1, Deleted: 1}, res)
	assert.Equal(t, []string{"Inbox/m1.eml", "Inbox/m3.eml"}, f.files(t))

	f.srv.Add("m4", "f-inbox", "four")
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 3, Downloaded: 1}, res)
}

func TestOutlookMail_FailedDownloadIsRetried(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("m1", "f-inbox", "one")
	f.srv.FailRaw["m1"] = http.StatusBadRequest

	res := f.backup(t)
	assert.Equal(t, backup.Result{Failed: 1}, res)
	assert.Equal(t, map[string]string{"m1": "Inbox"}, f.state.Retry)

	delete(f.srv.FailRaw, "m1")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1}, res)
	assert.Empty(t, f.state.Retry)
	assert.Equal(t, []string{"Inbox/m1.eml"}, f.files(t))
}

func TestOutlookMail_ArchiveChangedOutsidePimvault(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("m1", "f-inbox", "one")
	f.srv.Add("m2", "f-inbox", "two")
	f.backup(t)

	require.NoError(t, os.Remove(filepath.Join(f.root, "Inbox", "m1.eml")))
	require.NoError(t, archive.NewStore(f.root, ".eml").Write("Inbox", "stray", []byte("x")))
	require.NoError(t, os.Remove(filepath.Join(f.root, "Inbox", "m2.eml")))

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 2, Deleted: 1}, res)
	assert.Equal(t, []string{"Inbox/m1.eml", "Inbox/m2.eml"}, f.files(t))
}

func TestOutlookMail_RelistingKeepsCopyInFollowedFolder(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.AddFolder("u-work", "Work", "")
	f.srv.Add("m1", "f-inbox", "one")
	f.srv.Add("m2", "u-work", "two")
	f.backup(t)

	// A stray second copy of m1 in Work, with a file removed to keep the
	// count the same, so the archive looks unchanged.
	store := archive.NewStore(f.root, ".eml").KeepingAside(f.root + "-aside")
	require.NoError(t, store.Write("Work", "m1", []byte("one")))
	require.NoError(t, os.Remove(filepath.Join(f.root, "Work", "m2.eml")))

	f.srv.RenameFolder("u-work", "Job") // Work is relisted, Inbox is not

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1, Deleted: 1}, res)
	assert.Equal(t, []string{"Inbox/m1.eml", "Job/m2.eml"}, f.files(t))
}

func TestOutlookMail_EditedDraftIsDownloadedAgain(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("d1", "f-drafts", "first version")
	f.backup(t)

	f.srv.Edit("d1", "second version")

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1}, res)

	data, err := os.ReadFile(filepath.Join(f.root, "Drafts", "d1.eml"))
	require.NoError(t, err)
	assert.Equal(t, "second version", string(data))
}

func TestOutlookMail_FailingFolderDoesNotStopOthers(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.AddFolder("u-work", "Work", "")
	f.srv.Add("m1", "u-work", "one")
	f.backup(t)

	f.srv.Add("m2", "f-inbox", "two")
	f.srv.Add("m3", "u-work", "three")
	f.srv.FailFolder("u-work", http.StatusBadRequest)

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1, Failed: 1}, res)
	assert.Equal(t, []string{"Inbox/m2.eml", "Work/m1.eml"}, f.files(t), "Work's files are kept")

	f.srv.FailFolder("u-work", 0)

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 3, Downloaded: 1}, res, "Work continues from its old position")
}

func TestOutlookMail_ListingThatExpiresMidwayRestarts(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.PageSize = 1
	f.srv.Add("m1", "f-inbox", "one")
	f.srv.Add("m2", "f-inbox", "two")
	f.srv.ExpireListings()

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 2}, res)
}

// Validates: PV-107
func TestOutlookMail_MassDeletionIsWithheldAndRetried(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.AddFolder("u-work", "Work", "")

	for i := range 50 {
		f.srv.Add(fmt.Sprintf("m%02d", i), "u-work", "x")
	}

	f.backup(t)

	// Deleting a folder sends its messages to Deleted Items, which is not
	// backed up.
	for i := range 11 {
		f.srv.Move(fmt.Sprintf("m%02d", i), "f-deleted")
	}

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 50, Withheld: 11}, res)

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 50, Withheld: 11}, res, "the delta does not repeat them; the state does")

	f.run.Options.AllowDeletions = true
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 39, Deleted: 11}, res)
	assert.Len(t, listFiles(t, f.root+"-aside"), 11)
}

// Validates: PV-062
func TestOutlookMail_LongFolderNameIsBackedUp(t *testing.T) {
	f := newOutlookFixture(t)
	long := strings.Repeat("ö", 256) // Graph takes 256 characters (F72): 512 bytes
	f.srv.AddFolder("u-long", long, "")
	f.srv.Add("m1", "u-long", "one")

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1}, res)

	have, err := f.run.Store.Scan()
	require.NoError(t, err)
	assert.Equal(t, []string{long}, archive.FolderSegments(have["m1"][0]))

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 1}, res, "found again on the next run")
}

// Validates: PV-108
func TestOutlookMail_DryRunSizeIsUnknown(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("m1", "f-inbox", "one")
	f.backup(t)

	f.srv.Add("m2", "f-inbox", "two")
	f.run.Options.DryRun = true

	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)
	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1}, res, "Outlook's size is not the download's (F79)")
	assert.Nil(t, next)
	assert.Equal(t, []string{"Inbox/m1.eml"}, f.files(t))

	f.run.Options.DryRun = false
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1}, res, "the delta read by the dry run is read again")
}
