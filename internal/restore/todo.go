package restore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/graph"
)

// zoneUTC is how To Do writes UTC.
const zoneUTC = "UTC"

// OutlookTasks writes archived task lists into Microsoft To Do.
// tasks/Tasks/ goes to the default list, other directories to lists of the
// same name, created if missing (D152). Related tasks become checklist
// items (D26). A task is already there when the list has a task with its
// ID or one written from it by pimvault (D160).
type OutlookTasks struct {
	Client   *graph.Client
	Source   archive.Store
	Logger   *slog.Logger
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
}

// Run writes every archived task the account does not have yet.
func (o *OutlookTasks) Run(ctx context.Context) (Result, error) {
	byFolder, err := itemsByFolder(o.Source)
	if err != nil {
		return Result{}, err
	}

	lists, err := o.Client.TaskLists(ctx)
	if err != nil {
		return Result{}, err
	}

	var res Result

	folders := slices.Sorted(maps.Keys(byFolder))
	for i, folder := range folders {
		o.Progress.Report(i, len(folders))

		if err := o.restoreList(ctx, folder, byFolder[folder], lists, &res); err != nil {
			o.Logger.Error("restore task list", slog.String("list", folder), slog.String("error", err.Error()))
		}
	}

	o.Progress.Report(len(folders), len(folders))

	return res, nil
}

// findList returns the ID of the list an archive directory goes to, or "".
func findList(folder string, lists []graph.TaskList) string {
	for _, l := range lists {
		isDefault := l.WellknownListName == graph.DefaultTaskList
		if folder == archive.Tasks && isDefault || folder != archive.Tasks && !isDefault && l.DisplayName == listName(folder) {
			return l.ID
		}
	}

	return ""
}

// restoreList writes one list's tasks, creating the list when needed. When
// the list fails as a whole, its tasks not yet tried count as failed.
func (o *OutlookTasks) restoreList(ctx context.Context, folder string, ids []string, lists []graph.TaskList, res *Result) error {
	roots, failed := readTaskTrees(o.Source, folder, ids, o.Logger)
	res.Failed += failed

	listID := findList(folder, lists)

	existing, err := o.existingTasks(ctx, listID)
	if err != nil {
		res.Failed += len(roots)

		return err
	}

	for i, t := range roots {
		if ctxErr := ctx.Err(); ctxErr != nil {
			res.Failed += len(roots) - i

			return fmt.Errorf("restore tasks: %w", ctxErr)
		}

		item := path.Join(folder, t.file)

		if existing[t.uid] || o.DryRun {
			res.count(o.DryRun, o.Planned, item, existing[t.uid])

			continue
		}

		if listID == "" {
			if listID, err = o.Client.CreateTaskList(ctx, listName(folder)); err != nil {
				res.Failed += len(roots) - i

				return err
			}
		}

		if writeErr := o.write(ctx, listID, t); writeErr != nil {
			res.Failed++
			o.Logger.Error("restore task", slog.String("task", item), slog.String("error", writeErr.Error()))

			continue
		}

		res.Written++
	}

	return nil
}

// existingTasks returns the IDs of a list's tasks and the archive IDs they
// were written from (D160); none for no list.
func (o *OutlookTasks) existingTasks(ctx context.Context, listID string) (map[string]bool, error) {
	existing := map[string]bool{}
	if listID == "" {
		return existing, nil
	}

	tasks, err := o.Client.ExistingTasks(ctx, listID)
	if err != nil {
		return nil, err
	}

	for _, t := range tasks {
		existing[t.ID] = true
		if t.Source != "" {
			existing[t.Source] = true
		}
	}

	return existing, nil
}

func (o *OutlookTasks) write(ctx context.Context, listID string, t *taskTree) error {
	task := t.graphTask()
	if t.uid != "" {
		task.SetSource(t.uid)
	}

	id, err := o.Client.CreateTask(ctx, listID, task)
	if err != nil {
		return err
	}

	for _, item := range t.items {
		if err := o.Client.AddChecklistItem(ctx, listID, id, item.title, item.status == statusCompleted); err != nil {
			// Deleted again, so the next run writes it whole.
			return errors.Join(err, gone(o.Client.DeleteTask(ctx, listID, id)))
		}
	}

	return nil
}

// graphTask converts a task for To Do. A recurrence To Do cannot hold goes
// into the body (D68).
func (t *vtodo) graphTask() *graph.NewTask {
	task := &graph.NewTask{
		Title:      t.title,
		Status:     cmp.Or(map[string]string{statusCompleted: apiCompleted, "IN-PROCESS": "inProgress"}[t.status], "notStarted"),
		Importance: importance(t.priority),
		Categories: t.categories,
	}

	if !t.due.IsZero() {
		task.DueDateTime = &graph.DateTimeZone{DateTime: t.due.Format(time.DateOnly) + "T00:00:00", TimeZone: zoneUTC} // F47
	}

	if !t.completed.IsZero() {
		task.CompletedDateTime = utcDateTime(t.completed)
	}

	if !t.alarm.IsZero() {
		task.IsReminderOn, task.ReminderDateTime = true, utcDateTime(t.alarm)
	}

	body := t.description

	if t.rrule != "" {
		start := t.start
		if start.IsZero() {
			start = t.due
		}

		if rec, ok := graphRecurrence(t.rrule, start); ok && !start.IsZero() {
			task.Recurrence = rec
		} else {
			body = strings.TrimSpace(body + "\n\nRepeats: " + t.rrule)
		}
	}

	if body != "" {
		task.Body = graph.TextBody(body)
	}

	return task
}

// importance maps PRIORITY (1 highest, 9 lowest) to To Do's importance.
func importance(priority string) string {
	p, err := strconv.Atoi(priority)

	switch {
	case err != nil || p == 0:
		return ""
	case p < 5: //nolint:mnd // RFC 5545: 1–4 high, 5 medium, 6–9 low.
		return "high"
	case p > 5: //nolint:mnd // as above.
		return "low"
	default:
		return "normal"
	}
}

func utcDateTime(t time.Time) *graph.DateTimeZone {
	return &graph.DateTimeZone{DateTime: t.UTC().Format("2006-01-02T15:04:05"), TimeZone: zoneUTC}
}
