package restore_test

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
	"github.com/tonimelisma/pimvault/internal/restore"
)

const sourceID = "String {7a5d4f0e-2c1b-4a6e-9f3d-5b8c1e2a7d90} Name pimvaultSource"

func TestOutlookCalendar_Restore(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddCalendar(graph.Calendar{ID: "cal-default", Name: "Calendar", IsDefaultCalendar: true, CanEdit: true})
	srv.SetEvents("cal-default", graph.Event{ID: "old", ICalUID: "already-there", Subject: "Old"})

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "s1", ics(weekly)))
	require.NoError(t, source.Write(archive.Calendar, "old", ics("BEGIN:VEVENT\nUID:already-there\nDTSTAMP:20260901T000000Z\nDTSTART:20260901T100000Z\nSUMMARY:Old\nEND:VEVENT\n")))
	// Twice a day: Outlook cannot repeat that way, so it is expanded (D63).
	require.NoError(t, source.Write("Shifts", "x1", ics(`BEGIN:VEVENT
UID:twice-daily
DTSTAMP:20260901T000000Z
DTSTART:20261001T090000Z
DTEND:20261001T093000Z
RRULE:FREQ=DAILY;BYHOUR=9,17;COUNT=4
SUMMARY:Check
END:VEVENT
`)))

	run := &restore.OutlookCalendar{
		Client: graph.New(http.DefaultClient, srv.URL), Source: source, Owner: "me@outlook.example.com", Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 2, Present: 1}, res)

	written := srv.WrittenEvents()
	require.Len(t, written, 5)

	sourceProp := func(uid string) []any { return []any{map[string]any{"id": sourceID, "value": uid}} }

	assert.Equal(t, map[string]any{
		"calendar": "cal-default",
		"subject":  "Weekly sync",
		"body": map[string]any{
			"contentType": "text",
			"content":     "Agenda\n\nOrganizer: Other Outlook <other@outlook.example.com>\nMeeting link: https://meet.example.com/abc",
		},
		"start": map[string]any{"dateTime": "2026-09-06T14:00:00", "timeZone": "Europe/Helsinki"},
		"end":   map[string]any{"dateTime": "2026-09-06T14:30:00", "timeZone": "Europe/Helsinki"},
		"attendees": []any{
			map[string]any{"emailAddress": map[string]any{"address": "ann@example.com", "name": "Ann"}, "type": "required"},
			map[string]any{"emailAddress": map[string]any{"address": "bob@example.com"}, "type": "optional"},
		},
		"isDraft": true,
		"recurrence": map[string]any{
			"pattern": map[string]any{"type": "weekly", "interval": 1.0, "daysOfWeek": []any{"sunday"}},
			"range": map[string]any{
				"type": "numbered", "startDate": "2026-09-06", "numberOfOccurrences": 10.0, "recurrenceTimeZone": "Europe/Helsinki",
			},
		},
		"isReminderOn":                  true,
		"reminderMinutesBeforeStart":    15.0,
		"singleValueExtendedProperties": sourceProp("series-1"),
	}, written["ev-new-1"])

	assert.Equal(t, []string{"ev-new-1_20260927T110000"}, srv.DeletedOccurrences())
	moved := srv.PatchedOccurrences()["ev-new-1_20260913T110000"]
	assert.Equal(t, "Weekly sync (moved)", moved["subject"])
	assert.Equal(t, map[string]any{"dateTime": "2026-09-14T16:00:00", "timeZone": "Europe/Helsinki"}, moved["start"])
	assert.NotContains(t, moved, "attendees")

	calendars := srv.Calendars()
	require.Len(t, calendars, 2)
	assert.Equal(t, "Shifts", calendars[1].Name)

	var starts []any

	for _, id := range []string{"ev-new-3", "ev-new-4", "ev-new-5", "ev-new-6"} {
		e := written[id]
		assert.Equal(t, calendars[1].ID, e["calendar"])
		assert.NotContains(t, e, "recurrence")
		assert.Equal(t, sourceProp("twice-daily"), e["singleValueExtendedProperties"])
		starts = append(starts, e["start"])
	}

	at := func(dateTime string) map[string]any { return map[string]any{"dateTime": dateTime, "timeZone": "UTC"} }
	assert.Equal(t, []any{
		at("2026-10-01T09:00:00"), at("2026-10-01T17:00:00"), at("2026-10-02T09:00:00"), at("2026-10-02T17:00:00"),
	}, starts)

	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

