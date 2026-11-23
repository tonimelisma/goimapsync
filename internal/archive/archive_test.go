package archive_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/archive"
)

func TestAccountDir(t *testing.T) {
	id, err := account.Parse("gmail:User@Gmail.Example.com")
	require.NoError(t, err)
	assert.Equal(t, "/a/gmail_user@gmail.example.com", archive.AccountDir("/a", id))
}

func TestAccounts(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()

	require.NoError(t, os.Mkdir(filepath.Join(root, "gmail_user@gmail.example.com"), 0o700))
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(root, "outlook_user@outlook.example.com")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "notes"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".deleted", "gmail_user@gmail.example.com"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gmail_file@gmail.example.com"), nil, 0o600))

	ids, err := archive.Accounts(root)
	require.NoError(t, err)

	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, id.String())
	}

	assert.Equal(t, []string{"gmail:user@gmail.example.com", "outlook:user@outlook.example.com"}, names)

	ids, err = archive.Accounts(filepath.Join(root, "missing"))
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestFolderPath(t *testing.T) {
	cases := map[string]struct {
		segments []string
		want     string
	}{
		"plain":        {[]string{"Work"}, "Work"},
		"nested":       {[]string{"Work", "Projects"}, "Work/Projects"},
		"brackets":     {[]string{"[Gmail]", "All Mail"}, "[Gmail]/All Mail"},
		"percent":      {[]string{"100%"}, "100%25"},
		"dot":          {[]string{"."}, "%2E"},
		"dot dot":      {[]string{"a", ".."}, "a/%2E%2E"},
		"empty":        {[]string{"a", "", "b"}, "a/%00/b"},
		"escaped name": {[]string{"%2E"}, "%252E"},
		"system name":  {[]string{"Archive"}, "%41rchive"},
		"system top":   {[]string{"Sent", "Old"}, "%53ent/Old"},
		"system below": {[]string{"Work", "Inbox"}, "Work/Inbox"},
		"other case":   {[]string{"inbox"}, "%69nbox"}, // the same directory on case-insensitive filesystems (D178)
		"slash":        {[]string{"a/b"}, "a%2Fb"},
		"traversal":    {[]string{"x/../../../tmp"}, "x%2F..%2F..%2F..%2Ftmp"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, filepath.FromSlash(tc.want), archive.FolderPath(tc.segments, archive.MailFolders()))
		})
	}
}

func TestFolderPath_OtherSystemFolders(t *testing.T) {
	assert.Equal(t, "%43alendar", archive.FolderPath([]string{"Calendar"}, []string{archive.Calendar}))
	// Whatever the case: on many filesystems it is the same directory (D178).
	assert.Equal(t, "%63alendar", archive.FolderPath([]string{"calendar"}, []string{archive.Calendar}))
	assert.Equal(t, "Inbox", archive.FolderPath([]string{"Inbox"}, []string{archive.Calendar}))
}

func TestUnique(t *testing.T) {
	assert.Equal(t, map[string]string{
		"a": "Work", "b": "Work (2)", "c": "Work (3)", "d": "Home",
	}, archive.Unique(map[string]string{"c": "Work", "a": "Work", "b": "Work", "d": "Home"}))

	assert.Equal(t, map[string]string{
		"a": "Work", "b": "Work (3)", "c": "Work (2)",
	}, archive.Unique(map[string]string{"a": "Work", "b": "Work", "c": "Work (2)"}), "a real \"Work (2)\" keeps its name")
	assert.Equal(t, map[string]string{
		"a": "Work", "b": "work (2)", "c": "WORK (3)",
	}, archive.Unique(map[string]string{"a": "Work", "b": "work", "c": "WORK"}), "names differing only in case are one directory on many filesystems")
}

