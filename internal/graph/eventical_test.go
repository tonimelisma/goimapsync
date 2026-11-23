package graph_test

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/graph"
)

var update = flag.Bool("update", false, "rewrite golden files") //nolint:gochecknoglobals // test flag.

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name+".ics")
	if *update {
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}

	want, err := os.ReadFile(path) //nolint:gosec // G304: test data path.
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))

	_, err = ical.NewDecoder(bytesReader(got)).Decode()
	require.NoError(t, err, "the file parses")
}

func meeting() graph.Event {
	e := graph.Event{
		ID:                    "AAMkMeeting=",
		ICalUID:               "040000008200E00074C5B7101A82E008",
		Type:                  "singleInstance",
		Subject:               "Budget review",
		Start:                 graph.DateTimeZone{DateTime: "2026-03-10T08:00:00.0000000", TimeZone: "UTC"},
		End:                   graph.DateTimeZone{DateTime: "2026-03-10T09:00:00.0000000", TimeZone: "UTC"},
		OriginalStartTimeZone: "FLE Standard Time",
		OriginalEndTimeZone:   "FLE Standard Time",
		ShowAs:                "busy",
		Sensitivity:           "private",
		Importance:            "high",
		Categories:            []string{"Work", "Finance, Q1"},
		IsReminderOn:          true,
		ReminderMinutes:       15,
		CreatedDateTime:       "2026-02-01T08:00:00.1234567Z",
		LastModifiedDateTime:  "2026-02-02T09:30:00Z",
		Attendees: []graph.EventAttendee{
			{EmailAddress: graph.EmailAddress{Name: "Ann", Address: "ann@example.com"}, Type: "required"},
			{EmailAddress: graph.EmailAddress{Name: "Room 4", Address: "room4@example.com"}, Type: "resource"},
		},
	}
	e.Body.Content = "Agenda\r\n1. Numbers\r\n"
	e.Location.DisplayName = "Room 4"
	e.Organizer.EmailAddress = graph.EmailAddress{Name: "User", Address: "user@outlook.example.com"}
	e.Attendees[0].Status.Response = "tentativelyAccepted"
	e.Attendees[1].Status.Response = "accepted"

	return e
}

func TestEncodeSeries_SingleEvent(t *testing.T) {
	e := meeting()
	attachments := map[string][]graph.Attachment{e.ID: {
		{ODataType: "#microsoft.graph.fileAttachment", Name: "a.txt", ContentType: "text/plain", ContentBytes: "aGVsbG8="},
		{ODataType: "#microsoft.graph.itemAttachment", Name: "mail"},
	}}

	got, err := graph.EncodeSeries(&e, nil, nil, attachments)
	require.NoError(t, err)
	golden(t, "single", got)
}

func TestEncodeSeries_AllDay(t *testing.T) {
	e := graph.Event{
		ID:                    "AAMkDay=",
		ICalUID:               "day-uid",
		Subject:               "Holiday",
		IsAllDay:              true,
		ShowAs:                "free",
		Start:                 graph.DateTimeZone{DateTime: "2026-06-30T21:00:00.0000000", TimeZone: "UTC"},
		End:                   graph.DateTimeZone{DateTime: "2026-07-01T21:00:00.0000000", TimeZone: "UTC"},
		OriginalStartTimeZone: "FLE Standard Time",
		OriginalEndTimeZone:   "FLE Standard Time",
		LastModifiedDateTime:  "2026-06-01T00:00:00Z",
	}

	got, err := graph.EncodeSeries(&e, nil, nil, nil)
	require.NoError(t, err)
	golden(t, "allday", got)
}

