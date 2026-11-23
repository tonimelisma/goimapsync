package restore_test

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gcal"
	"github.com/tonimelisma/pimvault/internal/gcal/gcaltest"
	"github.com/tonimelisma/pimvault/internal/restore"
)

func ics(events ...string) []byte {
	return []byte(strings.ReplaceAll("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//test//EN\n"+strings.Join(events, "")+"END:VCALENDAR\n", "\n", "\r\n"))
}

const weekly = `BEGIN:VEVENT
UID:series-1
DTSTAMP:20260901T000000Z
DTSTART;TZID=Europe/Helsinki:20260906T140000
DTEND;TZID=Europe/Helsinki:20260906T143000
RRULE:FREQ=WEEKLY;COUNT=10
EXDATE;TZID=Europe/Helsinki:20260927T140000
SUMMARY:Weekly sync
DESCRIPTION:Agenda
ORGANIZER;CN=Other Outlook:mailto:other@outlook.example.com
ATTENDEE;CN=Ann;PARTSTAT=ACCEPTED;ROLE=REQ-PARTICIPANT:mailto:ann@example.com
ATTENDEE;PARTSTAT=NEEDS-ACTION;ROLE=OPT-PARTICIPANT:mailto:bob@example.com
CONFERENCE;VALUE=URI:https://meet.example.com/abc
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT15M
END:VALARM
BEGIN:VALARM
ACTION:EMAIL
TRIGGER:-P1D
END:VALARM
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;RELATED=END:PT5M
END:VALARM
END:VEVENT
BEGIN:VEVENT
UID:series-1
DTSTAMP:20260901T000000Z
RECURRENCE-ID;TZID=Europe/Helsinki:20260913T140000
DTSTART;TZID=Europe/Helsinki:20260914T160000
DTEND;TZID=Europe/Helsinki:20260914T170000
SUMMARY:Weekly sync (moved)
END:VEVENT
`

func TestGoogleCalendar_Restore(t *testing.T) {
	srv := gcaltest.New(t)
	srv.Owner = "me@gmail.example.com"
	srv.AddCalendar(gcal.Calendar{ID: "me@gmail.example.com", Summary: "me@gmail.example.com", Primary: true}, "owner")
	srv.Put("me@gmail.example.com", gcal.Event{ID: "old", ICalUID: "already-there", Summary: "Old"})

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "s1", ics(weekly)))
	require.NoError(t, source.Write(archive.Calendar, "old", ics("BEGIN:VEVENT\nUID:already-there\nDTSTAMP:20260901T000000Z\nDTSTART:20260901T100000Z\nSUMMARY:Old\nEND:VEVENT\n")))
	// The owner attends: the organizer is kept.
	require.NoError(t, source.Write("Team", "t1", ics(`BEGIN:VEVENT
UID:team-1
DTSTAMP:20260901T000000Z
DTSTART;VALUE=DATE:20261002
DTEND;VALUE=DATE:20261003
SUMMARY:Offsite
TRANSP:TRANSPARENT
CLASS:PRIVATE
ORGANIZER:mailto:boss@example.com
ATTENDEE;PARTSTAT=TENTATIVE:mailto:ME@gmail.example.com
END:VEVENT
`)))

	run := &restore.GoogleCalendar{
		Client: gcal.New(http.DefaultClient, srv.URL), Source: source, Owner: "me@gmail.example.com", Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 2, Present: 1}, res)

	imported := srv.Imported()
	require.Len(t, imported["me@gmail.example.com"], 2)
	assert.Equal(t, map[string]any{
		"iCalUID":     "series-1",
		"summary":     "Weekly sync",
		"description": "Agenda\n\nMeeting link: https://meet.example.com/abc",
		"organizer":   map[string]any{"email": "other@outlook.example.com", "displayName": "Other Outlook"},
		"start":       map[string]any{"dateTime": "2026-09-06T14:00:00", "timeZone": "Europe/Helsinki"},
		"end":         map[string]any{"dateTime": "2026-09-06T14:30:00", "timeZone": "Europe/Helsinki"},
		"recurrence":  []any{"RRULE:FREQ=WEEKLY;COUNT=10", "EXDATE;TZID=Europe/Helsinki:20260927T140000"},
		"attendees": []any{
			map[string]any{"email": "ann@example.com", "displayName": "Ann", "responseStatus": "accepted"},
			map[string]any{"email": "bob@example.com", "responseStatus": "needsAction", "optional": true},
			// Someone else organizes: the owner attends (D173).
			map[string]any{"email": "me@gmail.example.com", "responseStatus": "accepted"},
		},
		"reminders": map[string]any{"useDefault": false, "overrides": []any{
			map[string]any{"method": "popup", "minutes": 15.0}, map[string]any{"method": "email", "minutes": 1440.0},
		}},
	}, imported["me@gmail.example.com"][0])
	assert.Equal(t, map[string]any{"dateTime": "2026-09-13T14:00:00", "timeZone": "Europe/Helsinki"}, imported["me@gmail.example.com"][1]["originalStartTime"])

	calendars := srv.Calendars()
	require.Len(t, calendars, 2)
	assert.Equal(t, "Team", calendars[1].Summary)

	team := imported[calendars[1].ID]
	require.Len(t, team, 1)
	assert.Equal(t, map[string]any{"email": "boss@example.com", "displayName": ""}, team[0]["organizer"])
	assert.Equal(t, map[string]any{"date": "2026-10-02"}, team[0]["start"])
	assert.Equal(t, "transparent", team[0]["transparency"])
	assert.Equal(t, "private", team[0]["visibility"])

	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

