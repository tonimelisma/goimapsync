package gcal

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/icalfile"
)

const (
	statusCancelled = "cancelled"
	dateLayout      = "2006-01-02"
	partTentative   = "TENTATIVE"
	tentative       = "tentative"
)

// Encode renders an event, or a recurring event with its changed and
// canceled instances, as one iCalendar file (D43) using standard
// properties only (D150). Canceled instances become EXDATEs. It returns nil
// when nothing is left to store: the event was deleted, or it is a date
// Google Calendar shows from the contacts (D148). owner is the account's
// email address, used for email reminders.
func Encode(events []Event, cal *Calendar, owner string) ([]byte, error) {
	var (
		master     *Event
		exceptions []Event
	)

	for i := range events {
		if events[i].RecurringEventID == "" {
			master = &events[i]
		} else {
			exceptions = append(exceptions, events[i])
		}
	}

	if master != nil && (master.Status == statusCancelled || fromContacts(master)) {
		return nil, nil
	}

	slices.SortFunc(exceptions, func(a, b Event) int {
		return cmp.Compare(originalStart(&a), originalStart(&b))
	})

	root := icalfile.New()

	r := renderer{cal: cal, owner: owner}

	masterTZ := ""

	if master != nil {
		masterTZ = master.Start.TimeZone

		comp, err := r.master(master, exceptions)
		if err != nil {
			return nil, err
		}

		root.Children = append(root.Children, comp)
	}

	for i := range exceptions {
		e := &exceptions[i]
		if e.Status == statusCancelled || fromContacts(e) {
			continue
		}

		comp, err := r.exception(e, masterTZ)
		if err != nil {
			return nil, err
		}

		root.Children = append(root.Children, comp)
	}

	if len(root.Children) == 0 {
		return nil, nil
	}

	data, err := icalfile.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("render event %s: %w", events[0].SeriesID(), err)
	}

	return data, nil
}

// renderer renders the events of one calendar.
type renderer struct {
	cal   *Calendar
	owner string
}

// master renders a recurring or single event, with an EXDATE for each
// canceled instance.
func (r renderer) master(master *Event, exceptions []Event) (*ical.Component, error) {
	comp, err := r.event(master)
	if err != nil {
		return nil, err
	}

	for i := range exceptions {
		if exceptions[i].Status != statusCancelled {
			continue
		}

		exdate := ical.NewProp(ical.PropExceptionDates)
		if err := r.setTime(exdate, exceptions[i].OriginalStartTime, master.Start.TimeZone); err != nil {
			return nil, err
		}

		comp.Props.Add(exdate)
	}

	return comp, nil
}

// exception renders a changed instance with its RECURRENCE-ID.
func (r renderer) exception(e *Event, masterTZ string) (*ical.Component, error) {
	comp, err := r.event(e)
	if err != nil {
		return nil, err
	}

	recurrenceID := ical.NewProp(ical.PropRecurrenceID)
	if err := r.setTime(recurrenceID, e.OriginalStartTime, masterTZ); err != nil {
		return nil, err
	}

	comp.Props.Set(recurrenceID)

	return comp, nil
}

// fromContacts reports whether an event is a birthday or other date that
// Google Calendar shows from a contact.
func fromContacts(e *Event) bool {
	return e.BirthdayProperties.Contact != ""
}

func originalStart(e *Event) string {
	return cmp.Or(e.OriginalStartTime.DateTime, e.OriginalStartTime.Date)
}

