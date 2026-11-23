package backup

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/state"
)

// tokenUnusableError is returned by a collection source when a stored change
// token can no longer be used; the collection is listed in full instead
// (D50). Its text is the reason, for the log.
type tokenUnusableError string

func (e tokenUnusableError) Error() string { return string(e) }

// errTokenExpired is the usual tokenUnusableError: the provider no longer
// accepts the token.
const errTokenExpired = tokenUnusableError("change history expired")

// collection is a calendar, address book or task list and its archive
// directory.
type collection struct {
	ID   string
	Path string
}

// listing is what a source read from one collection. Items are rendered file
// contents by ID; in a change listing a nil item was deleted. Failed holds
// items the source could not read or render; it has logged them, and their
// archived files are kept.
type listing struct {
	items  map[string][]byte
	token  string
	failed map[string]bool
}

// fail records an item the source could not read or render.
func (l *listing) fail(id string) {
	if l.failed == nil {
		l.failed = make(map[string]bool)
	}

	l.failed[id] = true
}

// collectionSource reads one kind of collection from a provider.
type collectionSource interface {
	// collections returns the collections to back up with their archive
	// directories.
	collections(ctx context.Context) ([]collection, error)
	// full lists every item of a collection and returns a token for later
	// changes, or "" if the provider has none.
	full(ctx context.Context, c collection) (listing, error)
	// changes lists the items changed since token. It returns
	// a tokenUnusableError error when token can no longer be used.
	changes(ctx context.Context, c collection, token string) (listing, error)
}

// collectionRun backs up the collections of one data type of one account.
type collectionRun struct {
	src    collectionSource
	store  archive.Store
	logger *slog.Logger
	// progress counts collections.
	progress Progress
	opts     Options
	res      Result
	// byFolder holds the item IDs the archive has in each directory.
	byFolder map[string]map[string]bool
}

// runCollections brings a store in line with the account's collections and
// returns the state to continue from next time. A collection that cannot be
// read is counted as failed and keeps its old position; the others continue.
func runCollections(
	ctx context.Context, src collectionSource, store archive.Store, logger *slog.Logger, progress Progress, opts Options,
	prev *state.Collections,
) (Result, *state.Collections, error) {
	r := &collectionRun{
		src: src, store: store, logger: logger, progress: progress, opts: opts,
		byFolder: make(map[string]map[string]bool),
	}

	next, err := r.run(ctx, prev)

	return r.res, next, err
}

func (r *collectionRun) run(ctx context.Context, prev *state.Collections) (*state.Collections, error) {
	if !r.opts.DryRun {
		if err := r.store.CleanTemp(); err != nil {
			r.logger.Warn(err.Error())
		}
	}

	colls, err := r.src.collections(ctx)
	if err != nil {
		return nil, err
	}

	have, err := r.store.Scan()
	if err != nil {
		return nil, err
	}

	files := 0

	for id, folders := range have {
		files += len(folders)

		for _, f := range folders {
			r.hold(f, id)
		}
	}

	old := map[string]state.Collection{}
	if prev != nil {
		reason := driftReason(prev.Archive, prev.Files, r.store.Root(), files)
		if reason == "" {
			old = prev.Collections
		} else {
			r.logger.Info("listing everything", slog.String("reason", reason))
		}
	}

	colls = slices.Clone(colls)
	slices.SortFunc(colls, func(a, b collection) int { return cmp.Compare(a.Path, b.Path) })

	positions, err := r.syncAll(ctx, colls, old)
	if err != nil {
		return nil, err
	}

	if r.opts.DryRun {
		r.res.Files = files

		return nil, nil //nolint:nilnil // a dry run keeps the state as it was.
	}

	if pruneErr := r.store.PruneEmpty(); pruneErr != nil {
		r.logger.Warn(pruneErr.Error())
	}

	after, err := r.store.Scan()
	if err != nil {
		return nil, err
	}

	r.res.Files = 0
	for _, folders := range after {
		r.res.Files += len(folders)
	}

	return &state.Collections{Collections: positions, Archive: r.store.Root(), Files: r.res.Files}, nil
}

