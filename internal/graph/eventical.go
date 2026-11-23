package graph

import (
	"bytes"
	"cmp"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/icalfile"
	"github.com/tonimelisma/pimvault/internal/wintz"
)

// graphLayout is how Graph writes a DateTimeTimeZone's dateTime.
const graphLayout = "2006-01-02T15:04:05.9999999"

// iCalendar values used more than once.
const (
	classPrivate    = "PRIVATE"
	partAccepted    = "ACCEPTED"
	partNeedsAction = "NEEDS-ACTION"
	freqMonthly     = "FREQ=MONTHLY"
	freqYearly      = "FREQ=YEARLY"
)

// EncodeSeries renders an Outlook event, or a series master with its
// exceptions and canceled occurrences, as one iCalendar file (D43) using
// standard properties only (D150). Times are written in the IANA zone that
// matches the event's Windows zone (D64), or in UTC when there is none.
// attachments maps event IDs to their attachments; file attachments are
// embedded.
func EncodeSeries(e *Event, exceptions []Event, canceled []string, attachments map[string][]Attachment) ([]byte, error) {
	root := icalfile.New()

	uid := seriesUID(e)
	loc := zone(e.OriginalStartTimeZone)

	master, err := eventComponent(e, uid, attachments[e.ID])
	if err != nil {
		return nil, err
	}

	if e.Recurrence != nil {
		rule, ruleErr := recurrenceRule(e.Recurrence, loc, e.IsAllDay)
		if ruleErr != nil {
			return nil, fmt.Errorf("convert recurrence of event %s: %w", e.ID, ruleErr)
		}

		rrule := ical.NewProp(ical.PropRecurrenceRule)
		rrule.Value = rule
		master.Props.Set(rrule)

		if exErr := addExdates(master, e, loc, canceled); exErr != nil {
			return nil, exErr
		}
	}

	root.Children = append(root.Children, master)

	// Graph's order is not stable; files must be, so they are only rewritten
	// when an event changed.
	exceptions = slices.Clone(exceptions)
	slices.SortFunc(exceptions, func(a, b Event) int { return cmp.Compare(a.OriginalStart, b.OriginalStart) })

	for i := range exceptions {
		comp, compErr := eventComponent(&exceptions[i], uid, attachments[exceptions[i].ID])
		if compErr != nil {
			return nil, compErr
		}

		original, parseErr := time.Parse(time.RFC3339, exceptions[i].OriginalStart)
		if parseErr != nil {
			return nil, fmt.Errorf("parse original start of event %s: %w", exceptions[i].ID, parseErr)
		}

		recurrenceID := ical.NewProp(ical.PropRecurrenceID)
		setTime(recurrenceID, original, loc, e.IsAllDay)
		comp.Props.Set(recurrenceID)

		root.Children = append(root.Children, comp)
	}

	data, err := icalfile.Encode(root)
	if err != nil {
		return nil, fmt.Errorf("render event %s: %w", e.ID, err)
	}

	return data, nil
}

// zone is the Go location for a Windows zone name, or UTC.
func zone(windows string) *time.Location {
	if loc, ok := wintz.Location(windows); ok {
		return loc
	}

	return time.UTC
}

// seriesUID is the iCalendar UID of an event. Graph's iCalUId is Outlook's
// global object ID in hex; for a meeting that came from another calendar
// system it embeds that system's UID after a "vCal-Uid" marker (MS-OXOCAL
// PidLidGlobalObjectId), which is the UID other copies of the meeting carry.
// Outlook's own events keep the hex ID, which Outlook also exports as UID.
func seriesUID(e *Event) string {
	if e.ICalUID == "" {
		return e.ID
	}

	raw, err := hex.DecodeString(e.ICalUID)
	if err != nil {
		return e.ICalUID
	}

	marker := []byte("vCal-Uid\x01\x00\x00\x00")

	i := bytes.Index(raw, marker)
	if i < 0 {
		return e.ICalUID
	}

	uid := raw[i+len(marker):]
	if end := bytes.IndexByte(uid, 0); end >= 0 {
		uid = uid[:end]
	}

	if len(uid) == 0 || !utf8.Valid(uid) {
		return e.ICalUID
	}

	return string(uid)
}

// eventComponent renders one event's own properties as a VEVENT.
func eventComponent(e *Event, uid string, attachments []Attachment) (*ical.Component, error) {
	comp := ical.NewComponent(ical.CompEvent)
	props := comp.Props

	icalfile.SetText(props, ical.PropUID, uid)

	if err := setStamps(props, e.ID, e.CreatedDateTime, e.LastModifiedDateTime); err != nil {
		return nil, err
	}

	if err := times(props, e); err != nil {
		return nil, err
	}

	icalfile.SetText(props, ical.PropSummary, e.Subject)
	icalfile.SetText(props, ical.PropDescription, strings.TrimSpace(e.Body.Content))
	icalfile.SetText(props, ical.PropLocation, e.Location.DisplayName)

	setCategories(props, e.Categories)

	flags(props, e)
	people(props, e)
	links(props, e, attachments)

	if e.IsReminderOn {
		alarm := ical.NewComponent(ical.CompAlarm)
		icalfile.SetRaw(alarm.Props, ical.PropTrigger, fmt.Sprintf("-PT%dM", e.ReminderMinutes))
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		icalfile.SetText(alarm.Props, ical.PropDescription, cmp.Or(e.Subject, "Reminder"))
		comp.Children = append(comp.Children, alarm)
	}

	return comp, nil
}

