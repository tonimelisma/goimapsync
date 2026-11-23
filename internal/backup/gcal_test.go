package backup_test

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gcal"
	"github.com/tonimelisma/pimvault/internal/gcal/gcaltest"
	"github.com/tonimelisma/pimvault/internal/state"
)

type gcalFixture struct {
	srv   *gcaltest.Server
	root  string
	run   *backup.GoogleCalendars
	state *state.Collections
}

func newGcalFixture(t *testing.T) *gcalFixture {
	t.Helper()

	srv := gcaltest.New(t)
	srv.AddCalendar(gcal.Calendar{ID: "user@gmail.example.com", Summary: "user@gmail.example.com", Primary: true, TimeZone: "Europe/Helsinki"}, "owner")

	root := filepath.Join(t.TempDir(), "calendars")

	return &gcalFixture{
		srv:  srv,
		root: root,
		run: &backup.GoogleCalendars{
			Client: gcal.New(http.DefaultClient, srv.URL),
			Store:  archive.NewStore(root, ".ics").KeepingAside(root + "-aside"),
			Logger: slog.New(slog.DiscardHandler),
			Owner:  "user@gmail.example.com",
		},
	}
}

func (f *gcalFixture) backup(t *testing.T) backup.Result {
	t.Helper()

	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)
	require.NotNil(t, next)
	f.state = next

	return res
}

func (f *gcalFixture) read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(path)))
	require.NoError(t, err)

	return string(data)
}

func timed(id, summary, day string) gcal.Event {
	return gcal.Event{
		ID:      id,
		ICalUID: id + "@google.com",
		Summary: summary,
		Start:   gcal.DateTime{DateTime: day + "T10:00:00+02:00", TimeZone: "Europe/Helsinki"},
		End:     gcal.DateTime{DateTime: day + "T11:00:00+02:00", TimeZone: "Europe/Helsinki"},
		Updated: day + "T00:00:00Z",
	}
}

func TestGoogleCalendars_FirstBackup(t *testing.T) {
	f := newGcalFixture(t)
	f.srv.AddCalendar(gcal.Calendar{ID: "work@group", Summary: "Work", DataOwner: "user@gmail.example.com"}, "owner")
	f.srv.AddCalendar(gcal.Calendar{ID: "work2@group", Summary: "Work", DataOwner: "User@Gmail.Example.com"}, "owner")
	f.srv.AddCalendar(gcal.Calendar{ID: "holidays@group", Summary: "Holidays"}, "reader")
	f.srv.AddCalendar(gcal.Calendar{ID: "family@group", Summary: "Family", DataOwner: "ann@example.com"}, "owner")
	f.srv.Put("family@group", timed("f1", "Shared", "2026-03-05"))
	f.srv.Put("user@gmail.example.com", timed("e1", "Dentist", "2026-03-01"))
	birthday := gcal.Event{ID: "b1", EventType: "birthday", Summary: "Ann", Start: gcal.DateTime{Date: "2026-05-05"}}
	birthday.BirthdayProperties.Contact = "people/c1"
	f.srv.Put("user@gmail.example.com", birthday)
	f.srv.Put("work@group", timed("e2", "Standup", "2026-03-02"))
	f.srv.Put("work2@group", timed("e3", "Other", "2026-03-03"))
	f.srv.Put("holidays@group", timed("h1", "Holiday", "2026-03-04"))

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 3, Downloaded: 3}, res)
	// Same names: the calendar whose ID sorts first keeps the plain name.
	assert.Equal(t, []string{"Calendar/e1.ics", "Work/e3.ics", "Work (2)/e2.ics"}, listFiles(t, f.root))
	assert.Contains(t, f.read(t, "Calendar/e1.ics"), "SUMMARY:Dentist\r\n")
}

func TestGoogleCalendars_SeriesChangesRewriteOneFile(t *testing.T) {
	f := newGcalFixture(t)
	master := timed("rec", "Weekly", "2026-01-05")
	master.Recurrence = []string{"RRULE:FREQ=WEEKLY"}
	f.srv.Put("user@gmail.example.com", master)
	f.srv.Put("user@gmail.example.com", timed("single", "Once", "2026-02-01"))
	f.backup(t)

	moved := timed("rec_20260112", "Weekly (moved)", "2026-01-13")
	moved.ICalUID = ""
	moved.RecurringEventID = "rec"
	moved.OriginalStartTime = gcal.DateTime{DateTime: "2026-01-12T10:00:00+02:00", TimeZone: "Europe/Helsinki"}
	f.srv.Put("user@gmail.example.com", moved)
	f.srv.Delete("user@gmail.example.com", "single")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1, Deleted: 1}, res)
	content := f.read(t, "Calendar/rec.ics")
	assert.Equal(t, 2, strings.Count(content, "BEGIN:VEVENT"))
	assert.Contains(t, content, "RECURRENCE-ID;TZID=Europe/Helsinki:20260112T100000\r\n")

	f.srv.Delete("user@gmail.example.com", "rec")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Deleted: 1}, res)
	assert.Empty(t, listFiles(t, f.root))
}

func TestGoogleCalendars_ExpiredTokenAndRename(t *testing.T) {
	f := newGcalFixture(t)
	f.srv.AddCalendar(gcal.Calendar{ID: "work@group", Summary: "Work", DataOwner: "user@gmail.example.com"}, "owner")
	f.srv.Put("work@group", timed("e1", "Standup", "2026-03-02"))
	f.backup(t)

	f.srv.Put("work@group", timed("e2", "Review", "2026-03-03"))
	f.srv.ExpireTokens()
	f.srv.RenameCalendar("work@group", "Job")

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 2, Downloaded: 2, Deleted: 1}, res)
	assert.Equal(t, []string{"Job/e1.ics", "Job/e2.ics"}, listFiles(t, f.root))

	f.srv.ExpireTokens()

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 2}, res, "a full listing rewrites nothing that did not change")
}

func TestGoogleCalendars_FailedSeriesIsReadAgain(t *testing.T) {
	f := newGcalFixture(t)
	f.srv.Put("user@gmail.example.com", timed("e1", "One", "2026-03-01"))
	f.backup(t)

	f.srv.Put("user@gmail.example.com", timed("e1", "One, edited", "2026-03-01"))
	f.srv.FailSeries["e1@google.com"] = http.StatusBadRequest

	res := f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Failed: 1}, res)

	delete(f.srv.FailSeries, "e1@google.com")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1}, res)
	assert.Contains(t, f.read(t, "Calendar/e1.ics"), "SUMMARY:One\\, edited\r\n")
}
