package backup_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gmail"
	"github.com/tonimelisma/pimvault/internal/gmail/gmailtest"
	"github.com/tonimelisma/pimvault/internal/state"
)

type fixture struct {
	srv   *gmailtest.Server
	root  string
	run   *backup.GmailMail
	state *state.GmailMail
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	srv := gmailtest.New(t)
	root := filepath.Join(t.TempDir(), "mail")

	return &fixture{
		srv:  srv,
		root: root,
		run: &backup.GmailMail{
			Client: gmail.New(http.DefaultClient, srv.URL),
			Store:  archive.NewStore(root, ".eml").KeepingAside(root + "-aside"),
			Logger: slog.New(slog.DiscardHandler),
		},
	}
}

// backup runs once and keeps the returned state, like the CLI does.
func (f *fixture) backup(t *testing.T) backup.Result {
	t.Helper()

	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)

	if next != nil {
		f.state = next
	}

	return res
}

// files lists the archive as folder/file paths.
func (f *fixture) files(t *testing.T) []string {
	t.Helper()

	return listFiles(t, f.root)
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()

	var paths []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			require.NoError(t, relErr)
			paths = append(paths, filepath.ToSlash(rel))
		}

		return nil
	})
	require.NoError(t, err)

	return paths
}

func TestGmailMail_FirstBackupListsEverything(t *testing.T) {
	f := newFixture(t)
	f.srv.AddLabel("L1", "Work/Projects")
	f.srv.AddLabel("L2", "[Gmail]/All Mail")
	f.srv.Add("m1", "one", "INBOX", "L1")
	f.srv.Add("m2", "two", "SENT")
	f.srv.Add("m3", "three", "L1")
	f.srv.Add("m4", "four", "L2")
	f.srv.Add("m5", "five")
	f.srv.Add("m6", "six", "DRAFT")
	f.srv.Add("m7", "spam", "SPAM")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 6, Downloaded: 6}, res)
	assert.Equal(t, []string{
		"Archive/m5.eml",
		"Drafts/m6.eml",
		"Inbox/m1.eml",
		"Sent/m2.eml",
		"Work/Projects/m3.eml",
		"[Gmail]/All Mail/m4.eml",
	}, f.files(t))

	data, err := os.ReadFile(filepath.Join(f.root, "Inbox", "m1.eml"))
	require.NoError(t, err)
	assert.Equal(t, "one", string(data))
	assert.Equal(t, map[string]string{"L1": "Work/Projects", "L2": "[Gmail]/All Mail"}, f.state.Labels)
	assert.NotEmpty(t, f.state.HistoryID)
}

func TestGmailMail_IncrementalFollowsHistory(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.srv.Add("m2", "two", "INBOX")
	f.srv.Add("m3", "three", "INBOX")
	f.srv.Add("m4", "four", "INBOX")
	f.backup(t)

	f.srv.SetLabels("m1")          // archived
	f.srv.Delete("m2")             // deleted for good
	f.srv.SetLabels("m3", "TRASH") // trashed
	f.srv.Add("m5", "five", "SENT")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 3, Downloaded: 1, Moved: 1, Deleted: 2}, res)
	assert.Equal(t, []string{"Archive/m1.eml", "Inbox/m4.eml", "Sent/m5.eml"}, f.files(t))
	assert.Equal(t, 4, f.srv.Requests["/messages"], "all, Inbox, Drafts and Sent listed once, on the first run only")
	assert.Equal(t, 1, f.srv.Requests["/history"])

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 3}, res, "nothing changed")
}

func TestGmailMail_LabelRenameListsEverything(t *testing.T) {
	f := newFixture(t)
	f.srv.AddLabel("L1", "Old")
	f.srv.Add("m1", "one", "L1")
	f.backup(t)

	f.srv.AddLabel("L1", "New/Name")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 1, Moved: 1}, res)
	assert.Equal(t, []string{"New/Name/m1.eml"}, f.files(t))
	assert.Zero(t, f.srv.Requests["/history"])
}

