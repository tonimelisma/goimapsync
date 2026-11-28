package account

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/tokenfile"
)

func saveToken(t *testing.T, r Registry, id ID) {
	t.Helper()

	require.NoError(t, tokenfile.Save(r.TokenPath(id), &oauth2.Token{RefreshToken: "refresh"}))
}

func TestRegistry_MissingDirectoryIsEmpty(t *testing.T) {
	t.Parallel()

	r := NewRegistry(filepath.Join(t.TempDir(), "missing"))

	ids, err := r.List()
	require.NoError(t, err)
	assert.Empty(t, ids)
}

// Validates: PV-002, PV-223
func TestRegistry_ListSortedAndIgnoresOtherFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := NewRegistry(dir)
	outlook := ID{Provider: Outlook, Email: "user@outlook.example.com"}
	gmail := ID{Provider: Gmail, Email: "user@gmail.example.com"}
	saveToken(t, r, outlook)
	saveToken(t, r, gmail)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(""), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "token_gmail_dir@example.com.json"), 0o700))

	ids, err := r.List()
	require.NoError(t, err)
	assert.Equal(t, []ID{gmail, outlook}, ids)
}

func TestRegistry_ListReportsBrokenFilesAndKeepsValidOnes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := NewRegistry(dir)
	good := ID{Provider: Gmail, Email: "good@gmail.example.com"}
	broken := ID{Provider: Gmail, Email: "broken@gmail.example.com"}
	saveToken(t, r, good)
	require.NoError(t, os.WriteFile(r.TokenPath(broken), []byte("{not json"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "token_yahoo_x@yahoo.example.com.json"), []byte("{}"), 0o600))

	ids, err := r.List()
	assert.Equal(t, []ID{good}, ids)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gmail:broken@gmail.example.com")
	assert.Contains(t, err.Error(), "token_yahoo_x@yahoo.example.com.json")

	require.ErrorIs(t, err, ErrNeedsLogin)

	all, badNames, idsErr := r.IDs()
	require.NoError(t, idsErr)
	assert.Equal(t, []ID{broken, good}, all)
	require.Error(t, badNames)
	assert.Contains(t, badNames.Error(), "token_yahoo_x@yahoo.example.com.json")
}

// Validates: PV-223
func TestRegistry_Remove(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := NewRegistry(dir)
	id := ID{Provider: Gmail, Email: "user@gmail.example.com"}
	saveToken(t, r, id)

	require.NoError(t, r.Remove(id))

	ids, err := r.List()
	require.NoError(t, err)
	assert.Empty(t, ids)
	require.ErrorIs(t, r.Remove(id), tokenfile.ErrNotFound)
}

func TestRegistry_UnreadableDirectoryIsAnError(t *testing.T) {
	t.Parallel()

	// A file where the registry directory should be cannot be listed.
	notDir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notDir, []byte("x"), 0o600))
	r := NewRegistry(notDir)

	ids, badNames, err := r.IDs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read account registry")
	assert.Nil(t, ids)
	require.NoError(t, badNames)

	listed, listErr := r.List()
	require.Error(t, listErr)
	assert.Nil(t, listed)
}
