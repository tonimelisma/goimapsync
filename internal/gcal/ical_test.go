package gcal_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/emersion/go-ical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/gcal"
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
}

func calendar() *gcal.Calendar {
	return &gcal.Calendar{
		ID:               "user@gmail.example.com",
		Primary:          true,
		TimeZone:         "Europe/Helsinki",
		DefaultReminders: []gcal.Reminder{{Method: "popup", Minutes: 30}},
	}
}

func TestEncode_TimedEventWithGuests(t *testing.T) {
	e := gcal.Event{
		ID:          "abc123",
		ICalUID:     "abc123@google.com",
		Status:      "confirmed",
		Summary:     "Planning, round 2",
		Description: "Agenda:\r\n1. Budget; 2. \"People\"",
		Location:    "Room 4",
		Start:       gcal.DateTime{DateTime: "2026-03-10T10:00:00+02:00", TimeZone: "Europe/Helsinki"},
		End:         gcal.DateTime{DateTime: "2026-03-10T11:00:00+02:00", TimeZone: "Europe/Helsinki"},
		Created:     "2026-02-01T08:00:00.000Z",
		Updated:     "2026-02-02T09:30:00.000Z",
		Sequence:    2,
		Visibility:  "private",
		Organizer:   gcal.Person{Email: "user@gmail.example.com", DisplayName: "User \"T\"\nM"},
		Attendees: []gcal.Attendee{
			{Email: "user@gmail.example.com", ResponseStatus: "accepted"},
			{Email: "ann@example.com", DisplayName: "Ann, A.", ResponseStatus: "tentative", Optional: true},
			{Email: "room@resource.example.com", ResponseStatus: "accepted", Resource: true},
		},
		HangoutLink: "https://meet.google.com/aaa-bbbb-ccc",
		Attachments: []gcal.Attachment{{FileURL: "https://drive.google.com/open?id=1", MimeType: "application/pdf"}},
	}
	e.Reminders.Overrides = []gcal.Reminder{{Method: "popup", Minutes: 10}, {Method: "email", Minutes: 1440}}
	e.ConferenceData.EntryPoints = []gcal.EntryPoint{
		{EntryPointType: "video", URI: "https://meet.google.com/aaa-bbbb-ccc", Label: "meet.google.com/aaa-bbbb-ccc"},
		{EntryPointType: "phone", URI: "tel:+358-9-1234", Label: "+358 9 1234"},
	}

	got, err := gcal.Encode([]gcal.Event{e}, calendar(), "user@gmail.example.com")
	require.NoError(t, err)
	golden(t, "timed", got)
}

func TestEncode_AllDayWithDefaultReminders(t *testing.T) {
	e := gcal.Event{
		ID:           "day1",
		ICalUID:      "day1@google.com",
		Status:       "confirmed",
		Summary:      "Holiday",
		Start:        gcal.DateTime{Date: "2026-07-01"},
		End:          gcal.DateTime{Date: "2026-07-15"},
		Updated:      "2026-06-01T00:00:00Z",
		Transparency: "transparent",
	}
	e.Reminders.UseDefault = true

	got, err := gcal.Encode([]gcal.Event{e}, calendar(), "user@gmail.example.com")
	require.NoError(t, err)
	golden(t, "allday", got)
}

func TestEncode_RecurringWithExceptions(t *testing.T) {
	master := gcal.Event{
		ID:         "rec1",
		ICalUID:    "rec1@google.com",
		Status:     "confirmed",
		Summary:    "Weekly",
		Start:      gcal.DateTime{DateTime: "2026-01-05T09:00:00+02:00", TimeZone: "Europe/Helsinki"},
		End:        gcal.DateTime{DateTime: "2026-01-05T09:30:00+02:00", TimeZone: "Europe/Helsinki"},
		Recurrence: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE;TZID=Europe/Helsinki:20260112T090000"},
		Updated:    "2026-01-01T00:00:00Z",
	}
	moved := gcal.Event{
		ID:                "rec1_20260119T070000Z",
		ICalUID:           "rec1@google.com",
		Status:            "confirmed",
		Summary:           "Weekly (moved)",
		RecurringEventID:  "rec1",
		OriginalStartTime: gcal.DateTime{DateTime: "2026-01-19T09:00:00+02:00", TimeZone: "Europe/Helsinki"},
		Start:             gcal.DateTime{DateTime: "2026-01-20T09:00:00+02:00", TimeZone: "Europe/Helsinki"},
		End:               gcal.DateTime{DateTime: "2026-01-20T09:30:00+02:00", TimeZone: "Europe/Helsinki"},
		Updated:           "2026-01-10T00:00:00Z",
	}
	cancelled := gcal.Event{
		ID:                "rec1_20260126T070000Z",
		Status:            "cancelled",
		RecurringEventID:  "rec1",
		OriginalStartTime: gcal.DateTime{DateTime: "2026-01-26T09:00:00+02:00", TimeZone: "Europe/Helsinki"},
	}

	got, err := gcal.Encode([]gcal.Event{cancelled, moved, master}, calendar(), "user@gmail.example.com")
	require.NoError(t, err)
	golden(t, "recurring", got)
	assert.Equal(t, "rec1", moved.SeriesID())
}