// event renders one event's own properties as a VEVENT.
func (r renderer) event(e *Event) (*ical.Component, error) {
	comp := ical.NewComponent(ical.CompEvent)
	props := comp.Props

	icalfile.SetText(props, ical.PropUID, cmp.Or(e.ICalUID, e.ID+"@google.com"))

	stamp, err := r.stamp(e)
	if err != nil {
		return nil, err
	}

	props.SetDateTime(ical.PropDateTimeStamp, stamp)

	for name, value := range map[string]string{ical.PropCreated: e.Created, ical.PropLastModified: e.Updated} {
		if value == "" {
			continue
		}

		t, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, fmt.Errorf("parse %s of event %s: %w", strings.ToLower(name), e.ID, err)
		}

		props.SetDateTime(name, t.UTC())
	}

	if err := r.times(props, e); err != nil {
		return nil, err
	}

	icalfile.SetText(props, ical.PropSummary, e.Summary)
	icalfile.SetText(props, ical.PropDescription, e.Description)
	icalfile.SetText(props, ical.PropLocation, e.Location)

	if e.Sequence > 0 {
		icalfile.SetRaw(props, ical.PropSequence, fmt.Sprint(e.Sequence))
	}

	icalfile.SetText(props, ical.PropStatus, map[string]string{"confirmed": "CONFIRMED", tentative: partTentative}[e.Status])
	icalfile.SetText(props, ical.PropTransparency, map[string]string{"transparent": "TRANSPARENT"}[e.Transparency])
	icalfile.SetText(props, ical.PropClass, map[string]string{
		"public": "PUBLIC", "private": "PRIVATE", "confidential": "CONFIDENTIAL",
	}[e.Visibility])

	for _, line := range e.Recurrence {
		prop, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("parse recurrence of event %s: %w", e.ID, err)
		}

		props.Add(prop)
	}

	r.people(props, e)
	r.links(props, e)
	r.alarms(comp, e)

	return comp, nil
}

// stamp is the event's DTSTAMP: its last change, so the file only changes
// when the event does.
func (r renderer) stamp(e *Event) (time.Time, error) {
	value := cmp.Or(e.Updated, e.Created)
	if value == "" {
		return time.Unix(0, 0).UTC(), nil
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse update time of event %s: %w", e.ID, err)
	}

	return t.UTC(), nil
}

func (r renderer) times(props ical.Props, e *Event) error {
	for name, dt := range map[string]DateTime{ical.PropDateTimeStart: e.Start, ical.PropDateTimeEnd: e.End} {
		if dt.Date == "" && dt.DateTime == "" {
			continue
		}

		prop := ical.NewProp(name)
		if err := r.setTime(prop, dt, ""); err != nil {
			return fmt.Errorf("event %s: %w", e.ID, err)
		}

		props.Set(prop)
	}

	return nil
}

// setTime sets a date, or a date-time in its time zone (the event's, else
// fallbackTZ, else the calendar's), named by its IANA TZID; Encode adds the
// VTIMEZONE components. An unknown zone falls back to UTC.
func (r renderer) setTime(prop *ical.Prop, dt DateTime, fallbackTZ string) error {
	if dt.Date != "" {
		t, err := time.Parse(dateLayout, dt.Date)
		if err != nil {
			return fmt.Errorf("parse date %q: %w", dt.Date, err)
		}

		prop.SetDate(t)

		return nil
	}

	t, err := time.Parse(time.RFC3339, dt.DateTime)
	if err != nil {
		return fmt.Errorf("parse time %q: %w", dt.DateTime, err)
	}

	loc := time.UTC
	if tz := cmp.Or(dt.TimeZone, fallbackTZ, r.cal.TimeZone); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}

	prop.SetDateTime(t.In(loc))

	return nil
}

