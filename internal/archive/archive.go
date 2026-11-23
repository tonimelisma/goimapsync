// Package archive owns the files of the local archive: where an account's
// items live (docs/requirements.md D29), how folder names become directories
// (D142), and writing, moving and removing item files safely (D54, D58).
// Items are files named by their provider ID, capitals marked, plus an
// extension (D36, D45, D178).
package archive

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/atomicfile"
)

const (
	dirPerm  = 0o700
	filePerm = 0o600
)

// AccountDir returns an account's directory in the archive.
func AccountDir(root string, id account.ID) string {
	return filepath.Join(root, id.DirName())
}

// keptAsideDir holds the files of items deleted in the account (D181).
// Its name is no account's, so Accounts, restore, migrate and verify pass it
// by.
const keptAsideDir = ".deleted"

// keepAsideDays is how many days deleted items are kept aside (D181).
const keepAsideDays = 30

// dayLayout names a day's directory of kept-aside files.
const dayLayout = "2006-01-02"

// KeptAside returns the directory an account's items deleted on day are
// kept in (D181): <root>/.deleted/<account>/<YYYY-MM-DD>, by the day in
// day's time zone.
func KeptAside(root string, id account.ID, day time.Time) string {
	return filepath.Join(root, keptAsideDir, id.DirName(), day.Format(dayLayout))
}

// PruneKeptAside deletes, for every account, the kept-aside days that are
// 30 or more days before now's day (D181): on 26 October, 26 September
// goes. Directories not named like a day are left alone.
func PruneKeptAside(root string, now time.Time) error {
	accounts, err := os.ReadDir(filepath.Join(root, keptAsideDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("prune deleted items: %w", err)
	}

	var errs []error

	for _, a := range accounts {
		if a.IsDir() {
			errs = append(errs, pruneDays(filepath.Join(root, keptAsideDir, a.Name()), now))
		}
	}

	return errors.Join(errs...)
}

// pruneDays deletes one account's kept-aside days that are due.
func pruneDays(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("prune deleted items: %w", err)
	}

	today, err := time.ParseInLocation(dayLayout, now.Format(dayLayout), now.Location())
	if err != nil {
		return fmt.Errorf("prune deleted items: %w", err)
	}

	var errs []error

	for _, e := range entries {
		day, parseErr := time.ParseInLocation(dayLayout, e.Name(), now.Location())
		if parseErr != nil || !e.IsDir() || day.AddDate(0, 0, keepAsideDays).After(today) {
			continue
		}

		if removeErr := os.RemoveAll(filepath.Join(dir, e.Name())); removeErr != nil {
			errs = append(errs, removeErr)
		}
	}

	if joined := errors.Join(errs...); joined != nil {
		return fmt.Errorf("prune deleted items: %w", joined)
	}

	return nil
}

// Accounts returns the accounts with a directory, or a symlink to one, in
// the archive. A missing archive has none; other entries are ignored.
func Accounts(root string) ([]account.ID, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("list archived accounts: %w", err)
	}

	var ids []account.ID

	for _, e := range entries {
		id, parseErr := account.ParseDirName(e.Name())
		if parseErr != nil {
			continue
		}

		// os.Stat follows a symlinked account directory.
		if info, statErr := os.Stat(filepath.Join(root, e.Name())); statErr == nil && info.IsDir() {
			ids = append(ids, id)
		}
	}

	return ids, nil
}

// validID matches the provider IDs pimvault stores: Gmail's hex IDs and
// Outlook's base64url immutable IDs.
var validID = regexp.MustCompile(`^[A-Za-z0-9_=-]+$`)

// maxName is the longest file name an ID may take: 250 plus a 4-character
// extension fits the 255-byte file name limit. Outlook's immutable IDs are
// about 150 characters, about 200 once their capitals are marked.
const maxName = 250

// ValidID reports whether a provider ID can be stored as a file.
func ValidID(id string) bool {
	capitals := 0

	for _, r := range id {
		if r >= 'A' && r <= 'Z' {
			capitals++
		}
	}

	return validID.MatchString(id) && len(id)+capitals <= maxName
}