func TestGmailMail_ExpiredHistoryListsEverything(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.srv.Add("m2", "two", "INBOX")
	f.backup(t)

	f.srv.Delete("m2")
	f.srv.Add("m3", "three")
	f.srv.ExpireHistory()

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1, Deleted: 1}, res)
	assert.Equal(t, []string{"Archive/m3.eml", "Inbox/m1.eml"}, f.files(t))

	// The next run continues from the new history ID.
	f.srv.Add("m4", "four", "INBOX")
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 3, Downloaded: 1}, res)
}

func TestGmailMail_FailedDownloadIsRetriedNextRun(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.backup(t)

	f.srv.Add("m2", "two", "INBOX")
	f.srv.Add("m3", "three", "INBOX")
	f.srv.FailRaw["m2"] = http.StatusBadRequest

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1, Failed: 1}, res)
	assert.Equal(t, []string{"m2"}, f.state.Retry)

	// Still failing: retried, reported again, and the rest keeps going.
	f.srv.Add("m4", "four", "INBOX")
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 3, Downloaded: 1, Failed: 1}, res)
	assert.Equal(t, 2, f.srv.Requests["/history"])

	delete(f.srv.FailRaw, "m2")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 4, Downloaded: 1}, res)
	assert.Empty(t, f.state.Retry)
	assert.Equal(t, []string{"Inbox/m1.eml", "Inbox/m2.eml", "Inbox/m3.eml", "Inbox/m4.eml"}, f.files(t))
}

func TestGmailMail_ArchiveChangesOutsidePimvaultListEverything(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.srv.Add("m2", "two", "INBOX")
	f.backup(t)

	require.NoError(t, os.Remove(filepath.Join(f.root, "Inbox", "m1.eml")))

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1}, res, "deleted file is downloaded again")
	assert.Zero(t, f.srv.Requests["/history"])

	// A new, empty archive directory with the old state.
	f.root = filepath.Join(t.TempDir(), "mail")
	f.run.Store = archive.NewStore(f.root, ".eml").KeepingAside(f.root + "-aside")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 2}, res)
	assert.Equal(t, []string{"Inbox/m1.eml", "Inbox/m2.eml"}, f.files(t))
}

func TestGmailMail_CanceledRunReturnsCountsSoFar(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, next, err := f.run.Run(ctx, nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, next)
}

func TestGmailMail_FullListingMirrorsArchive(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")

	// Left over from an earlier run or another tool: a deleted message, a
	// copy in a second folder, a temp file and a file that is not an item.
	store := archive.NewStore(f.root, ".eml").KeepingAside(f.root + "-aside")
	require.NoError(t, store.Write("Inbox", "old", []byte("x")))
	require.NoError(t, store.Write("Sent", "m1", []byte("one")))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "Inbox", ".pimvault-1.tmp"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "Inbox", "README"), nil, 0o600))

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 1, Moved: 1, Deleted: 1}, res)
	assert.Equal(t, []string{"Inbox/README", "Inbox/m1.eml"}, f.files(t))
}

func TestGmailMail_ListingFailureFailsTheAccount(t *testing.T) {
	f := newFixture(t)
	f.run.Client = gmail.New(http.DefaultClient, f.srv.URL+"/nowhere")

	_, next, err := f.run.Run(t.Context(), nil)
	require.Error(t, err)
	assert.Nil(t, next)
}

func TestGmailMail_ReportsDownloadProgress(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.srv.Add("m2", "two", "INBOX")

	var reports [][2]int
	f.run.Progress = func(done, total int) { reports = append(reports, [2]int{done, total}) }

	f.backup(t)
	assert.Equal(t, [][2]int{{0, 2}, {1, 2}, {2, 2}}, reports)

	reports = nil
	f.backup(t)
	assert.Equal(t, [][2]int{{0, 0}}, reports, "nothing to download")
}

