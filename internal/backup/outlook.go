package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/state"
)

// OutlookMail backs up one Outlook.com account's mail.
type OutlookMail struct {
	Client *graph.Client
	Store  archive.Store
	Logger *slog.Logger
	// Progress, if set, receives download progress.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// outlookRun is the working state of one Run.
type outlookRun struct {
	*OutlookMail
	mailSync
}

// Run brings the mail archive in line with the account, reading each
// folder's changes since its stored delta link, and returns the state to
// continue from next time. A folder is listed in full on the first backup,
// when its delta link expired, when it was renamed or moved, or when the
// archive changed outside pimvault. Failed items are counted, logged and
// kept for the next run. An error means the account could not be backed up.
func (o *OutlookMail) Run(ctx context.Context, prev *state.OutlookMail) (Result, *state.OutlookMail, error) {
	gone := func(err error) bool { return errors.Is(err, graph.ErrNotFound) }
	// Outlook.com's listed size is not the download's (F79): no size.
	r := &outlookRun{OutlookMail: o, mailSync: newMailSync(o.Store, o.Logger, o.Options, o.Progress,
		mailProvider{raw: o.Client.Raw, gone: gone})}

	next, err := r.run(ctx, prev)

	return r.res, next, err
}

func (r *outlookRun) run(ctx context.Context, prev *state.OutlookMail) (*state.OutlookMail, error) {
	if !r.Options.DryRun {
		if err := r.Store.CleanTemp(); err != nil {
			r.Logger.Warn(err.Error())
		}
	}

	folders, err := r.Client.MailFolders(ctx)
	if err != nil {
		return nil, err
	}

	have, err := r.Store.Scan()
	if err != nil {
		return nil, err
	}

	if reason := outlookFullReason(prev, r.Store.Root(), len(have)); reason != "" {
		r.Logger.Info("listing all mail", slog.String("reason", reason))

		prev = nil
	}

	want, fc, err := r.changes(ctx, prev, folders, have)
	if err != nil {
		return nil, err
	}

	if applyErr := r.apply(ctx, want, have, fc.refresh); applyErr != nil {
		return nil, applyErr
	}

	failed, err := r.finish()
	if err != nil {
		return nil, err
	}

	if r.Options.DryRun {
		r.res.Files = len(have)

		return nil, nil //nolint:nilnil // a dry run keeps the state as it was.
	}

	retry := make(map[string]string, len(failed))

	for _, id := range failed {
		if folder, known := want[id]; known {
			retry[id] = folder
		}
	}

	// Withheld deletions are looked at again next time (D182).
	for _, id := range r.withheldIDs {
		retry[id] = ""
	}

	return &state.OutlookMail{
		Folders: fc.positions,
		Archive: r.Store.Root(),
		Files:   r.res.Files,
		Retry:   retry,
	}, nil
}

// outlookFullReason says why prev cannot be continued from, or "" if it can.
func outlookFullReason(prev *state.OutlookMail, root string, files int) string {
	if prev == nil {
		return reasonFirstBackup
	}

	return driftReason(prev.Archive, prev.Files, root, files)
}

// folderChanges is what one run learned from the folders' deltas.
type folderChanges struct {
	// added maps messages to the folder they are now in; removed lists
	// messages that left a folder.
	added   map[string]string
	removed map[string]bool
	// refresh lists drafts whose content may have changed: an edited
	// Outlook draft keeps its ID.
	refresh map[string]bool
	// relisted holds directories whose whole content was listed: a file
	// there that no listing returned is gone.
	relisted map[string]bool
	// positions is each folder's new backup position.
	positions map[string]state.OutlookFolder
}

// changes reads every folder's changes and returns where messages belong
// (want: ID to folder, "" for gone) and what the folders reported.
func (r *outlookRun) changes(
	ctx context.Context, prev *state.OutlookMail, folders []graph.Folder, have map[string][]string,
) (map[string]string, folderChanges, error) {
	var old map[string]state.OutlookFolder
	if prev != nil {
		old = prev.Folders
	}

	fc, err := r.readFolders(ctx, folders, old)
	if err != nil {
		return nil, fc, err
	}

	want := make(map[string]string, len(fc.added)+len(fc.removed))

	for id := range fc.removed {
		want[id] = ""
	}

	for id, folder := range fc.added {
		want[id] = folder
	}

	if prev == nil {
		markUnlisted(want, have)
	} else {
		for id, held := range have {
			if _, decided := want[id]; !decided {
				r.dropRelisted(want, id, held, fc.relisted)
			}
		}

		for id, folder := range prev.Retry {
			if _, decided := want[id]; !decided {
				want[id] = folder
			}
		}
	}

	for id := range want {
		if !r.validID(id) {
			delete(want, id)
		}
	}

	return want, fc, nil
}

// dropRelisted handles a message no folder mentioned: copies in relisted
// directories are gone, while a copy in a directory followed by its delta
// stays.
func (r *outlookRun) dropRelisted(want map[string]string, id string, held []string, relisted map[string]bool) {
	if !slices.ContainsFunc(held, func(f string) bool { return relisted[f] }) {
		return
	}

	want[id] = ""

	for _, f := range held {
		if !relisted[f] {
			want[id] = f

			return
		}
	}
}

// readFolders reads each folder's delta, continuing from its old position
// when the folder is still at the same path. A folder whose changes cannot
// be read is counted as failed and keeps its old position; the other
// folders continue.
func (r *outlookRun) readFolders(
	ctx context.Context, folders []graph.Folder, old map[string]state.OutlookFolder,
) (folderChanges, error) {
	fc := folderChanges{
		added:     make(map[string]string),
		removed:   make(map[string]bool),
		refresh:   make(map[string]bool),
		relisted:  make(map[string]bool),
		positions: make(map[string]state.OutlookFolder, len(folders)),
	}

	for _, f := range folders {
		if err := ctx.Err(); err != nil {
			return fc, fmt.Errorf("back up mail: %w", err)
		}

		link := ""
		if p, ok := old[f.ID]; ok && p.Path == f.Path {
			link = p.DeltaLink
		}

		delta, err := r.Client.MessageDelta(ctx, f.ID, link)
		if errors.Is(err, graph.ErrDeltaExpired) {
			// A stored link, or a listing in progress, expired: list the
			// folder again from the start, once.
			r.Logger.Info("listing folder", slog.String("folder", f.Path), slog.String("reason", "change history expired"))

			link = ""
			delta, err = r.Client.MessageDelta(ctx, f.ID, "")
		}

		if err != nil {
			r.res.Failed++
			r.Logger.Error("back up folder", slog.String("folder", f.Path), slog.String("error", err.Error()))

			if p, ok := old[f.ID]; ok {
				fc.positions[f.ID] = p
			}

			continue
		}

		fc.record(f, delta, link == "", old)
	}

	// Folders gone since last time (deleted, or moved out of the backup)
	// lose their messages unless another folder listed them.
	for id, p := range old {
		if _, still := fc.positions[id]; !still && !slices.ContainsFunc(folders, func(f graph.Folder) bool { return f.ID == id }) {
			fc.relisted[p.Path] = true
		}
	}

	return fc, nil
}

// record adds one folder's delta.
func (fc *folderChanges) record(f graph.Folder, delta graph.Delta, full bool, old map[string]state.OutlookFolder) {
	if full {
		fc.relisted[f.Path] = true
		if p, ok := old[f.ID]; ok {
			fc.relisted[p.Path] = true
		}
	}

	for _, id := range delta.Added {
		fc.added[id] = f.Path
		if f.Path == archive.Drafts {
			fc.refresh[id] = true
		}
	}

	for _, id := range delta.Removed {
		fc.removed[id] = true
	}

	fc.positions[f.ID] = state.OutlookFolder{Path: f.Path, DeltaLink: delta.Link}
}