// capital marks a capital letter in a file name (D178).
const capital = '~'

// fileName returns the file name, without extension, of a provider ID.
// Outlook's IDs are case-sensitive but many filesystems are not (macOS by
// default, exFAT, most SMB shares), so each capital letter is written as
// '~' and its lowercase letter (D178): names then differ in more than case
// whenever their IDs differ.
func fileName(id string) string {
	var b strings.Builder

	for _, r := range id {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(capital)
			r += 'a' - 'A'
		}

		b.WriteRune(r)
	}

	return b.String()
}

// idOf returns the provider ID a file name stands for, or false when the
// name is not one fileName writes.
func idOf(name string) (string, bool) {
	var b strings.Builder

	for i := 0; i < len(name); i++ {
		c := name[i]

		switch {
		case c >= 'A' && c <= 'Z':
			return "", false
		case c == capital:
			if i+1 == len(name) || name[i+1] < 'a' || name[i+1] > 'z' {
				return "", false
			}

			i++
			b.WriteByte(name[i] - ('a' - 'A'))
		default:
			b.WriteByte(c)
		}
	}

	id := b.String()

	return id, len(name) <= maxName && validID.MatchString(id)
}

// System folder directories, the same for every provider: mail folders
// (D30, D143) and the default calendar, address book and task list (D149).
const (
	Inbox    = "Inbox"
	Drafts   = "Drafts"
	Sent     = "Sent"
	Archive  = "Archive"
	Calendar = "Calendar"
	Contacts = "Contacts"
	Tasks    = "Tasks"
)

// MailFolders are the system mail folder directories.
func MailFolders() []string {
	return []string{Inbox, Drafts, Sent, Archive}
}

// FolderPath turns the name segments of a user folder (a Gmail label, an
// Outlook folder, a calendar) into a relative directory path. Segments are
// escaped so that different names never share a directory and no name leaves
// the store: "%" and "/" are written as %25 and %2F, the names "", "." and
// ".." are percent-encoded, and a top-level name equal to one of the store's
// system folders has its first letter encoded ("Archive" becomes %41rchive).
func FolderPath(segments []string, system []string) string {
	escaped := make([]string, len(segments))

	for i, seg := range segments {
		var reserved []string
		if i == 0 {
			reserved = system
		}

		escaped[i] = escapeSegment(seg, reserved)
	}

	return filepath.Join(escaped...)
}

// emptySegment stands for an empty folder name.
const emptySegment = "%00"

// FolderSegments reverses FolderPath and Subfolder: the folder names of a
// directory path, outermost first. A " (2)" suffix from Unique stays part of
// the name.
func FolderSegments(path string) []string {
	parts := strings.Split(filepath.ToSlash(path), "/")

	for i, p := range parts {
		parts[i] = unescapeSegment(p)
	}

	return parts
}

