package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Validates: PV-062
func TestDiskSegment(t *testing.T) {
	short := strings.Repeat("a", 255)
	got, cut := diskSegment(short)
	assert.Equal(t, short, got)
	assert.False(t, cut)

	for name, tc := range map[string]struct {
		seg     string
		keepLen int
	}{
		// The cut at 246 falls inside the second byte of an "ö".
		"character": {seg: "a" + strings.Repeat("ö", 200), keepLen: 245},
		// An escape at 244 would be split by the cut at 246.
		"escape": {seg: strings.Repeat("a", 244) + "%2F" + strings.Repeat("b", 20), keepLen: 244},
	} {
		got, cut := diskSegment(tc.seg)
		assert.True(t, cut, name)
		assert.LessOrEqual(t, len(got), 255, name)
		assert.True(t, utf8.ValidString(got), name)
		assert.Equal(t, tc.seg[:tc.keepLen], got[:len(got)-9], name)
		assert.Regexp(t, `~[0-9a-f]{8}$`, got, name)
		assert.True(t, maybeCut(got), name)
	}
}

func TestMakeFolder_RepairsANameFileThatNamesAnotherFolder(t *testing.T) {
	base := t.TempDir()
	seg := strings.Repeat("ö", 200)
	disk := diskName(seg)

	require.NoError(t, makeFolder(base, seg))
	require.NoError(t, os.WriteFile(filepath.Join(base, disk, nameFile), []byte("wrong\n"), 0o600))
	assert.Equal(t, disk, newFolderNames(base).folder(disk), "a name file of another folder does not count")

	require.NoError(t, makeFolder(base, seg))
	assert.Equal(t, seg, newFolderNames(base).folder(disk))
}

func TestRemoveNameFile_LeavesOtherFilesOfThatName(t *testing.T) {
	base := t.TempDir()
	plain := filepath.Join(base, "Work")
	require.NoError(t, os.MkdirAll(plain, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(plain, nameFile), []byte("Work\n"), 0o600))

	require.NoError(t, removeNameFile(plain))
	assert.FileExists(t, filepath.Join(plain, nameFile), "not a directory pimvault cut")

	seg := strings.Repeat("ö", 200)
	require.NoError(t, makeFolder(base, seg))
	require.NoError(t, removeNameFile(filepath.Join(base, diskName(seg))))
	assert.NoFileExists(t, filepath.Join(base, diskName(seg), nameFile))
}
