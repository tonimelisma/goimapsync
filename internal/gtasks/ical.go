package gtasks

import (
	"fmt"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/icalfile"
)

// Encode renders a task as one iCalendar file (D25) with standard
// properties only (D150). A subtask is related to its parent task (D26).
// Google keeps only the date of a due time.
func Encode(t *Task) ([]byte, error) {
	root := icalfile.New()
	todo := ical.NewComponent(ical.CompToDo)
	props := todo.Props

	icalfile.SetText(props, ical.PropUID, t.ID)
	icalfile.SetText(props, ical.PropSummary, t.Title)
	icalfile.SetText(props, ical.PropDescription, t.Notes)

	status := "NEEDS-ACTION"
	if t.Status == "completed" {
		status = "COMPLETED"
	}

	props.SetText(ical.PropStatus, status)

	if t.Parent != "" {
		related := ical.NewProp(ical.PropRelatedTo)
		related.Value = t.Parent
		related.Params.Set(ical.ParamRelationshipType, "PARENT")
		props.Set(related)
	}

	stamp := time.Unix(0, 0).UTC()

	for _, f := range []struct {
		name, value string
		date        bool
	}{
		{ical.PropDue, t.Due, true},
		{ical.PropCompleted, t.Completed, false},
		{ical.PropLastModified, t.Updated, false},
	} {
		if f.value == "" {
			continue
		}

		v, err := time.Parse(time.RFC3339, f.value)
		if err != nil {
			return nil, fmt.Errorf("parse %s of task %s: %w", f.name, t.ID, err)
		}

		v = v.UTC()

		switch {
		case f.date:
			props.SetDate(f.name, time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, time.UTC))
		default:
			props.SetDateTime(f.name, v)
		}

		if f.name == ical.PropLastModified {
			stamp = v
		}
	}

	// DTSTAMP is the last change, so the file only changes when the task
	// does.
	props.SetDateTime(ical.PropDateTimeStamp, stamp)

	root.Children = append(root.Children, todo)

	data, err := icalfile.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("render task %s: %w", t.ID, err)
	}

	return data, nil
}
