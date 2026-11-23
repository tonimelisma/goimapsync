package backup

import (
	"context"
	"log/slog"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gtasks"
	"github.com/tonimelisma/pimvault/internal/state"
)

// GoogleTasks backs up one Google account's task lists: one directory per
// list, one .ics file per task (D25, D29). Google Tasks has no change
// token, so every list is listed in full on every run (D146); files are
// only rewritten when their content changed.
type GoogleTasks struct {
	Client *gtasks.Client
	Store  archive.Store
	Logger *slog.Logger
	// Progress, if set, receives progress per collection.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// Run brings the tasks archive in line with the account.
func (g *GoogleTasks) Run(ctx context.Context, prev *state.Collections) (Result, *state.Collections, error) {
	return runCollections(ctx, &gtasksSource{g}, g.Store, g.Logger, g.Progress, g.Options, prev)
}

type gtasksSource struct {
	*GoogleTasks
}

// collections returns the default list as tasks/Tasks/ (D149) and the
// others by name.
func (s *gtasksSource) collections(ctx context.Context) ([]collection, error) {
	lists, defaultID, err := s.Client.Lists(ctx)
	if err != nil {
		return nil, err
	}

	paths := make(map[string]string, len(lists))

	for _, l := range lists {
		if l.ID == defaultID {
			paths[l.ID] = archive.Tasks
		} else {
			paths[l.ID] = archive.FolderPath([]string{l.Title}, []string{archive.Tasks})
		}
	}

	colls := make([]collection, 0, len(paths))
	for id, path := range archive.Unique(paths) {
		colls = append(colls, collection{ID: id, Path: path})
	}

	return colls, nil
}

func (s *gtasksSource) full(ctx context.Context, c collection) (listing, error) {
	tasks, err := s.Client.Tasks(ctx, c.ID)
	if err != nil {
		return listing{}, err
	}

	l := listing{items: make(map[string][]byte, len(tasks))}

	for i := range tasks {
		t := &tasks[i]

		data, err := gtasks.Encode(t)
		if err != nil {
			l.fail(t.ID)
			s.Logger.Error("back up task", slog.String("list", c.Path), slog.String("task", t.ID), slog.String("error", err.Error()))

			continue
		}

		l.items[t.ID] = data
	}

	return l, nil
}

// changes is never called: collections have no token, so each run lists
// them in full.
func (s *gtasksSource) changes(ctx context.Context, c collection, _ string) (listing, error) {
	return s.full(ctx, c)
}
