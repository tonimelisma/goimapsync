package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/state"
)

// fakeSource serves collections from memory. Each change bumps a counter
// used as the token; changes lists items touched since the given token.
type fakeSource struct {
	colls   []collection
	items   map[string]map[string][]byte // collection ID → item ID → content
	touched map[string]map[string]int    // collection ID → item ID → change number
	seq     int
	expired bool
	fail    map[string]bool
	// failItems are items full listings cannot render.
	failItems map[string]bool
	calls     []string
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		items:   make(map[string]map[string][]byte),
		touched: make(map[string]map[string]int),
		fail:    make(map[string]bool),
	}
}

func (f *fakeSource) add(collID, path string) {
	f.colls = append(f.colls, collection{ID: collID, Path: path})
	f.items[collID] = make(map[string][]byte)
	f.touched[collID] = make(map[string]int)
}

func (f *fakeSource) put(collID, id, content string) {
	f.seq++
	f.items[collID][id] = []byte(content)
	f.touched[collID][id] = f.seq
}

func (f *fakeSource) del(collID, id string) {
	f.seq++
	delete(f.items[collID], id)
	f.touched[collID][id] = f.seq
}

func (f *fakeSource) collections(context.Context) ([]collection, error) {
	return f.colls, nil
}

func (f *fakeSource) full(_ context.Context, c collection) (listing, error) {
	f.calls = append(f.calls, "full "+c.ID)
	if f.fail[c.ID] {
		return listing{}, errors.New("injected")
	}

	l := listing{items: maps.Clone(f.items[c.ID]), token: tokenOf(f.seq)}

	for id := range f.failItems {
		delete(l.items, id)
		l.fail(id)
	}

	return l, nil
}

func (f *fakeSource) changes(_ context.Context, c collection, token string) (listing, error) {
	f.calls = append(f.calls, "changes "+c.ID)
	if f.fail[c.ID] {
		return listing{}, errors.New("injected")
	}

	if f.expired {
		return listing{}, errTokenExpired
	}

	since := seqOf(token)
	items := make(map[string][]byte)

	for id, n := range f.touched[c.ID] {
		if n > since {
			items[id] = f.items[c.ID][id] // nil when deleted
		}
	}

	return listing{items: items, token: tokenOf(f.seq)}, nil
}

func tokenOf(n int) string { return strconv.Itoa(n) }

func seqOf(s string) int {
	n, _ := strconv.Atoi(s) //nolint:errcheck // tokens come from tokenOf.

	return n
}

type collFixture struct {
	src   *fakeSource
	root  string
	aside string
	store archive.Store
	state *state.Collections
	opts  Options
	// reports records progress as "done/total".
	reports []string
}

func newCollFixture(t *testing.T) *collFixture {
	t.Helper()

	dir := t.TempDir()
	root, aside := filepath.Join(dir, "calendars"), filepath.Join(dir, "aside", "calendars")

	return &collFixture{src: newFakeSource(), root: root, aside: aside, store: archive.NewStore(root, ".ics").KeepingAside(aside)}
}

func (f *collFixture) run(t *testing.T) Result {
	t.Helper()

	f.src.calls = nil
	f.reports = nil
	progress := func(done, total int) { f.reports = append(f.reports, strconv.Itoa(done)+"/"+strconv.Itoa(total)) }

	res, next, err := runCollections(t.Context(), f.src, f.store, slog.New(slog.DiscardHandler), progress, f.opts, f.state)
	require.NoError(t, err)
	require.NotNil(t, next)
	f.state = next

	return res
}

func (f *collFixture) read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(path)))
	require.NoError(t, err)

	return string(data)
}

func TestCollections_FullThenChanges(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.put("c1", "e1", "one")
	f.src.put("c1", "e2", "two")

	f.src.add("c2", "Work")

	res := f.run(t)
	assert.Equal(t, Result{Files: 2, Downloaded: 2}, res)
	assert.Equal(t, []string{"full c1", "full c2"}, f.src.calls)
	assert.Equal(t, []string{"0/2", "1/2", "2/2"}, f.reports)

	f.src.put("c1", "e1", "one, edited")
	f.src.del("c1", "e2")
	f.src.put("c1", "e3", "three")

	res = f.run(t)
	assert.Equal(t, Result{Files: 2, Downloaded: 2, Deleted: 1}, res)
	assert.Equal(t, []string{"changes c1", "changes c2"}, f.src.calls)
	assert.Equal(t, "one, edited", f.read(t, "Calendar/e1.ics"))

	res = f.run(t)
	assert.Equal(t, Result{Files: 2}, res)
}

// Validates: PV-103
func TestCollections_UnchangedContentIsNotRewritten(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.put("c1", "e1", "one")
	f.run(t)

	f.src.put("c1", "e1", "one") // touched, same content
	f.src.expired = true         // and a full listing on top

	res := f.run(t)
	assert.Equal(t, Result{Files: 1}, res)
	assert.Equal(t, []string{"changes c1", "full c1"}, f.src.calls)
}

