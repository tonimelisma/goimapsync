package backup

import (
	"context"
	"log/slog"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/state"
)

// OutlookTasks backs up one account's Microsoft To Do lists: one directory
// per list, one .ics file per task with its checklist (D25, D29).
type OutlookTasks struct {
	Client *graph.Client
	Store  archive.Store
	Logger *slog.Logger
	// Progress, if set, receives progress per collection.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// Run brings the tasks archive in line with the account, continuing from
// each list's delta link where it can (D146).
func (o *OutlookTasks) Run(ctx context.Context, prev *state.Collections) (Result, *state.Collections, error) {
	return runCollections(ctx, &outlookTaskSource{o}, o.Store, o.Logger, o.Progress, o.Options, prev)
}

type outlookTaskSource struct {
	*OutlookTasks
}

// collections returns the default list as tasks/Tasks/ (D149) and the other
// lists the account owns; lists shared with it (D20) and the flagged email
// list (D148) are skipped.
func (s *outlookTaskSource) collections(ctx context.Context) ([]collection, error) {
	lists, err := s.Client.TaskLists(ctx)
	if err != nil {
		return nil, err
	}

	paths := make(map[string]string, len(lists))

	for _, l := range lists {
		switch {
		case l.WellknownListName == graph.DefaultTaskList:
			paths[l.ID] = archive.Tasks
		case l.WellknownListName == graph.FlaggedEmailList || !l.IsOwner:
			continue
		default:
			paths[l.ID] = archive.FolderPath([]string{l.DisplayName}, []string{archive.Tasks})
		}
	}

	colls := make([]collection, 0, len(paths))
	for id, path := range archive.Unique(paths) {
		colls = append(colls, collection{ID: id, Path: path})
	}

	return colls, nil
}

func (s *outlookTaskSource) full(ctx context.Context, c collection) (listing, error) {
	return s.changes(ctx, c, "")
}

func (s *outlookTaskSource) changes(ctx context.Context, c collection, token string) (listing, error) {
	changes, err := s.Client.TaskDelta(ctx, c.ID, token)

	return graphListing(changes, err, s.Logger, c, "task", func(t *graph.Task) string { return t.ID }, graph.EncodeTask)
}