func TestStore_Read(t *testing.T) {
	store := archive.NewStore(filepath.Join(t.TempDir(), "cal"), ".ics")
	require.NoError(t, store.Write("Calendar", "e1", []byte("x")))

	data, err := store.Read("Calendar", "e1")
	require.NoError(t, err)
	assert.Equal(t, "x", string(data))

	_, err = store.Read("Calendar", "missing")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestSubfolder(t *testing.T) {
	assert.Equal(t, filepath.FromSlash("Inbox/Archive"), archive.Subfolder("Inbox", "Archive"))
	assert.Equal(t, filepath.FromSlash("Inbox/%2E%2E"), archive.Subfolder("Inbox", ".."))
	assert.Equal(t, filepath.FromSlash("Work/50%25"), archive.Subfolder("Work", "50%"))
	assert.Equal(t, filepath.FromSlash("Work/..%2F..%2Fetc"), archive.Subfolder("Work", "../../etc"))
}

func TestValidID(t *testing.T) {
	assert.True(t, archive.ValidID("18c2f9a0b1d2e3f4"))
	assert.True(t, archive.ValidID("AAMk-_="))
	assert.False(t, archive.ValidID(""))
	assert.False(t, archive.ValidID("../x"))
	assert.False(t, archive.ValidID("a/b"))
	assert.False(t, archive.ValidID("a.b"))
	assert.True(t, archive.ValidID(strings.Repeat("a", 250)))
	assert.False(t, archive.ValidID(strings.Repeat("a", 251)))
	// Capitals take two characters in a file name (D178).
	assert.True(t, archive.ValidID(strings.Repeat("A", 125)))
	assert.False(t, archive.ValidID(strings.Repeat("A", 126)))
}

// IDs that differ only in case get names that differ in more than case,
// so a case-insensitive filesystem keeps both (D178).
func TestStore_IDsDifferingInCaseStayApart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mail")
	store := archive.NewStore(root, ".eml")

	for _, id := range []string{"AAkAbC", "AAkaBc", "aakabc"} {
		require.NoError(t, store.Write(archive.Inbox, id, []byte(id)))
	}

	entries, err := os.ReadDir(filepath.Join(root, archive.Inbox))
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		assert.Equal(t, strings.ToLower(e.Name()), e.Name(), "names are lowercase")
		names = append(names, e.Name())
	}

	assert.ElementsMatch(t, []string{"~a~ak~ab~c.eml", "~a~aka~bc.eml", "aakabc.eml"}, names)

	items, err := store.Scan()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"AAkAbC": {archive.Inbox}, "AAkaBc": {archive.Inbox}, "aakabc": {archive.Inbox},
	}, items)

	data, err := store.Read(archive.Inbox, "AAkaBc")
	require.NoError(t, err)
	assert.Equal(t, "AAkaBc", string(data))

	// A name pimvault does not write is not an item, and is left alone.
	require.NoError(t, os.WriteFile(filepath.Join(root, archive.Inbox, "Upper.eml"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, archive.Inbox, "bad~.eml"), nil, 0o600))

	items, err = store.Scan()
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestStore_WriteScanMoveRemove(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mail")
	store := archive.NewStore(root, ".eml")

	items, err := store.Scan()
	require.NoError(t, err)
	assert.Empty(t, items, "missing root is an empty store")

	require.NoError(t, store.Write("Inbox", "m1", []byte("one")))
	require.NoError(t, store.Write(filepath.Join("Work", "Projects"), "m2", []byte("two")))
	require.NoError(t, store.Write("Sent", "m2", []byte("two")))

	// Files that are not items are ignored.
	require.NoError(t, os.WriteFile(filepath.Join(root, "Inbox", "notes.txt"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Inbox", "bad.name.eml"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "top.eml"), nil, 0o600))
	require.NoError(t, os.Symlink(filepath.Join(root, "Inbox", "m1.eml"), filepath.Join(root, "Inbox", "link.eml")))

	items, err = store.Scan()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"m1": {"Inbox"},
		"m2": {"Sent", filepath.Join("Work", "Projects")},
	}, items)

	require.NoError(t, store.Move("m1", "Inbox", "Archive"))
	require.NoError(t, store.Remove("Sent", "m2"))

	items, err = store.Scan()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"m1": {"Archive"},
		"m2": {filepath.Join("Work", "Projects")},
	}, items)

	data, err := os.ReadFile(filepath.Join(root, "Archive", "m1.eml")) //nolint:gosec // G304: test path.
	require.NoError(t, err)
	assert.Equal(t, "one", string(data))
}

func TestStore_FilePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mail")
	store := archive.NewStore(root, ".eml")
	require.NoError(t, store.Write("Inbox", "m1", []byte("x")))

	info, err := os.Stat(filepath.Join(root, "Inbox", "m1.eml"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	info, err = os.Stat(filepath.Join(root, "Inbox"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestStore_CleanTemp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mail")
	store := archive.NewStore(root, ".eml")
	require.NoError(t, store.CleanTemp(), "missing root is fine")

	require.NoError(t, store.Write("Inbox", "m1", []byte("x")))
	leftover := filepath.Join(root, "Inbox", ".pimvault-42.tmp")
	require.NoError(t, os.WriteFile(leftover, []byte("partial"), 0o600))

	require.NoError(t, store.CleanTemp())

	_, err := os.Stat(leftover)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(root, "Inbox", "m1.eml"))
	require.NoError(t, err)
}

func TestStore_PruneEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mail")
	store := archive.NewStore(root, ".eml")
	require.NoError(t, store.PruneEmpty(), "missing root is fine")

	require.NoError(t, store.Write(filepath.Join("Work", "Projects"), "m1", []byte("x")))
	require.NoError(t, store.Write("Inbox", "m2", []byte("x")))
	require.NoError(t, store.Remove(filepath.Join("Work", "Projects"), "m1"))

	require.NoError(t, store.PruneEmpty())

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "Inbox", entries[0].Name())
}

