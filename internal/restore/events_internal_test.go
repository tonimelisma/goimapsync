package restore

import (
	"testing"

	"github.com/emersion/go-ical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadSeries_PinsFloatingTimes(t *testing.T) {
	raw := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:test\r\n" +
		// Floating series: pinned to the zone; its UNTIL becomes UTC.
		"BEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T100000\r\n" +
		"RRULE:FREQ=DAILY;UNTIL=20260110T100000\r\nEXDATE:20260107T100000\r\nEND:VEVENT\r\n" +
		// A changed instance with a floating RECURRENCE-ID follows the series.
		"BEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:20260101T000000Z\r\nRECURRENCE-ID:20260108T100000\r\n" +
		"DTSTART;TZID=W. Europe Standard Time:20260108T110000\r\nDTEND;VALUE=DATE:20260109\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n")

	s, err := readSeries(raw, "America/New_York")
	require.NoError(t, err)

	start := s.master.Props.Get(ical.PropDateTimeStart)
	assert.Equal(t, "America/New_York", start.Params.Get(ical.ParamTimezoneID))
	assert.Equal(t, "FREQ=DAILY;UNTIL=20260110T150000Z", s.master.Props.Get(ical.PropRecurrenceRule).Value)
	assert.Equal(t, "America/New_York", s.master.Props.Get(ical.PropExceptionDates).Params.Get(ical.ParamTimezoneID))

	ex := s.exceptions[0]
	assert.Equal(t, "America/New_York", ex.Props.Get(ical.PropRecurrenceID).Params.Get(ical.ParamTimezoneID))
	assert.Equal(t, "Europe/Berlin", ex.Props.Get(ical.PropDateTimeStart).Params.Get(ical.ParamTimezoneID), "Windows names are mapped")
	assert.Empty(t, ex.Props.Get(ical.PropDateTimeEnd).Params.Get(ical.ParamTimezoneID), "dates keep no zone")
}

func TestReadSeries_ChangedInstancesFollowTheSeriesZone(t *testing.T) {
	raw := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:test\r\n" +
		"BEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=Europe/Helsinki:20260105T090000\r\n" +
		"RRULE:FREQ=WEEKLY;COUNT=3\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:20260101T000000Z\r\nRECURRENCE-ID:20260112T090000\r\n" +
		"DTSTART:20260112T100000Z\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n")

	s, err := readSeries(raw, "America/Los_Angeles")
	require.NoError(t, err)

	ex := s.exceptions[0]
	assert.Equal(t, "Europe/Helsinki", ex.Props.Get(ical.PropRecurrenceID).Params.Get(ical.ParamTimezoneID))
	assert.Empty(t, ex.Props.Get(ical.PropDateTimeStart).Params.Get(ical.ParamTimezoneID), "UTC times keep theirs")
}

func TestReadSeries_WithoutZoneLeavesTimesAlone(t *testing.T) {
	raw := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:test\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20260105T100000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")

	s, err := readSeries(raw, "")
	require.NoError(t, err)
	assert.Empty(t, s.master.Props.Get(ical.PropDateTimeStart).Params.Get(ical.ParamTimezoneID))
}