func (r renderer) people(props ical.Props, e *Event) {
	if e.Organizer.Email != "" {
		org := ical.NewProp(ical.PropOrganizer)
		org.Value = "mailto:" + e.Organizer.Email
		icalfile.SetParam(org, ical.ParamCommonName, e.Organizer.DisplayName)
		props.Set(org)
	}

	for _, a := range e.Attendees {
		if a.Email == "" {
			continue
		}

		att := ical.NewProp(ical.PropAttendee)
		att.Value = "mailto:" + a.Email
		icalfile.SetParam(att, ical.ParamCommonName, a.DisplayName)
		icalfile.SetParam(att, ical.ParamParticipationStatus, map[string]string{
			"needsAction": "NEEDS-ACTION", "declined": "DECLINED", tentative: partTentative, "accepted": "ACCEPTED",
		}[a.ResponseStatus])

		role := "REQ-PARTICIPANT"
		if a.Optional {
			role = "OPT-PARTICIPANT"
		}

		icalfile.SetParam(att, ical.ParamRole, role)

		if a.Resource {
			icalfile.SetParam(att, ical.ParamCalendarUserType, "RESOURCE")
		}

		props.Add(att)
	}
}

// links adds meeting links (RFC 7986 CONFERENCE) and attachment links.
func (r renderer) links(props ical.Props, e *Event) {
	features := map[string]string{"video": "VIDEO", "phone": "PHONE"}

	for _, ep := range e.ConferenceData.EntryPoints {
		if ep.URI == "" {
			continue
		}

		conf := ical.NewProp("CONFERENCE")
		conf.Value = ep.URI
		conf.Params.Set(ical.ParamValue, string(ical.ValueURI))
		icalfile.SetParam(conf, "FEATURE", features[ep.EntryPointType])
		icalfile.SetParam(conf, "LABEL", ep.Label)
		props.Add(conf)
	}

	if len(e.ConferenceData.EntryPoints) == 0 && e.HangoutLink != "" {
		conf := ical.NewProp("CONFERENCE")
		conf.Value = e.HangoutLink
		conf.Params.Set(ical.ParamValue, string(ical.ValueURI))
		conf.Params.Set("FEATURE", "VIDEO")
		props.Add(conf)
	}

	for _, a := range e.Attachments {
		if a.FileURL == "" {
			continue
		}

		attach := ical.NewProp(ical.PropAttach)
		attach.Value = a.FileURL
		icalfile.SetParam(attach, ical.ParamFormatType, a.MimeType)
		props.Add(attach)
	}
}

// alarms adds the event's reminders, or the calendar's default reminders
// when the event uses them.
func (r renderer) alarms(comp *ical.Component, e *Event) {
	reminders := e.Reminders.Overrides
	if e.Reminders.UseDefault {
		reminders = r.cal.DefaultReminders
	}

	for _, rem := range reminders {
		alarm := ical.NewComponent(ical.CompAlarm)
		icalfile.SetRaw(alarm.Props, ical.PropTrigger, fmt.Sprintf("-PT%dM", rem.Minutes))
		icalfile.SetText(alarm.Props, ical.PropDescription, cmp.Or(e.Summary, "Reminder"))

		if rem.Method == "email" && r.owner != "" {
			alarm.Props.SetText(ical.PropAction, "EMAIL")
			icalfile.SetText(alarm.Props, ical.PropSummary, cmp.Or(e.Summary, "Reminder"))

			attendee := ical.NewProp(ical.PropAttendee)
			attendee.Value = "mailto:" + r.owner
			alarm.Props.Set(attendee)
		} else {
			alarm.Props.SetText(ical.PropAction, "DISPLAY")
		}

		comp.Children = append(comp.Children, alarm)
	}
}

// parseLine parses one content line from Google's recurrence list, such as
// "RRULE:FREQ=WEEKLY" or "EXDATE;TZID=Europe/Helsinki:20240101T100000".
func parseLine(line string) (*ical.Prop, error) {
	head, value, ok := strings.Cut(line, ":")
	if !ok {
		return nil, fmt.Errorf("parse %q: no value", line)
	}

	parts := strings.Split(head, ";")
	prop := ical.NewProp(strings.ToUpper(parts[0]))
	prop.Value = value

	for _, p := range parts[1:] {
		name, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("parse %q: bad parameter %q", line, p)
		}

		prop.Params.Add(strings.ToUpper(name), strings.Trim(v, `"`))
	}

	return prop, nil
}