// syncAll backs up each collection, then settles the deletions: those the
// listings reported and the files in directories no collection uses any
// more (collections that are gone or were renamed). It returns the new
// positions.
func (r *collectionRun) syncAll(
	ctx context.Context, colls []collection, old map[string]state.Collection,
) (map[string]state.Collection, error) {
	positions := make(map[string]state.Collection, len(colls))
	inUse := make(map[string]bool, len(colls))

	sizes := make(map[string]int, len(r.byFolder))
	for folder, ids := range r.byFolder {
		sizes[folder] = len(ids)
	}

	var deletions []deletion

	for i, c := range colls {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("back up collections: %w", err)
		}

		r.progress.Report(i, len(colls))

		inUse[c.Path] = true

		// On failure the old position is kept, and so are the files in its
		// directory.
		synced := r.syncCollection(ctx, c, old)
		if synced.ok {
			positions[c.ID] = synced.pos
			inUse[synced.pos.Path] = true
		}

		if synced.keep != "" {
			inUse[synced.keep] = true
		}

		if len(synced.deleted.ids) > 0 {
			deletions = append(deletions, synced.deleted)
		}
	}

	r.progress.Report(len(colls), len(colls))

	for _, folder := range slices.Sorted(maps.Keys(r.byFolder)) {
		if !inUse[folder] && len(r.byFolder[folder]) > 0 {
			deletions = append(deletions, deletion{folder: folder, ids: slices.Sorted(maps.Keys(r.byFolder[folder]))})
		}
	}

	r.settle(deletions, sizes, positions)

	return positions, nil
}

// dropCopies deletes the files of a deletion whose items the archive holds
// in another directory, and returns the items it holds nowhere else and the
// number of files that could not be deleted.
func (r *collectionRun) dropCopies(d deletion, holders map[string]int) (gone []string, failed int) {
	for _, id := range d.ids {
		if holders[id] < 2 {
			gone = append(gone, id)

			continue
		}

		if !r.opts.DryRun {
			if err := r.store.Remove(d.folder, id); err != nil {
				failed++
				r.fail(d.folder, id, err)

				continue
			}

			delete(r.byFolder[d.folder], id)
		}

		holders[id]--
		r.res.Deleted++
	}

	return gone, failed
}

// deletion is the files of one directory whose items no listing has any
// more, settled once every collection is read.
type deletion struct {
	folder string
	ids    []string
	// collID is the collection whose listing reported them, "" for a
	// directory no collection uses; revert is the position token it keeps
	// when the deletions are withheld or fail.
	collID, revert string
}

// syncResult is what backing up one collection gave.
type syncResult struct {
	pos state.Collection
	// ok is false when the collection has no position to keep.
	ok bool
	// keep is a directory to leave alone: the old one of a renamed
	// collection whose items were not all read.
	keep    string
	deleted deletion
}

// syncCollection backs up one collection's items and returns its new
// position and the deletions it found.
func (r *collectionRun) syncCollection(ctx context.Context, c collection, old map[string]state.Collection) syncResult {
	pos, known := old[c.ID]
	token := ""

	if known && pos.Path == c.Path {
		token = pos.Token
	}

	full := token == ""

	var (
		l   listing
		err error
	)

	if !full {
		l, err = r.src.changes(ctx, c, token)

		var unusable tokenUnusableError
		if errors.As(err, &unusable) {
			r.logger.Info("listing collection", slog.String("collection", c.Path), slog.String("reason", string(unusable)))

			full = true
		}
	}

	if full {
		l, err = r.src.full(ctx, c)
	}

	if err != nil {
		r.res.Failed++
		r.logger.Error("back up collection", slog.String("collection", c.Path), slog.String("error", err.Error()))

		return syncResult{pos: pos, ok: known}
	}

	r.res.Failed += len(l.failed)
	writeFailed, deleted := r.write(c.Path, l, full)
	failed := len(l.failed) + writeFailed

	// Read the same changes again next time when this run cannot finish
	// them; a full listing is repeated in full.
	revert := token
	if full {
		revert = ""
	}

	res := syncResult{
		pos: state.Collection{Path: c.Path, Token: l.token}, ok: true,
		deleted: deletion{folder: c.Path, ids: deleted, collID: c.ID, revert: revert},
	}

	if failed > 0 {
		res.pos.Token = revert

		if known && pos.Path != c.Path {
			// Items not read are still in the old directory.
			res.keep = pos.Path
		}
	}

	return res
}