func TestEncodeSeries_SeriesWithExceptions(t *testing.T) {
	e := graph.Event{
		ID:                    "AAMkMaster=",
		ICalUID:               "series-uid",
		Type:                  "seriesMaster",
		Subject:               "Standup",
		Start:                 graph.DateTimeZone{DateTime: "2026-01-05T07:00:00.0000000", TimeZone: "UTC"},
		End:                   graph.DateTimeZone{DateTime: "2026-01-05T07:15:00.0000000", TimeZone: "UTC"},
		OriginalStartTimeZone: "FLE Standard Time",
		OriginalEndTimeZone:   "FLE Standard Time",
		LastModifiedDateTime:  "2026-01-01T00:00:00Z",
	}
	e.Recurrence = &graph.Recurrence{
		Pattern: graph.RecurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"monday", "wednesday"}, FirstDayOfWeek: "monday"},
		Range:   graph.RecurrenceRange{Type: "endDate", StartDate: "2026-01-05", EndDate: "2026-06-30", RecurrenceTimeZone: "FLE Standard Time"},
	}

	moved := graph.Event{
		ID:                    "AAMkException=",
		ICalUID:               "occurrence-uid-differs",
		Type:                  "exception",
		SeriesMasterID:        "AAMkMaster=",
		Subject:               "Standup (moved)",
		Start:                 graph.DateTimeZone{DateTime: "2026-01-07T09:00:00.0000000", TimeZone: "UTC"},
		End:                   graph.DateTimeZone{DateTime: "2026-01-07T09:15:00.0000000", TimeZone: "UTC"},
		OriginalStart:         "2026-01-07T07:00:00Z",
		OriginalStartTimeZone: "FLE Standard Time",
		OriginalEndTimeZone:   "FLE Standard Time",
		LastModifiedDateTime:  "2026-01-06T00:00:00Z",
	}

	got, err := graph.EncodeSeries(&e, []graph.Event{moved}, []string{"OID.AAMkMaster=.2026-01-12"}, nil)
	require.NoError(t, err)
	golden(t, "series", got)
}

func TestEncodeSeries_Patterns(t *testing.T) {
	cases := map[string]struct {
		pattern graph.RecurrencePattern
		rng     graph.RecurrenceRange
		want    string
	}{
		"daily numbered": {
			graph.RecurrencePattern{Type: "daily", Interval: 2},
			graph.RecurrenceRange{Type: "numbered", NumberOfOccurrences: 10},
			"RRULE:FREQ=DAILY;INTERVAL=2;COUNT=10",
		},
		"absolute monthly": {
			graph.RecurrencePattern{Type: "absoluteMonthly", Interval: 1, DayOfMonth: 15},
			graph.RecurrenceRange{Type: "noEnd"},
			"RRULE:FREQ=MONTHLY;BYMONTHDAY=15",
		},
		"relative monthly": {
			graph.RecurrencePattern{Type: "relativeMonthly", Interval: 3, DaysOfWeek: []string{"thursday"}, Index: "last"},
			graph.RecurrenceRange{Type: "noEnd"},
			"RRULE:FREQ=MONTHLY;BYDAY=TH;BYSETPOS=-1;INTERVAL=3",
		},
		"absolute yearly": {
			graph.RecurrencePattern{Type: "absoluteYearly", Interval: 1, Month: 3, DayOfMonth: 15},
			graph.RecurrenceRange{Type: "noEnd"},
			"RRULE:FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=15",
		},
		"relative yearly": {
			graph.RecurrencePattern{Type: "relativeYearly", Interval: 1, Month: 11, DaysOfWeek: []string{"thursday"}, Index: "fourth"},
			graph.RecurrenceRange{Type: "noEnd"},
			"RRULE:FREQ=YEARLY;BYMONTH=11;BYDAY=TH;BYSETPOS=4",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := graph.Event{
				ID: "x", ICalUID: "x", Subject: "x", LastModifiedDateTime: "2026-01-01T00:00:00Z",
				Start: graph.DateTimeZone{DateTime: "2026-01-01T10:00:00.0000000", TimeZone: "UTC"},
			}
			e.Recurrence = &graph.Recurrence{tc.pattern, tc.rng}

			got, err := graph.EncodeSeries(&e, nil, nil, nil)
			require.NoError(t, err)
			assert.Contains(t, string(got), tc.want+"\r\n")
		})
	}
}