// unescapeSegment reverses escapeSegment.
func unescapeSegment(seg string) string {
	if seg == emptySegment {
		return ""
	}

	var b strings.Builder

	for i := 0; i < len(seg); i++ {
		if seg[i] == '%' && i+2 < len(seg) {
			if v, err := strconv.ParseUint(seg[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 2

				continue
			}
		}

		b.WriteByte(seg[i])
	}

	return b.String()
}

// Subfolder returns the path of a folder named name inside parent.
func Subfolder(parent, name string) string {
	return filepath.Join(parent, escapeSegment(name, nil))
}

// Unique makes directory paths distinct, ignoring case as many filesystems
// do (D178): when several IDs map to the same path, the one whose ID sorts
// first keeps it and the others get the first free " (2)", " (3)" and so
// on, in ID order. A suffixed path never takes a path another ID already
// has.
func Unique(paths map[string]string) map[string]string {
	taken := make(map[string]bool, len(paths))
	for _, p := range paths {
		taken[strings.ToLower(p)] = true
	}

	byPath := make(map[string][]string)
	for id, p := range paths {
		byPath[strings.ToLower(p)] = append(byPath[strings.ToLower(p)], id)
	}

	out := make(map[string]string, len(paths))

	for _, key := range slices.Sorted(maps.Keys(byPath)) {
		ids := byPath[key]
		slices.Sort(ids)

		out[ids[0]] = paths[ids[0]]

		n := 2

		for _, id := range ids[1:] {
			for taken[strings.ToLower(fmt.Sprintf("%s (%d)", paths[id], n))] {
				n++
			}

			out[id] = fmt.Sprintf("%s (%d)", paths[id], n)
			taken[strings.ToLower(out[id])] = true
		}
	}

	return out
}

func escapeSegment(seg string, reserved []string) string {
	seg = strings.ReplaceAll(seg, "%", "%25")
	seg = strings.ReplaceAll(seg, "/", "%2F")

	switch {
	case seg == "":
		return emptySegment
	case seg == "." || seg == "..":
		return strings.ReplaceAll(seg, ".", "%2E")
	case slices.ContainsFunc(reserved, func(r string) bool { return strings.EqualFold(r, seg) }):
		return fmt.Sprintf("%%%02X", seg[0]) + seg[1:]
	default:
		return seg
	}
}

// Store is one kind of item (such as mail) of one account: folders of item
// files under root, each file named by its ID, capitals marked (D178), plus
// ext.
type Store struct {
	root string
	ext  string
	// aside is where Discard puts files: the store's directory among the
	// kept-aside files of the day (D181).
	aside string
}

// NewStore returns the store rooted at dir for files ending in ext.
func NewStore(dir, ext string) Store {
	return Store{root: dir, ext: ext}
}

// KeepingAside returns the store with Discard putting files in dir, the
// same type and folder directories below it.
func (s Store) KeepingAside(dir string) Store {
	s.aside = dir

	return s
}

// Ext returns the extension of the store's item files.
func (s Store) Ext() string {
	return s.ext
}

// FilePath returns an item's file path relative to the store's directory,
// as it is on disk.
func (s Store) FilePath(folder, id string) string {
	return filepath.Join(diskFolder(folder), fileName(id)+s.ext)
}

// Root returns the store's directory.
func (s Store) Root() string {
	return s.root
}

// file returns an item's file path; a folder name too long for a directory
// is stored cut (D188).
func (s Store) file(folder, id string) string {
	return filepath.Join(s.root, s.FilePath(folder, id))
}

// Scan returns, for every item file, the folders that hold it (normally one).
// Files that are not items of this store (other names, files directly in the
// root, symlinks) are ignored and never touched.
func (s Store) Scan() (map[string][]string, error) {
	items := make(map[string][]string)
	names := newFolderNames(s.root)

	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == s.root {
				return fs.SkipAll
			}

			return err
		}

		if !d.Type().IsRegular() {
			return nil
		}

		name, isItem := strings.CutSuffix(d.Name(), s.ext)
		if !isItem {
			return nil
		}

		id, isItem := idOf(name)
		if !isItem {
			return nil
		}

		folder, err := filepath.Rel(s.root, filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("locate %s: %w", path, err)
		}

		if folder != "." {
			items[id] = append(items[id], names.folder(folder))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan archive %s: %w", s.root, err)
	}

	for _, folders := range items {
		slices.Sort(folders)
	}

	return items, nil
}

// Read returns an item's file contents.
func (s Store) Read(folder, id string) ([]byte, error) {
	data, err := os.ReadFile(s.file(folder, id))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.file(folder, id), err)
	}

	return data, nil
}

// Open opens an item's file for reading.
func (s Store) Open(folder, id string) (*os.File, error) {
	f, err := os.Open(s.file(folder, id))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", s.file(folder, id), err)
	}

	return f, nil
}

// ModTime returns when an item's file was last written.
func (s Store) ModTime(folder, id string) (time.Time, error) {
	info, err := os.Stat(s.file(folder, id))
	if err != nil {
		return time.Time{}, fmt.Errorf("stat %s: %w", s.file(folder, id), err)
	}

	return info.ModTime(), nil
}