func TestGmailMail_DeletedMessagesAreKeptAside(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.srv.Add("m2", "two", "INBOX")
	f.backup(t)

	f.srv.Delete("m2")

	res := f.backup(t)
	assert.Equal(t, 1, res.Deleted)
	assert.Equal(t, []string{"Inbox/m1.eml"}, f.files(t))
	assert.Equal(t, []string{"Inbox/m2.eml"}, listFiles(t, f.root+"-aside"))
}

func TestGmailMail_MassDeletionIsWithheld(t *testing.T) {
	f := newFixture(t)
	for i := range 60 {
		f.srv.Add(fmt.Sprintf("m%02d", i), "x", "INBOX")
	}

	f.srv.Add("w1", "x", "L1")
	f.srv.AddLabel("L1", "Work")
	f.backup(t)

	// 13 of 60 is more than a fifth; the one message of Work is not a
	// folder of 50.
	for i := range 13 {
		f.srv.Delete(fmt.Sprintf("m%02d", i))
	}

	f.srv.Delete("w1")
	f.srv.Add("new", "x", "INBOX")

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 61, Downloaded: 1, Deleted: 1, Withheld: 13}, res)
	assert.Len(t, f.files(t), 61)

	history := f.srv.Requests["/history"]
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 61, Withheld: 13}, res, "still withheld on the next run")
	assert.Equal(t, history+1, f.srv.Requests["/history"], "from the history, not a full listing")

	f.run.Options.AllowDeletions = true
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 48, Deleted: 13}, res)
	assert.Len(t, listFiles(t, f.root+"-aside"), 14)
}

func TestGmailMail_AFifthOfAFolderIsNotWithheld(t *testing.T) {
	f := newFixture(t)
	for i := range 60 {
		f.srv.Add(fmt.Sprintf("m%02d", i), "x", "INBOX")
	}

	f.backup(t)

	for i := range 12 {
		f.srv.Delete(fmt.Sprintf("m%02d", i))
	}

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 48, Deleted: 12}, res)
}

func TestGmailMail_MovedMessagesAreNotDeletions(t *testing.T) {
	f := newFixture(t)
	for i := range 60 {
		f.srv.Add(fmt.Sprintf("m%02d", i), "x", "INBOX")
	}

	f.backup(t)

	for i := range 30 {
		f.srv.SetLabels(fmt.Sprintf("m%02d", i)) // archived
	}

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 60, Moved: 30}, res)
}

func TestGmailMail_DryRunCountsAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.srv.Add("m2", "two", "INBOX")
	f.srv.Add("m3", "three", "INBOX")
	f.backup(t)

	f.srv.Add("m4", "four!", "SENT")
	f.srv.Add("m5", "five", "INBOX")
	f.srv.SetLabels("m1") // archived
	f.srv.Delete("m2")

	f.run.Options.DryRun = true
	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)
	assert.Equal(t, backup.Result{Files: 3, Downloaded: 2, Moved: 1, Deleted: 1, Bytes: 9, SizeKnown: true}, res)
	assert.Nil(t, next, "the state stays")
	assert.Equal(t, []string{"Inbox/m1.eml", "Inbox/m2.eml", "Inbox/m3.eml"}, f.files(t), "nothing written")

	f.run.Options.DryRun = false
	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 4, Downloaded: 2, Moved: 1, Deleted: 1}, res, "the backup does what the dry run said")
}

func TestGmailMail_DryRunSizeIsUnknownWhenALookupFails(t *testing.T) {
	f := newFixture(t)
	f.srv.Add("m1", "one", "INBOX")
	f.srv.Add("m2", "two", "INBOX")
	f.srv.FailSize["m2"] = http.StatusInternalServerError
	f.run.Options.DryRun = true

	res, _, err := f.run.Run(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, backup.Result{Downloaded: 2, Bytes: 3}, res, "never a short total shown as known")
}