func TestEncode_NothingToStore(t *testing.T) {
	cases := map[string][]gcal.Event{
		"deleted event":     {{ID: "x", Status: "cancelled"}},
		"deleted series":    {{ID: "x", Status: "cancelled"}, {ID: "x_1", Status: "confirmed", RecurringEventID: "x", Start: gcal.DateTime{Date: "2026-01-01"}}},
		"contact birthday":  {contactBirthday()},
		"only cancellation": {{ID: "y_1", Status: "cancelled", RecurringEventID: "y"}},
	}

	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := gcal.Encode(events, calendar(), "user@gmail.example.com")
			require.NoError(t, err)
			assert.Nil(t, got)
		})
	}
}

func TestEncode_InstanceWithoutItsSeries(t *testing.T) {
	e := gcal.Event{
		ID:                "other_20260301",
		ICalUID:           "other@example.com",
		Status:            "confirmed",
		Summary:           "Invited instance",
		RecurringEventID:  "other",
		OriginalStartTime: gcal.DateTime{DateTime: "2026-03-01T12:00:00Z"},
		Start:             gcal.DateTime{DateTime: "2026-03-01T12:00:00Z", TimeZone: "UTC"},
		End:               gcal.DateTime{DateTime: "2026-03-01T13:00:00Z", TimeZone: "UTC"},
		Updated:           "2026-02-01T00:00:00Z",
	}

	got, err := gcal.Encode([]gcal.Event{e}, calendar(), "user@gmail.example.com")
	require.NoError(t, err)
	golden(t, "instance", got)
}

func TestEncode_UnknownZoneFallsBackToUTC(t *testing.T) {
	e := gcal.Event{
		ID: "z", ICalUID: "z@google.com", Status: "confirmed", Updated: "2026-01-01T00:00:00Z",
		Start: gcal.DateTime{DateTime: "2026-03-01T12:00:00+02:00", TimeZone: "Mars/Olympus"},
	}

	got, err := gcal.Encode([]gcal.Event{e}, &gcal.Calendar{}, "")
	require.NoError(t, err)
	assert.Contains(t, string(got), "DTSTART:20260301T100000Z\r\n")
}

func TestGoldenFilesParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.ics"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, path := range files {
		f, err := os.Open(path) //nolint:gosec // G304: test data path.
		require.NoError(t, err)

		cal, err := ical.NewDecoder(f).Decode()
		require.NoError(t, err, path)
		require.NoError(t, f.Close())
		assert.NotEmpty(t, cal.Events(), path)
	}
}

func contactBirthday() gcal.Event {
	e := gcal.Event{ID: "b", Status: "confirmed", EventType: "birthday", Start: gcal.DateTime{Date: "2026-05-05"}}
	e.BirthdayProperties.Contact = "people/c123"

	return e
}

func TestEncode_OwnBirthdayIsKept(t *testing.T) {
	e := gcal.Event{
		ID: "self", ICalUID: "self@google.com", Status: "confirmed", EventType: "birthday", Summary: "My birthday",
		Start: gcal.DateTime{Date: "2026-05-05"}, End: gcal.DateTime{Date: "2026-05-06"}, Updated: "2026-01-01T00:00:00Z",
		Recurrence: []string{"RRULE:FREQ=YEARLY"},
	}

	got, err := gcal.Encode([]gcal.Event{e}, calendar(), "user@gmail.example.com")
	require.NoError(t, err)
	assert.Contains(t, string(got), "SUMMARY:My birthday\r\n")
}
