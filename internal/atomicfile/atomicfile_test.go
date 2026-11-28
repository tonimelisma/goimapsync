package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/atomicfile"
)

// Validates: PV-063
func TestWrite_CreatesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "item.eml")

	require.NoError(t, atomicfile.Write(path, []byte("one"), 0o600))
	require.NoError(t, atomicfile.Write(path, []byte("two"), 0o600))

	data, err := os.ReadFile(path) //nolint:gosec // G304: test-owned temp path.
	require.NoError(t, err)
	assert.Equal(t, "two", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp file left behind")
}

// Validates: PV-063
func TestWrite_FailedRenameLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory at the target makes the rename fail, even as root.
	target := filepath.Join(dir, "item.eml")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "child"), 0o700))

	err := atomicfile.Write(target, []byte("x"), 0o600)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rename temp file")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the blocking directory remains")
}

func TestWrite_MissingDirectory(t *testing.T) {
	err := atomicfile.Write(filepath.Join(t.TempDir(), "missing", "item.eml"), []byte("x"), 0o600)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create temp file")
}

func TestIsTemp(t *testing.T) {
	assert.True(t, atomicfile.IsTemp(".pimvault-123.tmp"))
	assert.False(t, atomicfile.IsTemp("123.eml"))
	assert.False(t, atomicfile.IsTemp(".pimvault-123.eml"))
}

// Validates: PV-064
func TestCleanDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, atomicfile.CleanDir(filepath.Join(dir, "missing"), 0))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".pimvault-1.tmp"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.json"), nil, 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", ".pimvault-2.tmp"), nil, 0o600))

	require.NoError(t, atomicfile.CleanDir(dir, 0))

	assert.NoFileExists(t, filepath.Join(dir, ".pimvault-1.tmp"))
	assert.FileExists(t, filepath.Join(dir, "keep.json"))
	assert.FileExists(t, filepath.Join(dir, "sub", ".pimvault-2.tmp"), "subdirectories are not touched")
}

func TestCleanDir_MinAgeKeepsYoungTempFiles(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, ".pimvault-1.tmp")
	young := filepath.Join(dir, ".pimvault-2.tmp")

	require.NoError(t, os.WriteFile(old, nil, 0o600))
	require.NoError(t, os.WriteFile(young, nil, 0o600))

	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(old, twoHoursAgo, twoHoursAgo))

	require.NoError(t, atomicfile.CleanDir(dir, time.Hour))

	assert.NoFileExists(t, old)
	assert.FileExists(t, young, "may belong to a write going on in another process")
}