// Write stores an item's contents in folder, replacing any earlier file.
func (s Store) Write(folder, id string, data []byte) error {
	path := s.file(folder, id)

	if err := makeFolder(s.root, folder); err != nil {
		return err
	}

	if err := atomicfile.Write(path, data, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// Move moves an item's file from one folder to another.
func (s Store) Move(id, from, to string) error {
	if err := makeFolder(s.root, to); err != nil {
		return err
	}

	if err := moveFile(s.file(from, id), s.file(to, id)); err != nil {
		return fmt.Errorf("move %s to %s: %w", s.file(from, id), to, err)
	}

	return nil
}

// moveFile moves a file into an existing directory and syncs both
// directories. Across filesystems (an account directory symlinked to
// another disk) it copies the file and deletes the original.
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if errors.Is(err, syscall.EXDEV) {
		err = copyAndRemove(src, dst)
	}

	if err != nil {
		return err
	}

	return errors.Join(atomicfile.SyncDir(filepath.Dir(dst)), atomicfile.SyncDir(filepath.Dir(src)))
}

// copyAndRemove writes a copy of src at dst, then deletes src.
func copyAndRemove(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec // G304: an archive file.
	if err != nil {
		return err //nolint:wrapcheck // the caller names the move.
	}

	if err := atomicfile.Write(dst, data, filePerm); err != nil {
		return err
	}

	return os.Remove(src) //nolint:wrapcheck // as above.
}

// Discard moves the file of an item deleted in the account out of the
// store, to where it is kept aside (D181).
func (s Store) Discard(folder, id string) error {
	if s.aside == "" {
		return fmt.Errorf("discard %s: no directory to keep it in", s.file(folder, id))
	}

	if err := makeFolder(s.aside, folder); err != nil {
		return err
	}

	if err := moveFile(s.file(folder, id), filepath.Join(s.aside, s.FilePath(folder, id))); err != nil {
		return fmt.Errorf("discard %s: %w", s.file(folder, id), err)
	}

	return nil
}

// Remove deletes an item's file from folder.
func (s Store) Remove(folder, id string) error {
	path := s.file(folder, id)

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	if err := atomicfile.SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	return nil
}

// CleanTemp removes temp files an interrupted run left behind (D58).
func (s Store) CleanTemp() error {
	var errs []error

	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == s.root {
				return fs.SkipAll
			}

			return err
		}

		if d.Type().IsRegular() && atomicfile.IsTemp(d.Name()) {
			// G122: the archive belongs to the user running pimvault; no
			// other party can swap a directory for a symlink mid-walk.
			if removeErr := os.Remove(path); removeErr != nil { //nolint:gosec // see above.
				errs = append(errs, removeErr)
			}
		}

		return nil
	})

	if joined := errors.Join(append(errs, err)...); joined != nil {
		return fmt.Errorf("remove temp files in %s: %w", s.root, joined)
	}

	return nil
}

// PruneEmpty removes empty folders left after items moved or were deleted,
// counting a folder that holds only its name file (D188) as empty. The
// store's root is kept.
func (s Store) PruneEmpty() error {
	var dirs []string

	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == s.root {
				return fs.SkipAll
			}

			return err
		}

		if d.IsDir() && path != s.root {
			dirs = append(dirs, path)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("prune folders in %s: %w", s.root, err)
	}

	// Deepest first, so a parent is empty once its empty children are gone.
	slices.Reverse(dirs)

	var errs []error

	for _, dir := range dirs {
		if nameErr := removeNameFile(dir); nameErr != nil {
			errs = append(errs, nameErr)

			continue
		}

		removeErr := os.Remove(dir)
		if removeErr != nil && !errors.Is(removeErr, syscall.ENOTEMPTY) && !errors.Is(removeErr, syscall.EEXIST) {
			errs = append(errs, removeErr)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("prune folders in %s: %w", s.root, err)
	}

	return nil
}
