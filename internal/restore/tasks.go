package restore

import (
	"bytes"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/archive"
)

const (
	// statusCompleted is the iCalendar status of a finished task.
	statusCompleted = "COMPLETED"
	// apiCompleted is the status of a finished task in both providers'
	// APIs.
	apiCompleted = "completed"
)

// vtodo is an archived task as read from its VTODO, for either provider.
type vtodo struct {
	uid, parent, title, description, status, priority, rrule string
	categories                                               []string
	due, start, completed, alarm                             time.Time
}

// taskTree is a top-level task with the tasks related to it, which become
// checklist items in To Do and subtasks in Google Tasks (D26).
type taskTree struct {
	vtodo

	file  string
	items []*taskTree
}

// itemsByFolder groups an archive's files by directory.
func itemsByFolder(store archive.Store) (map[string][]string, error) {
	have, err := store.Scan()
	if err != nil {
		return nil, err
	}

	byFolder := map[string][]string{}

	for id, folders := range have {
		for _, f := range folders {
			byFolder[f] = append(byFolder[f], id)
		}
	}

	for f := range maps.Keys(byFolder) {
		slices.Sort(byFolder[f])
	}

	return byFolder, nil
}

// readTaskTrees parses a list's files and returns its top-level tasks, each
// with the tasks related to it, sorted by file, and the number of files that
// could not be read. A task whose parent is in another file of the list (a
// Google subtask) or in the same file (a To Do checklist item) is attached
// to its top-level ancestor.
func readTaskTrees(store archive.Store, folder string, ids []string, logger *slog.Logger) ([]*taskTree, int) {
	var (
		all    []*taskTree
		failed int
	)

	for _, id := range ids {
		raw, err := store.Read(folder, id)
		if err == nil {
			var todos []vtodo

			todos, err = parseTaskFile(raw)
			for i := range todos {
				all = append(all, &taskTree{vtodo: todos[i], file: id})
			}
		}

		if err != nil {
			failed++
			logger.Error("read task", slog.String("task", path.Join(folder, id)), slog.String("error", err.Error()))
		}
	}

	byUID := make(map[string]*taskTree, len(all))
	for _, t := range all {
		byUID[t.uid] = t
	}

	var roots []*taskTree

	for _, t := range all {
		root := t
		for steps := 0; root.parent != "" && byUID[root.parent] != nil && steps < len(all); steps++ {
			root = byUID[root.parent]
		}

		if root == t {
			roots = append(roots, t)
		} else {
			root.items = append(root.items, t)
		}
	}

	return roots, failed
}

// parseTaskFile reads the VTODOs of one archived task file.
func parseTaskFile(raw []byte) ([]vtodo, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("parse task: %w", err)
	}

	var out []vtodo

	for _, comp := range cal.Children {
		if comp.Name != ical.CompToDo {
			continue
		}

		t, err := parseVTODO(comp)
		if err != nil {
			return nil, err
		}

		out = append(out, t)
	}

	return out, nil
}

func parseVTODO(comp *ical.Component) (vtodo, error) {
	props := comp.Props
	t := vtodo{
		uid:      propValue(props, ical.PropUID),
		parent:   propValue(props, ical.PropRelatedTo),
		status:   propValue(props, ical.PropStatus),
		priority: propValue(props, ical.PropPriority),
		rrule:    propValue(props, ical.PropRecurrenceRule),
		alarm:    alarmTime(comp),
	}

	t.title, _ = props.Text(ical.PropSummary)           //nolint:errcheck // a malformed title reads as empty.
	t.description, _ = props.Text(ical.PropDescription) //nolint:errcheck // as above.

	if cats := props.Get(ical.PropCategories); cats != nil {
		t.categories, _ = cats.TextList() //nolint:errcheck // malformed categories read as none.
	}

	var err error

	if t.due, err = taskDate(props, ical.PropDue); err != nil {
		return vtodo{}, fmt.Errorf("convert task %s: %w", t.uid, err)
	}

	if t.start, err = taskDate(props, ical.PropDateTimeStart); err != nil {
		return vtodo{}, fmt.Errorf("convert task %s: %w", t.uid, err)
	}

	if done := props.Get(ical.PropCompleted); done != nil {
		if t.completed, err = done.DateTime(time.UTC); err != nil {
			return vtodo{}, fmt.Errorf("parse completion of task %s: %w", t.uid, err)
		}
	}

	return t, nil
}

// taskDate reads a date property as a calendar date, or zero.
func taskDate(props ical.Props, name string) (time.Time, error) {
	prop := props.Get(name)
	if prop == nil {
		return time.Time{}, nil
	}

	t, err := prop.DateTime(time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s: %w", strings.ToLower(name), err)
	}

	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
}

// alarmTime returns the absolute trigger of a VTODO's first alarm, or zero.
func alarmTime(comp *ical.Component) time.Time {
	for _, child := range comp.Children {
		if child.Name != ical.CompAlarm {
			continue
		}

		if trigger := child.Props.Get(ical.PropTrigger); trigger != nil && trigger.ValueType() == ical.ValueDateTime {
			if at, err := trigger.DateTime(time.UTC); err == nil {
				return at
			}
		}
	}

	return time.Time{}
}

func propValue(props ical.Props, name string) string {
	if p := props.Get(name); p != nil {
		return p.Value
	}

	return ""
}

// listName is the task list name for an archive directory.
func listName(folder string) string {
	return strings.Join(archive.FolderSegments(folder), "/")
}