// times sets DTSTART and DTEND in the event's zones.
func times(props ical.Props, e *Event) error {
	for _, f := range []struct {
		name  string
		value DateTimeZone
		zone  string
	}{
		{ical.PropDateTimeStart, e.Start, e.OriginalStartTimeZone},
		{ical.PropDateTimeEnd, e.End, cmp.Or(e.OriginalEndTimeZone, e.OriginalStartTimeZone)},
	} {
		if f.value.DateTime == "" {
			continue
		}

		t, err := parseDateTimeZone(f.value)
		if err != nil {
			return fmt.Errorf("read times of event %s: %w", e.ID, err)
		}

		prop := ical.NewProp(f.name)
		setTime(prop, t, zone(f.zone), e.IsAllDay)
		props.Set(prop)
	}

	return nil
}

// flags sets CLASS, TRANSP, PRIORITY and STATUS.
func flags(props ical.Props, e *Event) {
	icalfile.SetText(props, ical.PropClass, map[string]string{
		"private": classPrivate, "personal": classPrivate, "confidential": "CONFIDENTIAL",
	}[e.Sensitivity])
	icalfile.SetText(props, ical.PropTransparency, map[string]string{"free": "TRANSPARENT"}[e.ShowAs])

	setPriority(props, e.Importance)

	if e.IsCancelled {
		props.SetText(ical.PropStatus, "CANCELLED")
	}
}

// links adds the online meeting link (RFC 7986 CONFERENCE) and embeds file
// attachments.
func links(props ical.Props, e *Event, attachments []Attachment) {
	if e.OnlineMeeting != nil && e.OnlineMeeting.JoinURL != "" {
		conf := ical.NewProp("CONFERENCE")
		conf.Value = e.OnlineMeeting.JoinURL
		conf.Params.Set(ical.ParamValue, string(ical.ValueURI))
		conf.Params.Set("FEATURE", "VIDEO")
		props.Add(conf)
	}

	attachments = slices.Clone(attachments)
	slices.SortFunc(attachments, func(a, b Attachment) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ContentBytes, b.ContentBytes))
	})

	for _, a := range attachments {
		if a.ContentBytes == "" {
			continue
		}

		attach := ical.NewProp(ical.PropAttach)
		attach.Value = a.ContentBytes
		attach.Params.Set(ical.ParamEncoding, "BASE64")
		attach.Params.Set(ical.ParamValue, string(ical.ValueBinary))
		icalfile.SetParam(attach, ical.ParamFormatType, a.ContentType)
		props.Add(attach)
	}
}

func people(props ical.Props, e *Event) {
	if org := e.Organizer.EmailAddress; org.Address != "" {
		prop := ical.NewProp(ical.PropOrganizer)
		prop.Value = "mailto:" + org.Address
		icalfile.SetParam(prop, ical.ParamCommonName, org.Name)
		props.Set(prop)
	}

	for _, a := range e.Attendees {
		if a.EmailAddress.Address == "" {
			continue
		}

		prop := ical.NewProp(ical.PropAttendee)
		prop.Value = "mailto:" + a.EmailAddress.Address
		icalfile.SetParam(prop, ical.ParamCommonName, a.EmailAddress.Name)
		icalfile.SetParam(prop, ical.ParamParticipationStatus, map[string]string{
			"accepted": partAccepted, "organizer": partAccepted, "declined": "DECLINED",
			"tentativelyAccepted": "TENTATIVE", "notResponded": partNeedsAction, "none": partNeedsAction,
		}[a.Status.Response])

		switch a.Type {
		case "optional":
			prop.Params.Set(ical.ParamRole, "OPT-PARTICIPANT")
		case "resource":
			prop.Params.Set(ical.ParamRole, "NON-PARTICIPANT")
			prop.Params.Set(ical.ParamCalendarUserType, "RESOURCE")
		default:
			prop.Params.Set(ical.ParamRole, "REQ-PARTICIPANT")
		}

		props.Add(prop)
	}
}