func TestEncodeSeries_UnknownZoneIsUTC(t *testing.T) {
	e := graph.Event{
		ID: "x", ICalUID: "x", LastModifiedDateTime: "2026-01-01T00:00:00Z", OriginalStartTimeZone: "tzone://Microsoft/Custom",
		Start: graph.DateTimeZone{DateTime: "2026-01-01T10:00:00.0000000", TimeZone: "UTC"},
	}

	got, err := graph.EncodeSeries(&e, nil, nil, nil)
	require.NoError(t, err)
	assert.Contains(t, string(got), "DTSTART:20260101T100000Z\r\n")
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func TestEncodeSeries_UIDFromOtherSystems(t *testing.T) {
	// A global object ID wrapping a Google UID (MS-OXOCAL PidLidGlobalObjectId).
	data := append([]byte("vCal-Uid\x01\x00\x00\x00"), []byte("abc123@google.com\x00")...)
	goid := "040000008200E00074C5B7101A82E008" + "00000000" + "0000000000000000" + "0000000000000000" +
		fmt.Sprintf("%02X000000", len(data)) + strings.ToUpper(hex.EncodeToString(data))

	for name, tc := range map[string]struct{ icaluid, want string }{
		"external meeting": {goid, "abc123@google.com"},
		"outlook's own":    {"040000008200E00074C5B7101A82E00800000000", "040000008200E00074C5B7101A82E00800000000"},
		"not hex":          {"plain-uid", "plain-uid"},
	} {
		t.Run(name, func(t *testing.T) {
			e := graph.Event{
				ID: "x", ICalUID: tc.icaluid, LastModifiedDateTime: "2026-01-01T00:00:00Z",
				Start: graph.DateTimeZone{DateTime: "2026-01-01T10:00:00.0000000", TimeZone: "UTC"},
			}

			got, err := graph.EncodeSeries(&e, nil, nil, nil)
			require.NoError(t, err)
			assert.Contains(t, string(got), "UID:"+tc.want+"\r\n")
		})
	}
}

func TestEncodeSeries_AllDayAsFloatingMidnight(t *testing.T) {
	e := graph.Event{
		ID: "x", ICalUID: "x", IsAllDay: true, LastModifiedDateTime: "2026-01-01T00:00:00Z",
		Start:                 graph.DateTimeZone{DateTime: "2026-07-01T00:00:00.0000000", TimeZone: "UTC"},
		End:                   graph.DateTimeZone{DateTime: "2026-07-02T00:00:00.0000000", TimeZone: "UTC"},
		OriginalStartTimeZone: "Pacific Standard Time",
	}

	got, err := graph.EncodeSeries(&e, nil, nil, nil)
	require.NoError(t, err)
	assert.Contains(t, string(got), "DTSTART;VALUE=DATE:20260701\r\n")
	assert.Contains(t, string(got), "DTEND;VALUE=DATE:20260702\r\n")
}

func TestEncodeSeries_AllDayCanceledDateIsTheOccurrenceDate(t *testing.T) {
	e := graph.Event{
		ID: "m", ICalUID: "m", Type: "seriesMaster", IsAllDay: true, LastModifiedDateTime: "2026-01-01T00:00:00Z",
		Start:                 graph.DateTimeZone{DateTime: "2026-01-01T00:00:00.0000000", TimeZone: "UTC"},
		End:                   graph.DateTimeZone{DateTime: "2026-01-02T00:00:00.0000000", TimeZone: "UTC"},
		OriginalStartTimeZone: "Pacific Standard Time",
	}
	e.Recurrence = &graph.Recurrence{
		Pattern: graph.RecurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"thursday"}},
		Range:   graph.RecurrenceRange{Type: "noEnd", RecurrenceTimeZone: "Pacific Standard Time"},
	}

	got, err := graph.EncodeSeries(&e, nil, []string{"OID.m.2026-01-08"}, nil)
	require.NoError(t, err)
	assert.Contains(t, string(got), "EXDATE;VALUE=DATE:20260108\r\n")
}

func TestEncodeSeries_CanceledDatesUseTheRecurrenceZoneAndStableOrder(t *testing.T) {
	e := graph.Event{
		ID: "m", ICalUID: "m", Type: "seriesMaster", LastModifiedDateTime: "2026-01-01T00:00:00Z",
		// 01:00 in Helsinki is 23:00 UTC the day before.
		Start:                 graph.DateTimeZone{DateTime: "2026-01-04T23:00:00.0000000", TimeZone: "UTC"},
		OriginalStartTimeZone: "FLE Standard Time",
	}
	e.Recurrence = &graph.Recurrence{
		Pattern: graph.RecurrencePattern{Type: "daily", Interval: 1},
		Range:   graph.RecurrenceRange{Type: "noEnd", RecurrenceTimeZone: "FLE Standard Time"},
	}

	a, err := graph.EncodeSeries(&e, nil, []string{"OID.m.2026-01-09", "OID.m.2026-01-07"}, nil)
	require.NoError(t, err)

	b, err := graph.EncodeSeries(&e, nil, []string{"OID.m.2026-01-07", "OID.m.2026-01-09"}, nil)
	require.NoError(t, err)

	assert.Equal(t, string(a), string(b))
	assert.Contains(t, string(a), "EXDATE;TZID=Europe/Kiev:20260107T010000\r\nEXDATE;TZID=Europe/Kiev:20260109T010000\r\n")
}
