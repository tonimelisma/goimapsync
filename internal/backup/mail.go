package backup

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/tonimelisma/pimvault/internal/archive"
)

// Result counts what one backup did.
type Result struct {
	// Files is the number of item files in the archive afterwards.
	Files      int
	Downloaded int
	Moved      int
	Deleted    int
	// Failed counts items that could not be checked or written; they are
	// retried on the next run.
	Failed int
	// Withheld counts items deleted in the account whose files were kept
	// because too many of their folder went at once (D182).
	Withheld int
	// Bytes is, in a dry run of a mail backup that can know it, the size of
	// the messages to download (D189); SizeKnown says whether it is known.
	Bytes     int64
	SizeKnown bool
}

// Options are a backup run's flags.
type Options struct {
	// AllowDeletions turns the mass-deletion guard off (D182).
	AllowDeletions bool
	// DryRun writes nothing and counts what the run would download, move
	// and delete instead (D183): no archive file, temp file or state.
	DryRun bool
}

// The mass-deletion guard (D182): a run removes none of a folder's items
// deleted in the account when they are more than a fifth of a folder
// holding at least guardMinItems, unless deletions are allowed.
const (
	guardMinItems = 50
	guardShare    = 5 // one in five
)

// guard reports whether the removal of removed of a folder's size items is
// held back, and warns once if so.
func guard(logger *slog.Logger, allow bool, folder string, removed, size int) bool {
	if allow || size < guardMinItems || removed*guardShare <= size {
		return false
	}

	logger.Warn("more than a fifth of a folder was deleted in the account; its files are kept. "+
		"Check the account, then run pimvault backup --allow-deletions to delete them from the archive",
		slog.String("folder", folder), slog.Int("deleted", removed), slog.Int("files", size))

	return true
}

// mailSync carries out a mail plan for any provider: it downloads, moves
// and deletes message files and counts the results.
type mailSync struct {
	store  archive.Store
	logger *slog.Logger
	opts   Options
	// raw, gone and size are the provider's, as in mailProvider.
	raw  func(ctx context.Context, id string) ([]byte, error)
	gone func(error) bool
	size func(ctx context.Context, id string) (int64, error)
	// progress counts downloads.
	progress Progress

	res    Result
	failed []string
	// withheldIDs are the messages whose deletion the guard held back
	// (D182): the next run looks at them again.
	withheldIDs []string
}

func newMailSync(store archive.Store, logger *slog.Logger, opts Options, progress Progress, provider mailProvider) mailSync {
	return mailSync{
		store: store, logger: logger, opts: opts, progress: progress,
		raw: provider.raw, gone: provider.gone, size: provider.size,
	}
}

// mailProvider is how a provider's messages are read: raw downloads one;
// gone reports errors meaning it no longer exists; size, when the provider
// can tell, gives a message's download size (F78).
type mailProvider struct {
	raw  func(ctx context.Context, id string) ([]byte, error)
	gone func(error) bool
	size func(ctx context.Context, id string) (int64, error)
}

// fail counts and logs a failed item and keeps it for the next run.
func (m *mailSync) fail(id string, err error) {
	m.res.Failed++
	m.failed = append(m.failed, id)
	m.logger.Error("back up message", slog.String("message", id), slog.String("error", err.Error()))
}

// validID reports whether id can name a file, failing the item if not.
func (m *mailSync) validID(id string) bool {
	if archive.ValidID(id) {
		return true
	}

	m.fail(id, fmt.Errorf("id %q is not usable as a file name", id))

	return false
}

// apply carries out the plan from want and have, continuing past failed
// items. It stops only when the run is canceled. The files of messages
// deleted in the account are kept aside (D181), or kept where they are when
// the guard holds their folder's deletions back (D182). A dry run counts
// the actions instead, and the bytes to download when the provider can
// tell (D183, D189).
func (m *mailSync) apply(ctx context.Context, want map[string]string, have map[string][]string, refresh map[string]bool) error {
	actions := Plan(want, have, refresh)
	withheld := m.withheld(actions, have)
	m.res.SizeKnown = m.opts.DryRun && m.size != nil

	total := 0
	for _, act := range actions {
		if act.Kind == Download {
			total++
		}
	}

	done := 0
	m.progress.Report(done, total)

	for _, act := range actions {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("back up mail: %w", err)
		}

		if err := m.do(ctx, act, withheld[act.From]); err != nil {
			m.fail(act.ID, err)
		}

		if act.Kind == Download {
			done++
			m.progress.Report(done, total)
		}
	}

	return nil
}