// addExdates adds an EXDATE for each canceled occurrence. Occurrence IDs end
// in the occurrence's start date in the series' recurrence time zone; its
// time of day is the series'.
func addExdates(master *ical.Component, e *Event, loc *time.Location, canceled []string) error {
	start, err := parseDateTimeZone(e.Start)
	if err != nil {
		return fmt.Errorf("read start of event %s: %w", e.ID, err)
	}

	recurrenceLoc := loc
	if tz := e.Recurrence.Range.RecurrenceTimeZone; tz != "" {
		recurrenceLoc = zone(tz)
	}

	local := start.In(recurrenceLoc)

	for _, occurrence := range slices.Sorted(slices.Values(canceled)) {
		datePart := occurrence[strings.LastIndex(occurrence, ".")+1:]

		day, err := time.Parse(time.DateOnly, datePart)
		if err != nil {
			return fmt.Errorf("parse canceled occurrence %q of event %s: %w", occurrence, e.ID, err)
		}

		exdate := ical.NewProp(ical.PropExceptionDates)

		if e.IsAllDay {
			// An all-day occurrence is its date.
			exdate.SetDate(day)
		} else {
			at := time.Date(day.Year(), day.Month(), day.Day(), local.Hour(), local.Minute(), local.Second(), 0, recurrenceLoc)
			setTime(exdate, at, loc, false)
		}

		master.Props.Add(exdate)
	}

	return nil
}

// recurrenceRule turns Graph's recurrence pattern and range into an RRULE.
func recurrenceRule(rec *Recurrence, loc *time.Location, allDay bool) (string, error) {
	p, r := rec.Pattern, rec.Range
	days := weekdays(p.DaysOfWeek)

	var parts []string

	switch p.Type {
	case "daily":
		parts = []string{"FREQ=DAILY"}
	case "weekly":
		parts = []string{"FREQ=WEEKLY", "BYDAY=" + days}
		if wkst := weekdays([]string{p.FirstDayOfWeek}); wkst != "" {
			parts = append(parts, "WKST="+wkst)
		}
	case "absoluteMonthly":
		parts = []string{freqMonthly, "BYMONTHDAY=" + strconv.Itoa(p.DayOfMonth)}
	case "relativeMonthly":
		parts = []string{freqMonthly, "BYDAY=" + days, "BYSETPOS=" + setPos(p.Index)}
	case "absoluteYearly":
		parts = []string{freqYearly, "BYMONTH=" + strconv.Itoa(p.Month), "BYMONTHDAY=" + strconv.Itoa(p.DayOfMonth)}
	case "relativeYearly":
		parts = []string{freqYearly, "BYMONTH=" + strconv.Itoa(p.Month), "BYDAY=" + days, "BYSETPOS=" + setPos(p.Index)}
	default:
		return "", fmt.Errorf("unknown recurrence pattern %q", p.Type)
	}

	if p.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(p.Interval))
	}

	switch r.Type {
	case "numbered":
		parts = append(parts, "COUNT="+strconv.Itoa(r.NumberOfOccurrences))
	case "endDate":
		until, err := untilValue(r, loc, allDay)
		if err != nil {
			return "", err
		}

		parts = append(parts, "UNTIL="+until)
	}

	return strings.Join(parts, ";"), nil
}

// untilValue is a range's last day as an RRULE UNTIL: a date for all-day
// series, else the end of that day in UTC, as RFC 5545 requires when DTSTART
// has a time zone.
func untilValue(r RecurrenceRange, loc *time.Location, allDay bool) (string, error) {
	day, err := time.Parse(time.DateOnly, r.EndDate)
	if err != nil {
		return "", fmt.Errorf("parse recurrence end %q: %w", r.EndDate, err)
	}

	if allDay {
		return day.Format("20060102"), nil
	}

	if r.RecurrenceTimeZone != "" {
		loc = zone(r.RecurrenceTimeZone)
	}

	end := time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, loc)

	return end.UTC().Format("20060102T150405Z"), nil
}

func weekdays(names []string) string {
	codes := make([]string, 0, len(names))

	for _, n := range names {
		if code, ok := map[string]string{
			"sunday": "SU", "monday": "MO", "tuesday": "TU", "wednesday": "WE",
			"thursday": "TH", "friday": "FR", "saturday": "SA",
		}[n]; ok {
			codes = append(codes, code)
		}
	}

	return strings.Join(codes, ",")
}

func setPos(index string) string {
	return cmp.Or(map[string]string{"first": "1", "second": "2", "third": "3", "fourth": "4", "last": "-1"}[index], "1")
}

// parseDateTimeZone reads a Graph date-time in its stated zone.
func parseDateTimeZone(dt DateTimeZone) (time.Time, error) {
	loc := time.UTC
	if dt.TimeZone != "" && dt.TimeZone != "UTC" {
		loc = zone(dt.TimeZone)
	}

	t, err := time.ParseInLocation(graphLayout, dt.DateTime, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", dt.DateTime, err)
	}

	return t, nil
}

// setTime writes t in loc, or a date for all-day events. An all-day time
// that is midnight UTC is taken as the date itself; any other is the
// instant of local midnight, whose date is read in loc. Which one Graph
// returns is not documented, and the two only agree for zones at UTC.
func setTime(prop *ical.Prop, t time.Time, loc *time.Location, allDay bool) {
	if allDay {
		day := t.UTC()
		if day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 {
			day = t.In(loc)
		}

		prop.SetDate(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))

		return
	}

	prop.SetDateTime(t.In(loc))
}