func TestFolderSegments_ReversesFolderPath(t *testing.T) {
	for _, segments := range [][]string{
		{"Work", "Projects"},
		{"Inbox"},           // a user label named like a system folder
		{"a/b", "100%", ""}, // escaped characters and an empty name
		{".", ".."},
		{"Ünïcödé 名前"},
	} {
		path := archive.FolderPath(segments, archive.MailFolders())
		assert.Equal(t, segments, archive.FolderSegments(path), path)
	}

	assert.Equal(t, []string{"Work (2)"}, archive.FolderSegments("Work (2)"))
}

func TestStore_DiscardKeepsTheFileAside(t *testing.T) {
	dir := t.TempDir()
	id, err := account.Parse("gmail:user@gmail.example.com")
	require.NoError(t, err)

	day := time.Date(2026, 10, 26, 23, 30, 0, 0, time.UTC)
	aside := filepath.Join(archive.KeptAside(dir, id, day), "mail")
	assert.Equal(t, filepath.Join(dir, ".deleted", "gmail_user@gmail.example.com", "2026-10-26", "mail"), aside)

	store := archive.NewStore(filepath.Join(archive.AccountDir(dir, id), "mail"), ".eml").KeepingAside(aside)
	require.NoError(t, store.Write("Work/Projects", "m1", []byte("one")))
	require.NoError(t, store.Discard("Work/Projects", "m1"))

	have, err := store.Scan()
	require.NoError(t, err)
	assert.Empty(t, have)

	data, err := os.ReadFile(filepath.Join(aside, "Work", "Projects", "m1.eml")) //nolint:gosec // G304: test path.
	require.NoError(t, err)
	assert.Equal(t, "one", string(data))

	require.Error(t, archive.NewStore(dir, ".eml").Discard("Inbox", "x"), "a store with nowhere to keep files refuses")
}

func TestPruneKeptAside(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".deleted", "gmail_user@gmail.example.com")
	for _, name := range []string{"2026-09-26", "2026-09-27", "2026-10-26", "notes"} {
		require.NoError(t, os.MkdirAll(filepath.Join(base, name, "mail"), 0o700))
	}

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".deleted", "outlook_x@outlook.example.com", "2026-01-01"), 0o700))

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, archive.PruneKeptAside(dir, now))
	assert.NoDirExists(t, filepath.Join(dir, ".deleted", "outlook_x@outlook.example.com", "2026-01-01"), "every account's")

	entries, err := os.ReadDir(base)
	require.NoError(t, err)

	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}

	assert.Equal(t, []string{"2026-09-27", "2026-10-26", "notes"}, left, "kept 30 days; other names left alone")

	require.NoError(t, archive.PruneKeptAside(t.TempDir(), now), "nothing kept aside yet")
}

func TestStore_LongFolderNamesAreCutOnDisk(t *testing.T) {
	root, aside := t.TempDir(), t.TempDir()
	store := archive.NewStore(root, ".eml").KeepingAside(aside)

	long := strings.Repeat("ö", 200)                                    // 400 bytes
	slashed := strings.Repeat("a", 240) + "/" + strings.Repeat("b", 30) // the escape is not split
	folder := archive.FolderPath([]string{long, "Sub"}, archive.MailFolders())
	other := archive.FolderPath([]string{slashed}, archive.MailFolders())

	require.NoError(t, store.Write(folder, "m1", []byte("one")))
	require.NoError(t, store.Write(other, "m2", []byte("two")))

	have, err := store.Scan()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"m1": {folder}, "m2": {other}}, have, "folder paths keep the whole names")
	assert.Equal(t, []string{long, "Sub"}, archive.FolderSegments(have["m1"][0]))
	assert.Equal(t, []string{slashed}, archive.FolderSegments(have["m2"][0]))

	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	for _, e := range entries {
		assert.LessOrEqual(t, len(e.Name()), 255, e.Name())
		assert.True(t, utf8.ValidString(e.Name()), "cut at a character boundary")
		assert.Regexp(t, `~[0-9a-f]{8}$`, e.Name())

		name, readErr := os.ReadFile(filepath.Join(root, e.Name(), ".folder-name")) //nolint:gosec // G304: test path.
		require.NoError(t, readErr)
		assert.Contains(t, []string{long + "\n", slashed + "\n"}, string(name))
	}

	data, err := store.Read(folder, "m1")
	require.NoError(t, err)
	assert.Equal(t, "one", string(data))

	require.NoError(t, store.Move("m1", folder, other))
	require.NoError(t, store.Discard(other, "m2"))
	require.NoError(t, store.PruneEmpty())

	have, err = store.Scan()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"m1": {other}}, have)

	entries, err = os.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the emptied long folder is pruned with its name file")

	kept, err := archive.NewStore(aside, ".eml").Scan()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"m2": {other}}, kept, "kept aside under the same name")
}

func TestStore_ANameFileOfAnotherFolderIsIgnored(t *testing.T) {
	root := t.TempDir()
	store := archive.NewStore(root, ".eml")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "Work"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Work", ".folder-name"), []byte("Something else\n"), 0o600))
	require.NoError(t, store.Write("Work", "m1", []byte("one")))

	have, err := store.Scan()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"m1": {"Work"}}, have)
}
