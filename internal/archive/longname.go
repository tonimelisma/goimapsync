package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tonimelisma/pimvault/internal/atomicfile"
)

// Folder names too long for a directory name (D188). A folder path above
// the Store keeps every name whole; only on disk is a segment longer than
// maxSegment bytes stored as the segment cut at a character boundary plus
// "~" and 8 hex characters of a hash of the whole segment, with the folder's
// name in a nameFile inside the directory.
const (
	nameFile   = ".folder-name"
	maxSegment = 255
	hashLen    = 8
)

// diskSegment returns the directory name a folder path segment is stored
// under, and whether it was cut.
func diskSegment(seg string) (string, bool) {
	if len(seg) <= maxSegment {
		return seg, false
	}

	cut := maxSegment - 1 - hashLen
	for cut > 0 && !utf8.RuneStart(seg[cut]) {
		cut--
	}

	// An escape ("%2F") is not split.
	if i := strings.LastIndexByte(seg[:cut], '%'); i >= 0 && i >= cut-2 {
		cut = i
	}

	sum := sha256.Sum256([]byte(seg))

	return seg[:cut] + "~" + hex.EncodeToString(sum[:])[:hashLen], true
}

// diskFolder returns the relative directory a folder path is stored in.
func diskFolder(folder string) string {
	segs := strings.Split(filepath.ToSlash(folder), "/")
	for i, seg := range segs {
		segs[i], _ = diskSegment(seg)
	}

	return filepath.Join(segs...)
}

// makeFolder creates a folder path's directories under base, and makes the
// name file of each directory whose name was cut name its folder, writing
// it when it is missing or names another.
func makeFolder(base, folder string) error {
	if err := os.MkdirAll(filepath.Join(base, diskFolder(folder)), dirPerm); err != nil {
		return fmt.Errorf("create folder %s: %w", folder, err)
	}

	dir := base

	for seg := range strings.SplitSeq(filepath.ToSlash(folder), "/") {
		disk, cut := diskSegment(seg)
		dir = filepath.Join(dir, disk)

		if !cut || namedBy(dir, disk) == seg {
			continue
		}

		if err := atomicfile.Write(filepath.Join(dir, nameFile), []byte(unescapeSegment(seg)+"\n"), filePerm); err != nil {
			return fmt.Errorf("create folder %s: %w", folder, err)
		}
	}

	return nil
}

// namedBy returns the folder path segment a directory's name file gives, or
// "" when it has none that names this directory.
func namedBy(dir, disk string) string {
	if !maybeCut(disk) {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(dir, nameFile)) //nolint:gosec // G304: an archive directory's name file.
	if err != nil {
		return ""
	}

	if seg := escapeSegment(strings.TrimSuffix(string(data), "\n"), nil); diskName(seg) == disk {
		return seg
	}

	return ""
}

// maybeCut reports whether a directory name can be a cut one: long, and
// ending in "~" and the hash.
func maybeCut(disk string) bool {
	if len(disk) <= maxSegment-hashLen-4 || disk[len(disk)-hashLen-1] != '~' {
		return false
	}

	_, err := hex.DecodeString(disk[len(disk)-hashLen:])

	return err == nil
}

// folderNames turns directories found on disk back into folder paths,
// reading the name file of directories whose name was cut.
type folderNames struct {
	base string
	// segments caches each directory's folder path segment by its path.
	segments map[string]string
}

func newFolderNames(base string) *folderNames {
	return &folderNames{base: base, segments: map[string]string{}}
}

// folder returns the folder path of a directory relative to base.
func (n *folderNames) folder(rel string) string {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	dir := n.base

	for i, disk := range segs {
		dir = filepath.Join(dir, disk)
		segs[i] = n.segment(dir, disk)
	}

	return filepath.Join(segs...)
}

// segment returns the folder path segment of one directory: its name, or
// the name in its name file when the directory name is that name cut.
func (n *folderNames) segment(dir, disk string) string {
	if seg, ok := n.segments[dir]; ok {
		return seg
	}

	seg := namedBy(dir, disk)
	if seg == "" {
		seg = disk
	}

	n.segments[dir] = seg

	return seg
}

// diskName returns the directory name of a segment.
func diskName(seg string) string {
	disk, _ := diskSegment(seg)

	return disk
}

// removeNameFile removes the name file pimvault wrote in a directory that
// holds nothing else, so the directory can be pruned. Any other file of
// that name is left alone.
func removeNameFile(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err //nolint:wrapcheck // PruneEmpty names the directory.
	}

	if len(entries) == 1 && entries[0].Name() == nameFile && namedBy(dir, filepath.Base(dir)) != "" {
		return os.Remove(filepath.Join(dir, nameFile)) //nolint:wrapcheck // as above.
	}

	return nil
}