func TestCollections_RenameAndRemoval(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Work")
	f.src.add("c2", "Old")
	f.src.put("c1", "e1", "one")
	f.src.put("c2", "e2", "two")
	f.run(t)

	f.src.colls = []collection{{ID: "c1", Path: "Job"}}

	res := f.run(t)
	assert.Equal(t, Result{Files: 1, Downloaded: 1, Deleted: 2}, res)
	assert.Equal(t, []string{"full c1"}, f.src.calls, "a renamed collection is listed in full")
	assert.Equal(t, "one", f.read(t, "Job/e1.ics"))
	assert.NoDirExists(t, filepath.Join(f.root, "Work"))
	assert.NoDirExists(t, filepath.Join(f.root, "Old"))
}

func TestCollections_FailedCollectionKeepsFilesAndPosition(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Work")
	f.src.add("c2", "Home")
	f.src.put("c1", "e1", "one")
	f.src.put("c2", "e2", "two")
	f.run(t)
	before := f.state.Collections["c1"]

	f.src.fail["c1"] = true
	f.src.colls[0].Path = "Job" // renamed while failing
	f.src.put("c2", "e3", "three")

	res := f.run(t)
	assert.Equal(t, Result{Files: 3, Downloaded: 1, Failed: 1}, res)
	assert.Equal(t, before, f.state.Collections["c1"])
	assert.Equal(t, "one", f.read(t, "Work/e1.ics"))

	delete(f.src.fail, "c1")

	res = f.run(t)
	assert.Equal(t, Result{Files: 3, Downloaded: 1, Deleted: 1}, res)
	assert.Equal(t, "one", f.read(t, "Job/e1.ics"))
}

func TestCollections_ArchiveChangedOutsidePimvault(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.put("c1", "e1", "one")
	f.src.put("c1", "e2", "two")
	f.run(t)

	require.NoError(t, os.Remove(filepath.Join(f.root, "Calendar", "e1.ics")))

	res := f.run(t)
	assert.Equal(t, Result{Files: 2, Downloaded: 1}, res)
	assert.Equal(t, []string{"full c1"}, f.src.calls)
}

func TestCollections_ItemFailureKeepsToken(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.put("c1", "e1", "one")
	f.run(t)
	before := f.state.Collections["c1"].Token

	f.src.put("c1", "bad.id", "x") // not usable as a file name
	f.src.put("c1", "e2", "two")

	res := f.run(t)
	assert.Equal(t, Result{Files: 2, Downloaded: 1, Failed: 1}, res)
	assert.Equal(t, before, f.state.Collections["c1"].Token, "the same changes are read again")
}

func TestCollections_SameIDInTwoCollectionsIsNoDrift(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.add("c2", "Work")
	f.src.put("c1", "shared", "one")
	f.src.put("c2", "shared", "two")
	f.run(t)

	res := f.run(t)
	assert.Equal(t, Result{Files: 2}, res)
	assert.Equal(t, []string{"changes c1", "changes c2"}, f.src.calls)
}

func TestCollections_DriftRemovesUnknownDirectories(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.put("c1", "e1", "one")
	f.run(t)

	require.NoError(t, f.store.Write("Gone", "x1", []byte("stale")))

	res := f.run(t)
	assert.Equal(t, Result{Files: 1, Deleted: 1}, res)
	assert.NoDirExists(t, filepath.Join(f.root, "Gone"))
}

// Validates: PV-100
func TestCollections_FullListingDeletesEveryUnlistedFile(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		f.src.put("c1", id, id)
	}

	f.run(t)

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		f.src.del("c1", id)
	}

	f.src.put("c1", "z", "z")
	f.src.expired = true

	res := f.run(t)
	assert.Equal(t, Result{Files: 1, Downloaded: 1, Deleted: 5}, res)
	entries, err := os.ReadDir(filepath.Join(f.root, "Calendar"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "z.ics", entries[0].Name())
}

func TestCollections_ItemsTheSourceFailedToReadAreKept(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.put("c1", "e1", "one")
	f.src.put("c1", "e2", "two")
	f.run(t)

	f.src.failItems = map[string]bool{"e1": true}
	f.src.expired = true

	res := f.run(t)
	assert.Equal(t, Result{Files: 2, Failed: 1}, res)
	assert.Equal(t, "one", f.read(t, "Calendar/e1.ics"))
}

// Validates: PV-106
func TestCollections_DeletedItemsAreKeptAside(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Work")
	f.src.add("c2", "Old")
	f.src.put("c1", "e1", "one")
	f.src.put("c1", "e2", "two")
	f.src.put("c2", "e3", "three")
	f.run(t)

	f.src.del("c1", "e2")
	f.src.colls = []collection{{ID: "c1", Path: "Job"}} // renamed; Old is gone

	res := f.run(t)
	assert.Equal(t, Result{Files: 1, Downloaded: 1, Deleted: 3}, res)
	assert.Equal(t, []string{"Old/e3.ics", "Work/e2.ics"}, f.asideFiles(t),
		"deleted items are kept aside; the renamed collection's old copy of e1 is not")
}

