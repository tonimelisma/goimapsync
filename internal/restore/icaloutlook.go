package restore

import (
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// graphDateTime is the layout of Graph's local date-times.
const graphDateTime = "2006-01-02T15:04:05"

// outlookEvent converts a VEVENT into an Outlook event in a calendar owned
// by owner. Meetings are created as drafts, which sends no invitations
// (D166). Outlook makes the owner the organizer; another organizer
// is named in the body, as are meeting links and attachment links.
func outlookEvent(comp *ical.Component, owner string) (*graph.NewEvent, error) {
	props := comp.Props
	e := &graph.NewEvent{
		Subject:     propText(props, ical.PropSummary),
		Sensitivity: map[string]string{icalPrivate: private, icalConfidential: confidential}[propValue(props, ical.PropClass)],
		Categories:  categoriesOf(props),
	}

	switch {
	case propValue(props, ical.PropTransparency) == icalTransparent, propValue(props, ical.PropStatus) == icalCancelled:
		e.ShowAs = "free"
	case propValue(props, ical.PropStatus) == icalTentative:
		e.ShowAs = tentativeStatus
	}

	if loc := propText(props, ical.PropLocation); loc != "" {
		e.Location = &graph.Location{DisplayName: loc}
	}

	if err := outlookTimes(props, e); err != nil {
		return nil, err
	}

	e.Attendees = outlookAttendees(props, owner)
	e.IsDraft = len(e.Attendees) > 0

	extra := outlookExtras(props, owner)

	body := propText(props, ical.PropDescription)
	if len(extra) > 0 {
		body = strings.TrimSpace(body + "\n\n" + strings.Join(extra, "\n"))
	}

	if body != "" {
		e.Body = graph.TextBody(body)
	}

	for _, alarm := range comp.Children {
		if minutes, ok := alarmMinutes(alarm); alarm.Name == ical.CompAlarm && ok {
			e.IsReminderOn, e.ReminderMinutes = true, minutes

			break
		}
	}

	return e, nil
}

// outlookAttendees returns an event's guests other than the owner.
func outlookAttendees(props ical.Props, owner string) []graph.NewAttendee {
	var out []graph.NewAttendee

	for _, a := range props[ical.PropAttendee] {
		email := mailto(a.Value)
		if email == "" || strings.EqualFold(email, owner) {
			continue
		}

		kind := "required"

		switch {
		case a.Params.Get(ical.ParamCalendarUserType) == userResource:
			kind = "resource"
		case a.Params.Get(ical.ParamRole) == roleOptional:
			kind = "optional"
		}

		out = append(out, graph.NewAttendee{
			EmailAddress: graph.EmailAddress{Address: email, Name: a.Params.Get(ical.ParamCommonName)}, Type: kind,
		})
	}

	return out
}

// outlookExtras returns body lines for what Outlook has no field for: an
// organizer other than the owner, meeting links and attachment links.
func outlookExtras(props ical.Props, owner string) []string {
	var extra []string

	if org := props.Get(ical.PropOrganizer); org != nil && mailto(org.Value) != "" && !strings.EqualFold(mailto(org.Value), owner) {
		extra = append(extra, organizerLine(mailto(org.Value), org.Params.Get(ical.ParamCommonName)))
	}

	for _, p := range props["CONFERENCE"] {
		extra = append(extra, "Meeting link: "+p.Value)
	}

	for _, p := range props[ical.PropAttach] {
		if !strings.EqualFold(p.Params.Get(ical.ParamValue), "BINARY") {
			extra = append(extra, "Attachment: "+p.Value)
		}
	}

	return extra
}

func organizerLine(email, name string) string {
	if name != "" {
		return fmt.Sprintf("Organizer: %s <%s>", name, email)
	}

	return "Organizer: " + email
}

// categoriesOf returns the values of every CATEGORIES property.
func categoriesOf(props ical.Props) []string {
	var out []string

	for i := range props[ical.PropCategories] {
		values, err := props[ical.PropCategories][i].TextList()
		if err != nil {
			continue
		}

		for _, c := range values {
			if c = strings.TrimSpace(c); c != "" {
				out = append(out, c)
			}
		}
	}

	return out
}

// outlookTimes sets the start and end; an all-day event's dates are
// midnights.
func outlookTimes(props ical.Props, e *graph.NewEvent) error {
	start, end := props.Get(ical.PropDateTimeStart), props.Get(ical.PropDateTimeEnd)
	if start == nil {
		return fmt.Errorf("event %s: no DTSTART", propText(props, ical.PropUID))
	}

	s, allDay, err := graphTime(start)
	if err != nil {
		return err
	}

	e.Start, e.IsAllDay = s, allDay

	switch {
	case end != nil:
		if e.End, _, err = graphTime(end); err != nil {
			return err
		}
	case allDay:
		day, _ := time.Parse(graphDateTime, s.DateTime) //nolint:errcheck // written by graphTime.
		e.End = &graph.DateTimeZone{DateTime: day.AddDate(0, 0, 1).Format(graphDateTime), TimeZone: s.TimeZone}
	default:
		e.End = s
	}

	return nil
}

// graphTime converts a DATE or DATE-TIME value into Graph's form: a local
// time in its TZID (Graph takes IANA names), else UTC.
func graphTime(p *ical.Prop) (*graph.DateTimeZone, bool, error) {
	g, err := googleTime(p)
	if err != nil {
		return nil, false, err
	}

	if g.Date != "" {
		return &graph.DateTimeZone{DateTime: g.Date + "T00:00:00", TimeZone: zoneUTC}, true, nil
	}

	return &graph.DateTimeZone{DateTime: strings.TrimSuffix(g.DateTime, "Z"), TimeZone: g.TimeZone}, false, nil
}

// instant returns the moment a DATE or DATE-TIME value names; a date is
// midnight UTC.
func instant(p *ical.Prop) (time.Time, error) {
	g, _, err := graphTime(p)
	if err != nil {
		return time.Time{}, err
	}

	loc, err := time.LoadLocation(g.TimeZone)
	if err != nil {
		loc = time.UTC
	}

	t, err := time.ParseInLocation(graphDateTime, g.DateTime, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", g.DateTime, err)
	}

	return t, nil
}
