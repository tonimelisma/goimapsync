package restore

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/gcal"
)

// Google Calendar's limits on reminders.
const (
	maxReminders       = 5
	maxReminderMinutes = 40320 // four weeks
)

// Google's status words, and iCalendar's TENTATIVE.
const (
	transparent     = "transparent"
	tentativeStatus = "tentative"
	needsAction     = "needsAction"
	accepted        = "accepted"
	icalTentative   = "TENTATIVE"
)

// googleEvent converts a VEVENT for import into a calendar owned by owner.
// Google takes an event organized by someone else only when its owner
// attends it (F54), so the owner is added as an attendee when missing
// (D173). Meeting links and attachments, which Google cannot import, go
// into the description.
func googleEvent(comp *ical.Component, owner string) (*gcal.Import, error) {
	props := comp.Props
	e := &gcal.Import{
		ICalUID:      propText(props, ical.PropUID),
		Summary:      propText(props, ical.PropSummary),
		Location:     propText(props, ical.PropLocation),
		Status:       map[string]string{"CONFIRMED": "confirmed", icalTentative: tentativeStatus}[propValue(props, ical.PropStatus)],
		Transparency: map[string]string{icalTransparent: transparent}[propValue(props, ical.PropTransparency)],
		Visibility: map[string]string{
			"PUBLIC": "public", icalPrivate: private, icalConfidential: confidential,
		}[propValue(props, ical.PropClass)],
	}

	// A canceled meeting stays visible, as in the account it came from,
	// but no longer blocks time.
	if propValue(props, ical.PropStatus) == icalCancelled {
		e.Transparency = transparent
	}

	if seq, err := strconv.Atoi(propValue(props, ical.PropSequence)); err == nil {
		e.Sequence = seq
	}

	if err := googleTimes(props, e); err != nil {
		return nil, err
	}

	for _, name := range []string{ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates} {
		for i := range props[name] {
			e.Recurrence = append(e.Recurrence, contentLine(&props[name][i]))
		}
	}

	googlePeople(props, e, owner)

	var extra []string

	for _, p := range props["CONFERENCE"] {
		extra = append(extra, "Meeting link: "+p.Value)
	}

	for _, p := range props[ical.PropAttach] {
		if !strings.EqualFold(p.Params.Get(ical.ParamValue), "BINARY") {
			extra = append(extra, "Attachment: "+p.Value)
		}
	}

	e.Description = propText(props, ical.PropDescription)
	if len(extra) > 0 {
		e.Description = strings.TrimSpace(e.Description + "\n\n" + strings.Join(extra, "\n"))
	}

	e.Reminders = googleReminders(comp)

	return e, nil
}

// googleTimes sets the start, end and original start.
func googleTimes(props ical.Props, e *gcal.Import) error {
	start := props.Get(ical.PropDateTimeStart)
	if start == nil {
		return fmt.Errorf("event %s: no DTSTART", e.ICalUID)
	}

	var err error

	if e.Start, err = googleTime(start); err != nil {
		return fmt.Errorf("event %s: %w", e.ICalUID, err)
	}

	e.End = e.Start

	if end := props.Get(ical.PropDateTimeEnd); end != nil {
		if e.End, err = googleTime(end); err != nil {
			return fmt.Errorf("event %s: %w", e.ICalUID, err)
		}
	} else if e.Start.Date != "" {
		day, _ := time.Parse(time.DateOnly, e.Start.Date) //nolint:errcheck // written by googleTime.
		e.End = gcal.DateTime{Date: day.AddDate(0, 0, 1).Format(time.DateOnly)}
	}

	if rid := props.Get(ical.PropRecurrenceID); rid != nil {
		original, err := googleTime(rid)
		if err != nil {
			return fmt.Errorf("event %s: %w", e.ICalUID, err)
		}

		e.OriginalStartTime = &original
	}

	return nil
}

// googleTime converts a DATE or DATE-TIME value: a date, a UTC time, or a
// local time in its TZID (an IANA name once readSeries pinned it). A
// floating time left unpinned, with no zone to pin it to, is taken as UTC.
func googleTime(p *ical.Prop) (gcal.DateTime, error) {
	v := p.Value

	if p.Params.Get(ical.ParamValue) == "DATE" || len(v) == len("20060102") {
		t, err := time.Parse("20060102", v)
		if err != nil {
			return gcal.DateTime{}, fmt.Errorf("parse date %q: %w", v, err)
		}

		return gcal.DateTime{Date: t.Format(time.DateOnly)}, nil
	}

	t, err := time.Parse("20060102T150405", strings.TrimSuffix(v, "Z"))
	if err != nil {
		return gcal.DateTime{}, fmt.Errorf("parse time %q: %w", v, err)
	}

	local := t.Format("2006-01-02T15:04:05")

	if tz := p.Params.Get(ical.ParamTimezoneID); tz != "" && !strings.HasSuffix(v, "Z") {
		if _, err := time.LoadLocation(tz); err == nil {
			return gcal.DateTime{DateTime: local, TimeZone: tz}, nil
		}
	}

	return gcal.DateTime{DateTime: local + "Z", TimeZone: "UTC"}, nil
}

// googlePeople sets the organizer and attendees. The owner of an event
// organized by someone else attends it, as an accepted guest when the
// event did not list them.
func googlePeople(props ical.Props, e *gcal.Import, owner string) {
	for _, a := range props[ical.PropAttendee] {
		email := mailto(a.Value)
		if email == "" {
			continue
		}

		e.Attendees = append(e.Attendees, gcal.NewAttendee{
			Email:       email,
			DisplayName: a.Params.Get(ical.ParamCommonName),
			ResponseStatus: map[string]string{
				"ACCEPTED": accepted, "DECLINED": "declined", icalTentative: tentativeStatus, "NEEDS-ACTION": needsAction,
			}[a.Params.Get(ical.ParamParticipationStatus)],
			Optional: a.Params.Get(ical.ParamRole) == roleOptional,
			Resource: a.Params.Get(ical.ParamCalendarUserType) == userResource,
		})
	}

	org := props.Get(ical.PropOrganizer)
	if org == nil || mailto(org.Value) == "" || strings.EqualFold(mailto(org.Value), owner) {
		return
	}

	e.Organizer = &gcal.Person{Email: mailto(org.Value), DisplayName: org.Params.Get(ical.ParamCommonName)}

	if !slices.ContainsFunc(e.Attendees, func(a gcal.NewAttendee) bool { return strings.EqualFold(a.Email, owner) }) {
		e.Attendees = append(e.Attendees, gcal.NewAttendee{Email: owner, ResponseStatus: accepted})
	}
}

// googleReminders converts alarms a fixed time before the start; the event
// has no reminders when it has none of those.
func googleReminders(comp *ical.Component) gcal.NewReminders {
	var r gcal.NewReminders

	for _, alarm := range comp.Children {
		if alarm.Name != ical.CompAlarm || len(r.Overrides) == maxReminders {
			continue
		}

		minutes, ok := alarmMinutes(alarm)
		if !ok || minutes > maxReminderMinutes {
			continue
		}

		method := "popup"
		if propValue(alarm.Props, ical.PropAction) == "EMAIL" {
			method = "email"
		}

		r.Overrides = append(r.Overrides, gcal.Reminder{Method: method, Minutes: minutes})
	}

	return r
}

// mailto returns the address of a mailto: URI, or "".
func mailto(uri string) string {
	if len(uri) > len("mailto:") && strings.EqualFold(uri[:len("mailto:")], "mailto:") {
		return uri[len("mailto:"):]
	}

	return ""
}
