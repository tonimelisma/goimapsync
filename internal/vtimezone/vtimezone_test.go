package vtimezone_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/vtimezone"
)

func encode(t *testing.T, comp *ical.Component) string {
	t.Helper()

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, "-//test//EN")
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Children = append(cal.Children, comp)

	var buf bytes.Buffer
	require.NoError(t, ical.NewEncoder(&buf).Encode(cal))

	return buf.String()
}

func TestComponent(t *testing.T) {
	cases := map[string]struct {
		tzid string
		want string
	}{
		"europe": {"Europe/Helsinki", "" +
			"BEGIN:VTIMEZONE\r\nTZID:Europe/Helsinki\r\n" +
			"BEGIN:DAYLIGHT\r\nDTSTART:20250330T030000\r\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU\r\n" +
			"TZNAME:EEST\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0300\r\nEND:DAYLIGHT\r\n" +
			"BEGIN:STANDARD\r\nDTSTART:20251026T040000\r\nRRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU\r\n" +
			"TZNAME:EET\r\nTZOFFSETFROM:+0300\r\nTZOFFSETTO:+0200\r\nEND:STANDARD\r\n" +
			"END:VTIMEZONE\r\n"},
		"america": {"America/New_York", "" +
			"BEGIN:VTIMEZONE\r\nTZID:America/New_York\r\n" +
			"BEGIN:DAYLIGHT\r\nDTSTART:20250309T020000\r\nRRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=2SU\r\n" +
			"TZNAME:EDT\r\nTZOFFSETFROM:-0500\r\nTZOFFSETTO:-0400\r\nEND:DAYLIGHT\r\n" +
			"BEGIN:STANDARD\r\nDTSTART:20251102T020000\r\nRRULE:FREQ=YEARLY;BYMONTH=11;BYDAY=1SU\r\n" +
			"TZNAME:EST\r\nTZOFFSETFROM:-0400\r\nTZOFFSETTO:-0500\r\nEND:STANDARD\r\n" +
			"END:VTIMEZONE\r\n"},
		"southern": {"Australia/Sydney", "" +
			"BEGIN:VTIMEZONE\r\nTZID:Australia/Sydney\r\n" +
			"BEGIN:STANDARD\r\nDTSTART:20250406T030000\r\nRRULE:FREQ=YEARLY;BYMONTH=4;BYDAY=1SU\r\n" +
			"TZNAME:AEST\r\nTZOFFSETFROM:+1100\r\nTZOFFSETTO:+1000\r\nEND:STANDARD\r\n" +
			"BEGIN:DAYLIGHT\r\nDTSTART:20251005T020000\r\nRRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=1SU\r\n" +
			"TZNAME:AEDT\r\nTZOFFSETFROM:+1000\r\nTZOFFSETTO:+1100\r\nEND:DAYLIGHT\r\n" +
			"END:VTIMEZONE\r\n"},
		"no daylight time": {"Asia/Tokyo", "" +
			"BEGIN:VTIMEZONE\r\nTZID:Asia/Tokyo\r\n" +
			"BEGIN:STANDARD\r\nDTSTART:19700101T000000\r\nTZNAME:JST\r\nTZOFFSETFROM:+0900\r\nTZOFFSETTO:+0900\r\nEND:STANDARD\r\n" +
			"END:VTIMEZONE\r\n"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			comp, err := vtimezone.Component(tc.tzid, 2026)
			require.NoError(t, err)

			got := encode(t, comp)
			assert.Contains(t, got, tc.want)
		})
	}
}

// The yearly rules must reproduce the real changes of the following years.
func TestComponent_RulesMatchTheZoneDatabase(t *testing.T) {
	for _, tzid := range []string{"Europe/Helsinki", "Europe/London", "America/New_York", "America/Los_Angeles", "Australia/Sydney", "America/Santiago"} {
		comp, err := vtimezone.Component(tzid, 2026)
		require.NoError(t, err, tzid)

		loc, err := time.LoadLocation(tzid)
		require.NoError(t, err)

		checked := 0

		for _, obs := range comp.Children {
			set, err := obs.RecurrenceSet(time.UTC)
			require.NoError(t, err, tzid)

			if set == nil {
				continue
			}

			for _, local := range set.Between(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC), true) {
				from := obs.Props.Get(ical.PropTimezoneOffsetFrom).Value
				to := obs.Props.Get(ical.PropTimezoneOffsetTo).Value
				before := wallOffset(t, local, from).Add(-time.Minute)
				after := wallOffset(t, local, from).Add(time.Minute)

				assert.Equal(t, from, offsetOf(before.In(loc)), "%s before %s", tzid, local)
				assert.Equal(t, to, offsetOf(after.In(loc)), "%s after %s", tzid, local)

				checked++
			}
		}

		assert.Equal(t, 6, checked, "%s: two changes a year for three years", tzid)
	}
}

// wallOffset turns a wall-clock time given as UTC fields into the instant it
// names in a zone with the given +hhmm offset.
func wallOffset(t *testing.T, local time.Time, offset string) time.Time {
	t.Helper()

	sign := 1
	if offset[0] == '-' {
		sign = -1
	}

	h, m := int(offset[1]-'0')*10+int(offset[2]-'0'), int(offset[3]-'0')*10+int(offset[4]-'0')

	return local.Add(-time.Duration(sign*(h*60+m)) * time.Minute)
}

func offsetOf(t time.Time) string {
	return t.Format("-0700")
}

func TestAdd(t *testing.T) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, "-//test//EN")
	cal.Props.SetText(ical.PropVersion, "2.0")

	event := ical.NewComponent(ical.CompEvent)
	event.Props.SetText(ical.PropUID, "x")
	event.Props.SetDateTime(ical.PropDateTimeStamp, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	event.Props.SetDateTime(ical.PropDateTimeStart, time.Date(2024, 6, 1, 9, 0, 0, 0, helsinki))

	exdate := ical.NewProp(ical.PropExceptionDates)
	exdate.Params.Set(ical.PropTimezoneID, "Mars/Olympus")
	exdate.Value = "20240608T090000"
	event.Props.Add(exdate)

	cal.Children = append(cal.Children, event)

	vtimezone.Add(cal)

	require.Len(t, cal.Children, 2)
	assert.Equal(t, ical.CompTimezone, cal.Children[0].Name)
	assert.Equal(t, "Europe/Helsinki", cal.Children[0].Props.Get(ical.PropTimezoneID).Value)
	assert.Equal(t, "20230326T030000", cal.Children[0].Children[0].Props.Get(ical.PropDateTimeStart).Value,
		"rules of the earliest year used, starting the year before")
}