// Validates: PV-107
func TestCollections_MassDeletionIsWithheld(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("cal", "Calendar")

	for i := range 50 {
		f.src.put("cal", fmt.Sprintf("e%02d", i), "x")
	}

	f.run(t)
	token := f.state.Collections["cal"].Token

	for i := range 11 {
		f.src.del("cal", fmt.Sprintf("e%02d", i))
	}

	f.src.put("cal", "new", "x")

	res := f.run(t)
	assert.Equal(t, Result{Files: 51, Downloaded: 1, Withheld: 11}, res)
	assert.Equal(t, token, f.state.Collections["cal"].Token, "the same changes are read again next time")

	res = f.run(t)
	assert.Equal(t, 11, res.Withheld)

	f.opts.AllowDeletions = true
	res = f.run(t)
	assert.Equal(t, Result{Files: 40, Deleted: 11}, res)
}

func TestCollections_RemovedCollectionIsWithheld(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Calendar")
	f.src.add("c2", "Holidays")

	for i := range 50 {
		f.src.put("c2", fmt.Sprintf("h%02d", i), "x")
	}

	f.run(t)

	f.src.colls = f.src.colls[:1]

	res := f.run(t)
	assert.Equal(t, Result{Files: 50, Withheld: 50}, res)

	f.opts.AllowDeletions = true
	res = f.run(t)
	assert.Equal(t, Result{Deleted: 50}, res)
	assert.Len(t, f.asideFiles(t), 50)
}

// asideFiles lists the kept-aside files as folder/file paths.
func (f *collFixture) asideFiles(t *testing.T) []string {
	t.Helper()

	var paths []string

	err := filepath.WalkDir(f.aside, func(path string, d os.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == f.aside {
			return fs.SkipAll
		}

		if err != nil || d.IsDir() {
			return err
		}

		rel, relErr := filepath.Rel(f.aside, path)
		if relErr != nil {
			return fmt.Errorf("locate %s: %w", path, relErr)
		}

		paths = append(paths, filepath.ToSlash(rel))

		return nil
	})
	require.NoError(t, err)

	return paths
}

// Validates: PV-107
func TestCollections_ItemsMovedBetweenCollectionsAreNotDeletions(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("a", "A")
	f.src.add("b", "B")

	for i := range 60 {
		f.src.put("a", fmt.Sprintf("e%02d", i), "x")
	}

	f.run(t)

	// Providers keep an item's ID when it moves to another calendar.
	for i := range 15 {
		id := fmt.Sprintf("e%02d", i)
		f.src.del("a", id)
		f.src.put("b", id, "x")
	}

	res := f.run(t)
	assert.Equal(t, Result{Files: 60, Downloaded: 15, Deleted: 15}, res)
	assert.Empty(t, f.asideFiles(t))
}

func TestCollections_RenamedCollectionWithUnreadItemsKeepsItsOldDirectory(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Work")
	f.src.put("c1", "e1", "one")
	f.src.put("c1", "e2", "two")
	f.run(t)

	f.src.colls[0].Path = "Job"
	f.src.failItems = map[string]bool{"e2": true}

	res := f.run(t)
	assert.Equal(t, Result{Files: 3, Downloaded: 1, Failed: 1}, res)
	assert.Equal(t, "two", f.read(t, "Work/e2.ics"), "kept until it is read")
	assert.Empty(t, f.asideFiles(t))

	f.src.failItems = nil

	res = f.run(t)
	assert.Equal(t, Result{Files: 2, Downloaded: 1, Deleted: 2}, res)
	assert.Empty(t, f.asideFiles(t))
}

// Validates: PV-108
func TestCollections_DryRunCountsAndWritesNothing(t *testing.T) {
	f := newCollFixture(t)
	f.src.add("c1", "Work")
	f.src.add("c2", "Old")
	f.src.put("c1", "e1", "one")
	f.src.put("c1", "e2", "two")
	f.src.put("c2", "e3", "three")
	f.run(t)

	f.src.put("c1", "e1", "one, edited")
	f.src.del("c1", "e2")
	f.src.put("c1", "e4", "four")
	f.src.colls = f.src.colls[:1] // Old is gone

	res, next, err := runCollections(t.Context(), f.src, f.store, slog.New(slog.DiscardHandler), nil, Options{DryRun: true}, f.state)
	require.NoError(t, err)
	assert.Nil(t, next)
	assert.Equal(t, Result{Files: 3, Downloaded: 2, Deleted: 2}, res)
	assert.Equal(t, "one", f.read(t, "Work/e1.ics"), "nothing written")
	assert.Empty(t, f.asideFiles(t))

	res = f.run(t)
	assert.Equal(t, Result{Files: 2, Downloaded: 2, Deleted: 2}, res)
}
