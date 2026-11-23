package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"time"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gtasks"
)

// GoogleTasks writes archived task lists into Google Tasks. tasks/Tasks/
// goes to the default list, other directories to lists of the same name,
// created if missing (D152). Related tasks (To Do checklist items, Google
// subtasks) become subtasks (D26). Due times, recurrences and reminders
// cannot be written and are dropped (D28). A task is already there when the
// list has a task with its ID, or with its title and due date (D160).
type GoogleTasks struct {
	Client   *gtasks.Client
	Source   archive.Store
	Logger   *slog.Logger
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
}

// Run writes every archived task the account does not have yet.
func (g *GoogleTasks) Run(ctx context.Context) (Result, error) {
	byFolder, err := itemsByFolder(g.Source)
	if err != nil {
		return Result{}, err
	}

	lists, defaultID, err := g.Client.Lists(ctx)
	if err != nil {
		return Result{}, err
	}

	byTitle := map[string]string{} // lists other than the default
	for _, l := range lists {
		if l.ID != defaultID {
			byTitle[l.Title] = l.ID
		}
	}

	var res Result

	folders := slices.Sorted(maps.Keys(byFolder))
	for i, folder := range folders {
		g.Progress.Report(i, len(folders))

		listID := defaultID
		if folder != archive.Tasks {
			listID = byTitle[listName(folder)]
		}

		if err := g.restoreList(ctx, folder, byFolder[folder], listID, &res); err != nil {
			g.Logger.Error("restore task list", slog.String("list", folder), slog.String("error", err.Error()))
		}
	}

	g.Progress.Report(len(folders), len(folders))

	return res, nil
}

// restoreList writes one list's tasks, creating the list when listID is
// empty. When the list fails as a whole, its tasks not yet tried count as
// failed.
func (g *GoogleTasks) restoreList(ctx context.Context, folder string, ids []string, listID string, res *Result) error {
	roots, failed := readTaskTrees(g.Source, folder, ids, g.Logger)
	res.Failed += failed

	existing := map[string]bool{}

	if listID != "" {
		tasks, err := g.Client.Tasks(ctx, listID)
		if err != nil {
			res.Failed += len(roots)

			return err
		}

		for i := range tasks {
			existing[tasks[i].ID] = true
			existing[titleDue(tasks[i].Title, tasks[i].Due)] = true
		}
	}

	for i, t := range roots {
		if err := ctx.Err(); err != nil {
			res.Failed += len(roots) - i

			return fmt.Errorf("restore tasks: %w", err)
		}

		item := path.Join(folder, t.file)

		present := existing[t.uid] || existing[titleDue(t.title, t.googleTask().Due)]
		if present || g.DryRun {
			res.count(g.DryRun, g.Planned, item, present)

			continue
		}

		if listID == "" {
			var err error
			if listID, err = g.Client.CreateList(ctx, listName(folder)); err != nil {
				res.Failed += len(roots) - i

				return err
			}
		}

		if err := g.write(ctx, listID, t); err != nil {
			res.Failed++
			g.Logger.Error("restore task", slog.String("task", item), slog.String("error", err.Error()))

			continue
		}

		res.Written++
	}

	return nil
}

// write creates a task with its subtasks in their order. When a subtask
// fails, the task is deleted again, so the next run writes it whole.
func (g *GoogleTasks) write(ctx context.Context, listID string, t *taskTree) error {
	id, err := g.Client.CreateTask(ctx, listID, "", "", t.googleTask())
	if err != nil {
		return err
	}

	previous := ""

	for _, item := range t.items {
		if previous, err = g.Client.CreateTask(ctx, listID, id, previous, item.googleTask()); err != nil {
			return errors.Join(err, gone(g.Client.DeleteTask(ctx, listID, id)))
		}
	}

	return nil
}

// titleDue is how a task is recognized in Google Tasks, which has no field
// for the archive ID it came from (D160): its title and due date.
func titleDue(title, due string) string {
	if len(due) >= len(time.DateOnly) {
		due = due[:len(time.DateOnly)]
	}

	return "\x00" + title + "\x00" + due
}

// googleTask converts a task for Google Tasks, which keeps only a due date
// (D28).
func (t *vtodo) googleTask() *gtasks.NewTask {
	task := &gtasks.NewTask{Title: t.title, Notes: t.description, Status: needsAction}

	if t.status == statusCompleted {
		task.Status = apiCompleted
		if !t.completed.IsZero() {
			task.Completed = t.completed.UTC().Format(time.RFC3339)
		}
	}

	if !t.due.IsZero() {
		task.Due = t.due.Format(time.RFC3339)
	}

	return task
}