func TestOutlookCalendar_FloatingTimesTakeTheMailboxZone(t *testing.T) {
	srv := graphtest.New(t)
	srv.TimeZone = "W. Europe Standard Time" // Outlook names Windows zones
	srv.AddCalendar(graph.Calendar{ID: "cal-default", Name: "Calendar", IsDefaultCalendar: true, CanEdit: true})

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "f", ics(`BEGIN:VEVENT
UID:floating
DTSTAMP:20260901T000000Z
DTSTART:20261005T090000
DTEND:20261005T100000
SUMMARY:Floating
END:VEVENT
`)))

	run := &restore.OutlookCalendar{
		Client: graph.New(http.DefaultClient, srv.URL), Source: source, Owner: "me@outlook.example.com", Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)

	require.Len(t, srv.WrittenEvents(), 1)

	for _, e := range srv.WrittenEvents() {
		assert.Equal(t, map[string]any{"dateTime": "2026-10-05T09:00:00", "timeZone": "Europe/Berlin"}, e["start"])
	}
}

func TestOutlookCalendar_FailedOccurrenceIsRetriedWhole(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddCalendar(graph.Calendar{ID: "cal-default", IsDefaultCalendar: true, CanEdit: true})

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "s1", ics(weekly)))

	run := &restore.OutlookCalendar{
		Client: graph.New(http.DefaultClient, srv.URL), Source: source, Owner: "other@outlook.example.com", Logger: slog.New(slog.DiscardHandler),
	}

	srv.FailWrites["PATCH /events/ev-new-1_20260913T110000"] = http.StatusInternalServerError
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Failed: 1}, res)
	assert.Empty(t, srv.WrittenEvents(), "the series is deleted again")

	clear(srv.FailWrites)
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)
}

func TestOutlookCalendar_CalendarFailureFailsItsEvents(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddCalendar(graph.Calendar{ID: "cal-default", IsDefaultCalendar: true, CanEdit: true})
	srv.FailWrites["POST /calendars"] = http.StatusForbidden

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	for _, id := range []string{"a", "b"} {
		require.NoError(t, source.Write("Team", id, ics("BEGIN:VEVENT\nUID:"+id+"\nDTSTAMP:20260901T000000Z\nDTSTART:20260901T100000Z\nSUMMARY:x\nEND:VEVENT\n")))
	}

	run := &restore.OutlookCalendar{Client: graph.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Failed: 2}, res)
}

func TestOutlookCalendar_CappedSeriesLeavesOutEarlyChanges(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddCalendar(graph.Calendar{ID: "cal-default", Name: "Calendar", IsDefaultCalendar: true, CanEdit: true})

	// Twice a day, 502 times: the first two occurrences fall to the cap
	// (D172), and so does the change to the first one.
	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "x1", ics(`BEGIN:VEVENT
UID:twice-daily
DTSTAMP:20260901T000000Z
DTSTART:20261001T090000Z
DTEND:20261001T093000Z
RRULE:FREQ=DAILY;BYHOUR=9,17;COUNT=502
SUMMARY:Check
END:VEVENT
BEGIN:VEVENT
UID:twice-daily
DTSTAMP:20260901T000000Z
RECURRENCE-ID:20261001T090000Z
DTSTART:20261001T100000Z
DTEND:20261001T103000Z
SUMMARY:Check (moved)
END:VEVENT
`)))

	run := &restore.OutlookCalendar{
		Client: graph.New(http.DefaultClient, srv.URL), Source: source, Owner: "me@outlook.example.com", Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)

	written := srv.WrittenEvents()
	assert.Len(t, written, 500)

	for _, e := range written {
		assert.Equal(t, "Check", e["subject"])
	}
}
