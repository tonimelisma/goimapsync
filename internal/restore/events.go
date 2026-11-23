package restore

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/wintz"
)

// iCalendar values both event converters read.
const (
	icalTransparent  = "TRANSPARENT"
	icalCancelled    = "CANCELLED"
	icalPrivate      = "PRIVATE"
	icalConfidential = "CONFIDENTIAL"
	roleOptional     = "OPT-PARTICIPANT"
	userResource     = "RESOURCE"
	confidential     = "confidential"
	valueDateTime    = "DATE-TIME"
	icalLocal        = "20060102T150405" // a DATE-TIME without its zone
	private          = "private"
)

// day is a day's length, ignoring clock changes.
const day = 24 * time.Hour

// eventSeries is an archived event file: a single or recurring event with
// its changed instances (D43). master is nil when the file holds only
// changed instances.
type eventSeries struct {
	uid        string
	master     *ical.Component
	exceptions []*ical.Component
}

// readSeries parses an archived event file. Floating times, and times in a
// zone pimvault cannot resolve, are pinned to zone (D171) when it is set;
// in changed instances, to the series' own zone, whose occurrences they
// replace.
func readSeries(raw []byte, zone string) (*eventSeries, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("parse event: %w", err)
	}

	s := &eventSeries{}

	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}

		if s.uid == "" {
			s.uid = propText(comp.Props, ical.PropUID)
		}

		if comp.Props.Get(ical.PropRecurrenceID) != nil {
			s.exceptions = append(s.exceptions, comp)
		} else if s.master == nil {
			s.master = comp
		}
	}

	if s.master == nil && len(s.exceptions) == 0 {
		return nil, fmt.Errorf("parse event: no VEVENT")
	}

	if zone != "" {
		if s.master != nil {
			pinFloating(s.master, zone)
			zone = startZone(s.master, zone)
		}

		for _, comp := range s.exceptions {
			pinFloating(comp, zone)
		}
	}

	return s, nil
}

// startZone returns the time zone of a component's DTSTART, or fallback
// for a date.
func startZone(comp *ical.Component, fallback string) string {
	p := comp.Props.Get(ical.PropDateTimeStart)

	switch {
	case p == nil:
		return fallback
	case p.Params.Get(ical.ParamTimezoneID) != "":
		return p.Params.Get(ical.ParamTimezoneID)
	case strings.HasSuffix(p.Value, "Z"):
		return zoneUTC
	default:
		return fallback
	}
}

// resolveZone returns a time zone as an IANA name Go can load: as it is,
// or mapped from a Windows name; else fallback.
func resolveZone(tz, fallback string) string {
	if tz == "" {
		return fallback
	}

	if _, err := time.LoadLocation(tz); err == nil {
		return tz
	}

	if iana, ok := wintz.IANA(tz); ok {
		return iana
	}

	return fallback
}

// pinFloating gives a component's date-times an IANA TZID: Windows names
// are mapped, and floating times, which mean the same clock time wherever
// they are kept (RFC 5545), and unknown zones get zone. Dates and UTC
// times keep theirs. A floating UNTIL then becomes UTC, as RFC 5545
// requires beside a DTSTART with a time zone.
func pinFloating(comp *ical.Component, zone string) {
	for _, name := range []string{
		ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropRecurrenceID, ical.PropExceptionDates, ical.PropRecurrenceDates,
	} {
		for i := range comp.Props[name] {
			p := &comp.Props[name][i]

			if kind := p.Params.Get(ical.ParamValue); kind != "" && kind != valueDateTime ||
				len(p.Value) == len("20060102") || strings.HasSuffix(p.Value, "Z") {
				continue
			}

			p.Params.Set(ical.ParamTimezoneID, resolveZone(p.Params.Get(ical.ParamTimezoneID), zone))
		}
	}

	start := comp.Props.Get(ical.PropDateTimeStart)
	if start == nil || start.Params.Get(ical.ParamTimezoneID) == "" {
		return
	}

	loc, err := time.LoadLocation(start.Params.Get(ical.ParamTimezoneID))
	if err != nil {
		return
	}

	for i := range comp.Props[ical.PropRecurrenceRule] {
		p := &comp.Props[ical.PropRecurrenceRule][i]
		parts := strings.Split(p.Value, ";")

		for j, part := range parts {
			v, ok := strings.CutPrefix(part, "UNTIL=")
			if !ok || len(v) != len(icalLocal) {
				continue
			}

			if t, err := time.ParseInLocation(icalLocal, v, loc); err == nil {
				parts[j] = "UNTIL=" + t.UTC().Format(icalLocal+"Z")
			}
		}

		p.Value = strings.Join(parts, ";")
	}
}

// propText returns a property's unescaped text, or "".
func propText(props ical.Props, name string) string {
	p := props.Get(name)
	if p == nil {
		return ""
	}

	if t, err := p.Text(); err == nil {
		return t
	}

	return p.Value
}

// contentLine renders a property as its iCalendar line without folding,
// such as "EXDATE;TZID=Europe/Helsinki:20260927T140000".
func contentLine(p *ical.Prop) string {
	var b strings.Builder

	b.WriteString(p.Name)

	for name, values := range p.Params {
		b.WriteString(";" + name + "=" + strings.Join(values, ","))
	}

	b.WriteString(":" + p.Value)

	return b.String()
}

// isoDuration matches the RFC 5545 durations alarms use: weeks, or days
// and a time part.
var isoDuration = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// alarmMinutes returns how many minutes before the start an alarm fires,
// and false for alarms that are not a fixed time before the start (after
// it, relative to the end, or at an absolute time).
func alarmMinutes(alarm *ical.Component) (int, bool) {
	trigger := alarm.Props.Get(ical.PropTrigger)
	if trigger == nil || trigger.Params.Get(ical.ParamValue) == valueDateTime ||
		trigger.Params.Get(ical.ParamRelated) == "END" {
		return 0, false
	}

	m := isoDuration.FindStringSubmatch(trigger.Value)
	if m == nil {
		return 0, false
	}

	var d time.Duration

	for i, unit := range []time.Duration{7 * day, day, time.Hour, time.Minute, time.Second} {
		if n, err := strconv.Atoi(m[i+2]); err == nil {
			d += time.Duration(n) * unit
		}
	}

	if d > 0 && m[1] != "-" {
		return 0, false
	}

	return int(d / time.Minute), true
}
