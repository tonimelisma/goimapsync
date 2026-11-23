package restore

import (
	"strconv"
	"strings"
	"time"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// graphDay is Graph's name for an RRULE weekday, or "".
func graphDay(code string) string {
	switch code {
	case "SU":
		return "sunday"
	case "MO":
		return "monday"
	case "TU":
		return "tuesday"
	case "WE":
		return "wednesday"
	case "TH":
		return "thursday"
	case "FR":
		return "friday"
	case "SA":
		return "saturday"
	default:
		return ""
	}
}

// graphIndex is Graph's name for an RRULE ordinal or BYSETPOS, or "".
func graphIndex(ord string) string {
	switch ord {
	case "1", "+1":
		return "first"
	case "2", "+2":
		return "second"
	case "3", "+3":
		return "third"
	case "4", "+4":
		return "fourth"
	case "-1":
		return "last"
	default:
		return ""
	}
}

// rrule is an RRULE's parts by name.
type rrule map[string]string

func parseRRule(rule string) (rrule, bool) {
	r := rrule{}

	for _, p := range strings.Split(rule, ";") {
		k, v, found := strings.Cut(p, "=")
		if !found {
			return nil, false
		}

		switch k = strings.ToUpper(k); k {
		case "FREQ", "INTERVAL", "COUNT", "UNTIL", "BYDAY", "BYMONTHDAY", "BYMONTH", "BYSETPOS", "WKST":
			r[k] = v
		default:
			return nil, false // such as BYHOUR: Graph cannot repeat that way
		}
	}

	return r, true
}

// number reads a numeric part, or def when absent.
func (r rrule) number(name string, def int) (int, bool) {
	v, has := r[name]
	if !has {
		return def, true
	}

	n, err := strconv.Atoi(v)

	return n, err == nil && n > 0
}

// graphRecurrence converts an RRULE starting on start into Graph's
// patternedRecurrence. ok is false for rules Graph cannot represent.
func graphRecurrence(rule string, start time.Time) (*graph.Recurrence, bool) {
	r, ok := parseRRule(rule)
	if !ok {
		return nil, false
	}

	pattern, ok := graphPattern(r, start)
	if !ok {
		return nil, false
	}

	rng := graph.RecurrenceRange{Type: "noEnd", StartDate: start.Format(time.DateOnly)}

	switch {
	case r["COUNT"] != "":
		n, ok := r.number("COUNT", 0)
		if !ok {
			return nil, false
		}

		rng.Type, rng.NumberOfOccurrences = "numbered", n
	case r["UNTIL"] != "":
		until := r["UNTIL"]
		if len(until) < len("20060102") {
			return nil, false
		}

		end := until[0:4] + "-" + until[4:6] + "-" + until[6:8]

		// A UTC UNTIL ends on its date in the series' own time zone.
		if t, err := time.Parse("20060102T150405Z", until); err == nil {
			end = t.In(start.Location()).Format(time.DateOnly)
		}

		rng.Type, rng.EndDate = "endDate", end
	}

	return &graph.Recurrence{Pattern: pattern, Range: rng}, true
}

// graphPattern converts FREQ and its BY* parts.
func graphPattern(r rrule, start time.Time) (graph.RecurrencePattern, bool) {
	interval, okInterval := r.number("INTERVAL", 1)
	dayOfMonth, okDay := r.number("BYMONTHDAY", start.Day())
	month, okMonth := r.number("BYMONTH", int(start.Month()))
	days, index, okDays := byDay(r["BYDAY"], r["BYSETPOS"])

	if !okInterval || !okDay || !okMonth || !okDays {
		return graph.RecurrencePattern{}, false
	}

	p := graph.RecurrencePattern{Interval: interval, DaysOfWeek: days, Index: index}

	_, byMonth := r["BYMONTH"]
	_, byMonthDay := r["BYMONTHDAY"]

	switch r["FREQ"] {
	case "DAILY":
		p.Type = "daily"

		return p, len(days) == 0 && !byMonth && !byMonthDay
	case "WEEKLY":
		if byMonth || byMonthDay {
			return graph.RecurrencePattern{}, false
		}

		p.Type = "weekly"
		if len(days) == 0 {
			p.DaysOfWeek = []string{strings.ToLower(start.Weekday().String())}
		}

		p.FirstDayOfWeek = graphDay(r["WKST"])

		return p, index == ""
	case "MONTHLY":
		if byMonth {
			return graph.RecurrencePattern{}, false
		}

		return monthly(&p, len(days) > 0, dayOfMonth, 0)
	case "YEARLY":
		return monthly(&p, len(days) > 0, dayOfMonth, month)
	default:
		return graph.RecurrencePattern{}, false
	}
}

// monthly finishes a monthly (month 0) or yearly pattern: relative when it
// names weekdays, absolute otherwise.
func monthly(p *graph.RecurrencePattern, relative bool, dayOfMonth, month int) (graph.RecurrencePattern, bool) {
	kind := "Monthly"
	if month > 0 {
		kind, p.Month = "Yearly", month
	}

	if relative {
		p.Type = "relative" + kind

		return *p, p.Index != ""
	}

	p.Type, p.DayOfMonth = "absolute"+kind, dayOfMonth

	return *p, true
}

// byDay reads BYDAY ("MO,WE" or "2TU") and BYSETPOS into Graph weekdays and
// an index ("" when none). Every day must carry the same ordinal, or none.
func byDay(value, setPos string) (days []string, index string, ok bool) {
	if value == "" {
		return nil, "", setPos == ""
	}

	ordinals := map[string]bool{}

	for _, d := range strings.Split(value, ",") {
		if len(d) < len("MO") {
			return nil, "", false
		}

		name := graphDay(d[len(d)-2:])
		if name == "" {
			return nil, "", false
		}

		ordinals[d[:len(d)-2]] = true
		days = append(days, name)
	}

	if len(ordinals) != 1 {
		return nil, "", false
	}

	for ord := range ordinals {
		index = ord
	}

	switch {
	case index != "" && setPos != "":
		return nil, "", false
	case setPos != "":
		index = setPos
	}

	if index == "" {
		return days, "", true
	}

	index = graphIndex(index)

	return days, index, index != ""
}
