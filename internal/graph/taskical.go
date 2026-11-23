package graph

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/icalfile"
)

// statusCompleted is the iCalendar status of a finished task.
const statusCompleted = "COMPLETED"

// EncodeTask renders a To Do task as one iCalendar file (D25) with
// standard properties only (D150). Its checklist items follow it as VTODOs
// related to it (D26). Due and start dates are dates: To Do has no time of
// day for them (F47).
func EncodeTask(t *Task) ([]byte, error) {
	root := icalfile.New()

	todo, err := taskComponent(t)
	if err != nil {
		return nil, err
	}

	root.Children = append(root.Children, todo)

	// Graph lists checklist items in their display order, the same on every
	// call; creation times are whole seconds and often equal (F47).
	for i := range t.ChecklistItems {
		item, itemErr := checklistComponent(&t.ChecklistItems[i], t)
		if itemErr != nil {
			return nil, itemErr
		}

		root.Children = append(root.Children, item)
	}

	data, err := icalfile.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("render task %s: %w", t.ID, err)
	}

	return data, nil
}

func taskComponent(t *Task) (*ical.Component, error) {
	comp := ical.NewComponent(ical.CompToDo)
	props := comp.Props

	icalfile.SetText(props, ical.PropUID, t.ID)
	icalfile.SetText(props, ical.PropSummary, t.Title)
	icalfile.SetText(props, ical.PropDescription, strings.TrimSpace(t.Body.Content))
	setCategories(props, t.Categories)

	setPriority(props, t.Importance)

	status := map[string]string{"completed": statusCompleted, "inProgress": "IN-PROCESS"}[t.Status]
	props.SetText(ical.PropStatus, cmp.Or(status, partNeedsAction))

	if err := setStamps(props, t.ID, t.CreatedDateTime, t.LastModifiedDateTime); err != nil {
		return nil, err
	}

	if err := taskTimes(comp, t); err != nil {
		return nil, err
	}

	return comp, nil
}

// taskTimes sets DUE, DTSTART, COMPLETED, the recurrence and the reminder.
func taskTimes(comp *ical.Component, t *Task) error {
	props := comp.Props

	for _, d := range []struct {
		name string
		dt   *DateTimeZone
	}{{ical.PropDue, t.DueDateTime}, {ical.PropDateTimeStart, t.StartDateTime}} {
		if d.dt == nil {
			continue
		}

		day, err := parseDateTimeZone(*d.dt)
		if err != nil {
			return fmt.Errorf("parse %s of task %s: %w", strings.ToLower(d.name), t.ID, err)
		}

		props.SetDate(d.name, time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))
	}

	if t.CompletedDateTime != nil {
		done, err := parseDateTimeZone(*t.CompletedDateTime)
		if err != nil {
			return fmt.Errorf("parse completion of task %s: %w", t.ID, err)
		}

		props.SetDateTime(ical.PropCompleted, done.UTC())
	}

	if t.Recurrence != nil {
		if err := taskRecurrence(props, t); err != nil {
			return err
		}
	}

	if t.IsReminderOn && t.ReminderDateTime != nil {
		at, err := parseDateTimeZone(*t.ReminderDateTime)
		if err != nil {
			return fmt.Errorf("parse reminder of task %s: %w", t.ID, err)
		}

		alarm := ical.NewComponent(ical.CompAlarm)
		trigger := ical.NewProp(ical.PropTrigger)
		trigger.SetDateTime(at.UTC())
		alarm.Props.Set(trigger)
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		icalfile.SetText(alarm.Props, ical.PropDescription, cmp.Or(t.Title, "Reminder"))
		comp.Children = append(comp.Children, alarm)
	}

	return nil
}

// taskRecurrence sets RRULE. RFC 5545 needs a DTSTART with it; without a
// start date the due date, then the range's start, is used. DTSTART then
// equals DUE, which RFC 5545 asks to be later; for a task due on a date,
// shifting either date would change what the user set, so they stay equal.
func taskRecurrence(props ical.Props, t *Task) error {
	rule, err := recurrenceRule(t.Recurrence, time.UTC, true)
	if err != nil {
		return fmt.Errorf("convert recurrence of task %s: %w", t.ID, err)
	}

	rrule := ical.NewProp(ical.PropRecurrenceRule)
	rrule.Value = rule
	props.Set(rrule)

	if props.Get(ical.PropDateTimeStart) != nil {
		return nil
	}

	if due := props.Get(ical.PropDue); due != nil {
		start := ical.NewProp(ical.PropDateTimeStart)
		start.Value = due.Value
		start.Params = due.Params
		props.Set(start)

		return nil
	}

	day, err := time.Parse(time.DateOnly, t.Recurrence.Range.StartDate)
	if err != nil {
		return fmt.Errorf("parse recurrence start of task %s: %w", t.ID, err)
	}

	props.SetDate(ical.PropDateTimeStart, day)

	return nil
}

// checklistComponent renders a checklist item as a VTODO related to its
// task.
func checklistComponent(item *ChecklistItem, t *Task) (*ical.Component, error) {
	comp := ical.NewComponent(ical.CompToDo)
	props := comp.Props

	icalfile.SetText(props, ical.PropUID, item.ID)
	icalfile.SetText(props, ical.PropSummary, item.DisplayName)

	related := ical.NewProp(ical.PropRelatedTo)
	related.Value = t.ID
	related.Params.Set(ical.ParamRelationshipType, "PARENT")
	props.Set(related)

	if err := setStamps(props, item.ID, item.CreatedDateTime, ""); err != nil {
		return nil, err
	}

	if !item.IsChecked {
		props.SetText(ical.PropStatus, partNeedsAction)

		return comp, nil
	}

	props.SetText(ical.PropStatus, statusCompleted)

	if item.CheckedDateTime != "" {
		checked, err := time.Parse(time.RFC3339, item.CheckedDateTime)
		if err != nil {
			return nil, fmt.Errorf("parse check time of checklist item %s: %w", item.ID, err)
		}

		props.SetDateTime(ical.PropCompleted, checked.UTC())
	}

	return comp, nil
}

// setStamps sets CREATED, LAST-MODIFIED and DTSTAMP (the last change, so
// the file only changes when the item does).
func setStamps(props ical.Props, id, created, modified string) error {
	stamp := time.Unix(0, 0).UTC()

	for _, f := range []struct{ name, value string }{
		{ical.PropCreated, created}, {ical.PropLastModified, modified},
	} {
		if f.value == "" {
			continue
		}

		t, err := time.Parse(time.RFC3339, f.value)
		if err != nil {
			return fmt.Errorf("parse %s of %s: %w", strings.ToLower(f.name), id, err)
		}

		props.SetDateTime(f.name, t.UTC())
		stamp = t.UTC()
	}

	props.SetDateTime(ical.PropDateTimeStamp, stamp)

	return nil
}

// setCategories sets CATEGORIES from a list of names.
func setCategories(props ical.Props, names []string) {
	if len(names) == 0 {
		return
	}

	categories := make([]string, len(names))
	for i, c := range names {
		categories[i] = icalfile.Text(c)
	}

	cats := ical.NewProp(ical.PropCategories)
	cats.SetTextList(categories)
	props.Set(cats)
}

// setPriority sets PRIORITY from Graph's importance; normal has none.
func setPriority(props ical.Props, importance string) {
	if p, ok := map[string]string{"high": "1", "low": "9"}[importance]; ok {
		icalfile.SetRaw(props, ical.PropPriority, p)
	}
}
