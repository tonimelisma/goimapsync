// Package vtimezone adds the VTIMEZONE components RFC 5545 requires for every
// TZID an iCalendar file uses, built from Go's time zone database. Files name
// zones by their IANA names; the VTIMEZONE describes the zone's rules in the
// year of the file's earliest time in that zone, which keeps a file's bytes
// the same from one run to the next.
package vtimezone

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/emersion/go-ical"
)

// localLayout is an iCalendar local date-time.
const localLayout = "20060102T150405"

const (
	secondsPerHour   = 3600
	secondsPerMinute = 60
	day              = 24 * time.Hour
)

// Add puts a VTIMEZONE into cal for every TZID its components use, before
// the other components. Zones Go does not know are left without one.
func Add(cal *ical.Calendar) {
	years := make(map[string]int)
	collect(cal.Component, years)

	var zones []*ical.Component

	for _, tzid := range slices.Sorted(maps.Keys(years)) {
		if comp, err := Component(tzid, years[tzid]); err == nil {
			zones = append(zones, comp)
		}
	}

	cal.Children = append(zones, cal.Children...)
}

// collect finds each TZID used in comp and its children, with the earliest
// year it is used in.
func collect(comp *ical.Component, years map[string]int) {
	for _, props := range comp.Props {
		for _, prop := range props {
			tzid := prop.Params.Get(ical.PropTimezoneID)
			if tzid == "" || len(prop.Value) < 4 {
				continue
			}

			year, err := strconv.Atoi(prop.Value[:4])
			if err != nil {
				continue
			}

			if y, seen := years[tzid]; !seen || year < y {
				years[tzid] = year
			}
		}
	}

	for _, child := range comp.Children {
		collect(child, years)
	}
}

// transition is a change of UTC offset.
type transition struct {
	at       time.Time // the instant of the change
	from, to int       // offsets in seconds east of UTC
	name     string    // the zone abbreviation after the change
}

// Component builds the VTIMEZONE of an IANA zone with its rules in year: one
// observance for a zone without changes that year, a yearly-repeating
// STANDARD and DAYLIGHT pair for a zone with a regular daylight saving time,
// and one observance per change otherwise.
func Component(tzid string, year int) (*ical.Component, error) {
	loc, err := time.LoadLocation(tzid)
	if err != nil {
		return nil, fmt.Errorf("load time zone %s: %w", tzid, err)
	}

	comp := ical.NewComponent(ical.CompTimezone)
	comp.Props.SetText(ical.PropTimezoneID, tzid)

	changes := transitions(loc, year)

	if len(changes) == 2 {
		for _, t := range changes {
			comp.Children = append(comp.Children, yearly(t, year))
		}

		return comp, nil
	}

	// No changes, or irregular ones: the offset at the start of the year
	// holds from 1970, then each change of the year applies once.
	name, offset := time.Date(year, time.January, 1, 0, 0, 0, 0, loc).Zone()
	comp.Children = append(comp.Children, observance(ical.CompTimezoneStandard, "19700101T000000", offset, offset, name))

	for _, t := range changes {
		comp.Children = append(comp.Children, observance(kind(t), wallClock(t), t.from, t.to, t.name))
	}

	return comp, nil
}

// yearly is an observance that starts in the year before year and repeats on
// the same weekday of the same month, as daylight saving rules do.
func yearly(t transition, year int) *ical.Component {
	local := t.at.In(time.FixedZone("", t.from))
	rule := byDay(local)

	start := local
	if prev, ok := onRule(year-1, local, rule); ok {
		start = prev
	}

	comp := observance(kind(t), start.Format(localLayout), t.from, t.to, t.name)

	rrule := ical.NewProp(ical.PropRecurrenceRule)
	rrule.Value = fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%s", int(local.Month()), rule)
	comp.Props.Set(rrule)

	return comp
}

// byDay is the BYDAY value for a date: its weekday in the month, counted
// from the end ("-1SU") when it falls in the month's last seven days.
func byDay(d time.Time) string {
	day := weekday(d)
	last := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()

	if d.Day()+7 > last {
		return "-1" + day
	}

	return strconv.Itoa((d.Day()-1)/7+1) + day
}

// onRule is the date in year that matches rule (such as "-1SU" or "2SU")
// in like's month, at like's wall-clock time.
func onRule(year int, like time.Time, rule string) (time.Time, bool) {
	n, err := strconv.Atoi(rule[:len(rule)-2])
	if err != nil {
		return time.Time{}, false
	}

	var matches []time.Time

	for d := 1; d <= 31; d++ {
		t := time.Date(year, like.Month(), d, like.Hour(), like.Minute(), like.Second(), 0, time.UTC)
		if t.Month() != like.Month() {
			break
		}

		if weekday(t) == rule[len(rule)-2:] {
			matches = append(matches, t)
		}
	}

	switch {
	case n == -1 && len(matches) > 0:
		return matches[len(matches)-1], true
	case n >= 1 && n <= len(matches):
		return matches[n-1], true
	default:
		return time.Time{}, false
	}
}

func weekday(t time.Time) string {
	return [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[t.Weekday()]
}

func kind(t transition) string {
	if t.to > t.from {
		return ical.CompTimezoneDaylight
	}

	return ical.CompTimezoneStandard
}

// wallClock is the local time a change happens at, on the clock before it.
func wallClock(t transition) string {
	return t.at.In(time.FixedZone("", t.from)).Format(localLayout)
}

func observance(name, start string, from, to int, abbrev string) *ical.Component {
	comp := ical.NewComponent(name)

	dtstart := ical.NewProp(ical.PropDateTimeStart)
	dtstart.Value = start
	comp.Props.Set(dtstart)

	for propName, offset := range map[string]int{ical.PropTimezoneOffsetFrom: from, ical.PropTimezoneOffsetTo: to} {
		prop := ical.NewProp(propName)
		prop.Value = formatOffset(offset)
		comp.Props.Set(prop)
	}

	if abbrev != "" {
		comp.Props.SetText(ical.PropTimezoneName, abbrev)
	}

	return comp
}

// formatOffset writes an offset as +hhmm, or +hhmmss when it has seconds.
func formatOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}

	h, m, s := seconds/secondsPerHour, seconds/secondsPerMinute%secondsPerMinute, seconds%secondsPerMinute
	if s != 0 {
		return fmt.Sprintf("%s%02d%02d%02d", sign, h, m, s)
	}

	return fmt.Sprintf("%s%02d%02d", sign, h, m)
}

// transitions finds the offset changes of loc during year, to the second.
func transitions(loc *time.Location, year int) []transition {
	var changes []transition

	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC)

	for d := start; d.Before(end); d = d.Add(day) {
		next := d.Add(day)
		if offset(d, loc) == offset(next, loc) {
			continue
		}

		// Narrow the day down to the second the offset changes.
		lo, hi := d, next
		for hi.Sub(lo) > time.Second {
			mid := lo.Add(hi.Sub(lo) / 2)
			if offset(mid, loc) == offset(lo, loc) {
				lo = mid
			} else {
				hi = mid
			}
		}

		name, to := hi.In(loc).Zone()
		changes = append(changes, transition{at: hi, from: offset(lo, loc), to: to, name: name})
	}

	return changes
}

func offset(t time.Time, loc *time.Location) int {
	_, off := t.In(loc).Zone()

	return off
}