// write brings one directory in line with a listing, except for deletions,
// and returns the number of items that failed and the items deleted: those
// the listing says were deleted and, after a full listing, those it does
// not list, except items the source failed to read.
func (r *collectionRun) write(folder string, l listing, full bool) (failed int, deleted []string) {
	deleted = r.deletedIn(folder, l, full)

	for _, id := range slices.Sorted(maps.Keys(l.items)) {
		var err error

		switch {
		case !archive.ValidID(id):
			err = errors.New("id is not usable as a file name")
		case l.items[id] != nil:
			err = r.put(folder, id, l.items[id])
		}

		if err != nil {
			failed++
			r.fail(folder, id, err)
		}
	}

	return failed, deleted
}

// deletedIn returns the items the archive holds in folder that a listing
// says were deleted, and after a full listing those it does not list,
// except items the source failed to read.
func (r *collectionRun) deletedIn(folder string, l listing, full bool) []string {
	var deleted []string

	for _, id := range slices.Sorted(maps.Keys(r.byFolder[folder])) {
		item, listed := l.items[id]

		if listed && item == nil || full && !listed && !l.failed[id] {
			deleted = append(deleted, id)
		}
	}

	return deleted
}

// settle carries out the deletions. A file whose item the archive now holds
// in another directory moved there (a collection renamed or an item moved)
// and is deleted. The others were deleted in the account and are kept
// aside (D181), unless more than a fifth of their directory went at once
// (D182): then they stay, and their collection reads the same changes
// again next time, as it does when one fails. sizes are the directories'
// file counts before the run.
func (r *collectionRun) settle(deletions []deletion, sizes map[string]int, positions map[string]state.Collection) {
	holders := map[string]int{}

	for _, ids := range r.byFolder {
		for id := range ids {
			holders[id]++
		}
	}

	for _, d := range deletions {
		gone, failed := r.dropCopies(d, holders)

		withheld := guard(r.logger, r.opts.AllowDeletions, d.folder, len(gone), sizes[d.folder])
		if withheld {
			r.res.Withheld += len(gone)
			gone = nil
		}

		if r.opts.DryRun {
			r.res.Deleted += len(gone)
			gone = nil
		}

		for _, id := range gone {
			if err := r.store.Discard(d.folder, id); err != nil {
				failed++
				r.fail(d.folder, id, err)

				continue
			}

			holders[id]--
			delete(r.byFolder[d.folder], id)
			r.res.Deleted++
		}

		if pos, ok := positions[d.collID]; ok && d.collID != "" && (withheld || failed > 0) {
			pos.Token = d.revert
			positions[d.collID] = pos
		}
	}
}

// put writes an item unless the archive already has the same contents; a
// dry run only counts it.
func (r *collectionRun) put(folder, id string, data []byte) error {
	if r.byFolder[folder][id] {
		if current, err := r.store.Read(folder, id); err == nil && bytes.Equal(current, data) {
			return nil
		}
	}

	if !r.opts.DryRun {
		if err := r.store.Write(folder, id, data); err != nil {
			return err
		}
	}

	r.hold(folder, id)
	r.res.Downloaded++

	return nil
}

// hold records that the archive has an item in a directory.
func (r *collectionRun) hold(folder, id string) {
	if r.byFolder[folder] == nil {
		r.byFolder[folder] = make(map[string]bool)
	}

	r.byFolder[folder][id] = true
}

// fail counts and logs an item that could not be written or removed.
func (r *collectionRun) fail(folder, id string, err error) {
	r.res.Failed++
	r.logger.Error("back up item", slog.String("collection", folder), slog.String("item", id), slog.String("error", err.Error()))
}