// do carries out one action, or in a dry run counts it. held says the guard
// holds back deletions from the action's folder.
func (m *mailSync) do(ctx context.Context, act Action, held bool) error {
	dry := m.opts.DryRun

	var err error

	switch act.Kind {
	case Download:
		if dry {
			m.sizeUp(ctx, act.ID)
		} else {
			err = m.download(ctx, act)
		}
	case Move:
		if !dry {
			err = m.store.Move(act.ID, act.From, act.To)
		}

		if err == nil {
			m.res.Moved++
		}
	case Delete:
		if held {
			m.withheldIDs = append(m.withheldIDs, act.ID)

			return nil
		}

		if !dry {
			err = m.store.Discard(act.From, act.ID)
		}

		if err == nil {
			m.res.Deleted++
		}
	case DropCopy:
		if !dry {
			err = m.store.Remove(act.From, act.ID)
		}

		if err == nil {
			m.res.Deleted++
		}
	}

	return err
}

// withheld returns the folders whose deletions the guard holds back
// (D182), counting their messages as withheld.
func (m *mailSync) withheld(actions []Action, have map[string][]string) map[string]bool {
	sizes := map[string]int{}

	for _, folders := range have {
		for _, f := range folders {
			sizes[f]++
		}
	}

	deleted := map[string]int{}

	for _, act := range actions {
		if act.Kind == Delete {
			deleted[act.From]++
		}
	}

	held := map[string]bool{}

	for _, folder := range slices.Sorted(maps.Keys(deleted)) {
		if guard(m.logger, m.opts.AllowDeletions, folder, deleted[folder], sizes[folder]) {
			held[folder] = true
			m.res.Withheld += deleted[folder]
		}
	}

	return held
}

// sizeUp counts a message a dry run would download and adds its size. A
// size that cannot be read makes the total unknown: it is never shown short.
func (m *mailSync) sizeUp(ctx context.Context, id string) {
	if m.size == nil {
		m.res.Downloaded++

		return
	}

	n, err := m.size(ctx, id)

	switch {
	case err != nil && m.gone(err):
		m.logger.Debug("message deleted while counting", slog.String("message", id))
	case err != nil:
		m.res.Downloaded++
		m.res.SizeKnown = false
		m.logger.Warn("read message size; the total is unknown", slog.String("message", id), slog.String("error", err.Error()))
	default:
		m.res.Downloaded++
		m.res.Bytes += n
	}
}

func (m *mailSync) download(ctx context.Context, act Action) error {
	data, err := m.raw(ctx, act.ID)
	if err != nil && m.gone(err) {
		m.logger.Debug("message deleted while backing up", slog.String("message", act.ID))

		return nil
	}

	if err != nil {
		return err
	}

	if err := m.store.Write(act.To, act.ID, data); err != nil {
		return err
	}

	m.res.Downloaded++

	return nil
}

// finish prunes empty folders, counts the files and returns the sorted IDs
// of failed items.
func (m *mailSync) finish() ([]string, error) {
	if m.opts.DryRun {
		return nil, nil
	}

	if err := m.store.PruneEmpty(); err != nil {
		m.logger.Warn(err.Error())
	}

	after, err := m.store.Scan()
	if err != nil {
		return nil, err
	}

	m.res.Files = len(after)
	slices.Sort(m.failed)

	return slices.Compact(m.failed), nil
}

// markUnlisted marks every archived item that a full listing did not return
// as gone.
func markUnlisted(want map[string]string, have map[string][]string) {
	for id := range have {
		if _, listed := want[id]; !listed {
			want[id] = ""
		}
	}
}

// driftReason says why a stored position cannot be continued from because
// of the archive, or "" if it can: the archive directory or its number of
// files changed outside pimvault since the last run.
func driftReason(prevRoot string, prevFiles int, root string, files int) string {
	switch {
	case prevRoot != root:
		return "archive directory changed"
	case prevFiles != files:
		return "archive files changed outside pimvault"
	default:
		return ""
	}
}

// reasonFirstBackup is logged when an account has no stored position.
const reasonFirstBackup = "first backup"