func TestGoogleCalendar_FloatingTimesTakeTheCalendarsZone(t *testing.T) {
	srv := gcaltest.New(t)
	srv.AddCalendar(gcal.Calendar{ID: "me@gmail.example.com", Primary: true, TimeZone: "America/New_York"}, "owner")

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "f", ics(`BEGIN:VEVENT
UID:floating
DTSTAMP:20260901T000000Z
DTSTART:20261005T090000
DTEND;TZID=Nowhere/Unknown:20261005T100000
SUMMARY:Floating
END:VEVENT
`)))

	run := &restore.GoogleCalendar{
		Client: gcal.New(http.DefaultClient, srv.URL), Source: source, Owner: "me@gmail.example.com", Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)

	got := srv.Imported()["me@gmail.example.com"][0]
	assert.Equal(t, map[string]any{"dateTime": "2026-10-05T09:00:00", "timeZone": "America/New_York"}, got["start"])
	assert.Equal(t, map[string]any{"dateTime": "2026-10-05T10:00:00", "timeZone": "America/New_York"}, got["end"])
}

func TestGoogleCalendar_FailedInstanceIsRetriedWhole(t *testing.T) {
	srv := gcaltest.New(t)
	srv.AddCalendar(gcal.Calendar{ID: "me@gmail.example.com", Primary: true}, "owner")

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "s1", ics(weekly)))

	run := &restore.GoogleCalendar{
		Client: gcal.New(http.DefaultClient, srv.URL), Source: source, Owner: "other@outlook.example.com", Logger: slog.New(slog.DiscardHandler),
	}

	srv.FailImports["Weekly sync (moved)"] = http.StatusInternalServerError
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Failed: 1}, res)
	assert.Empty(t, srv.Events("me@gmail.example.com"), "the series is deleted again")

	clear(srv.FailImports)
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)
	assert.Len(t, srv.Events("me@gmail.example.com"), 2)
}

func TestGoogleCalendar_InstancesWithoutTheirSeriesAreRetriedWhole(t *testing.T) {
	srv := gcaltest.New(t)
	srv.AddCalendar(gcal.Calendar{ID: "me@gmail.example.com", Primary: true}, "owner")

	instance := func(day, summary string) string {
		return "BEGIN:VEVENT\nUID:orphans\nDTSTAMP:20260901T000000Z\nRECURRENCE-ID:202609" + day + "T100000Z\n" +
			"DTSTART:202609" + day + "T120000Z\nSUMMARY:" + summary + "\nEND:VEVENT\n"
	}

	source := archive.NewStore(filepath.Join(t.TempDir(), "calendars"), ".ics")
	require.NoError(t, source.Write(archive.Calendar, "o1", ics(instance("06", "First"), instance("13", "Second"))))

	run := &restore.GoogleCalendar{Client: gcal.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	srv.FailImports["Second"] = http.StatusInternalServerError
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Failed: 1}, res)
	assert.Empty(t, srv.Events("me@gmail.example.com"))

	clear(srv.FailImports)
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)
	assert.Len(t, srv.Events("me@gmail.example.com"), 2)
}
