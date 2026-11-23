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
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
)

func outlookCalendar(id, name string, isDefault bool) graph.Calendar {
	c := graph.Calendar{ID: id, Name: name, IsDefaultCalendar: isDefault, CanEdit: true, IsRemovable: !isDefault}
	c.Owner.Address = "Outlook_abc@outlook.example.com"

	return c
}

func outlookEvent(id, subject string) graph.Event {
	return graph.Event{
		ID: id, ICalUID: id + "-uid", Type: "singleInstance", Subject: subject,
		Start:                 graph.DateTimeZone{DateTime: "2026-03-01T08:00:00.0000000", TimeZone: "UTC"},
		End:                   graph.DateTimeZone{DateTime: "2026-03-01T09:00:00.0000000", TimeZone: "UTC"},
		OriginalStartTimeZone: "FLE Standard Time", OriginalEndTimeZone: "FLE Standard Time",
		LastModifiedDateTime: "2026-02-01T00:00:00Z",
	}
}

func TestOutlookCalendars(t *testing.T) {
	srv := graphtest.New(t)
	defaultCal := outlookCalendar("cal-default", "Calendar", true)
	defaultCal.Owner.Address = "outlook_ABC@outlook.example.com" // an alias of the sign-in address
	srv.AddCalendar(defaultCal)
	srv.AddCalendar(outlookCalendar("cal-work", "Work", false))

	birthdays := outlookCalendar("cal-bday", "Birthdays", false)
	birthdays.IsRemovable = false
	srv.AddCalendar(birthdays)

	shared := outlookCalendar("cal-shared", "Ann's calendar", false)
	shared.Owner.Address = "ann@example.com"
	srv.AddCalendar(shared)

	holidays := outlookCalendar("cal-holidays", "Holidays", false)
	holidays.CanEdit = false
	srv.AddCalendar(holidays)

	master := outlookEvent("m1", "Standup")
	master.Type = "seriesMaster"
	master.Recurrence = &graph.Recurrence{Pattern: graph.RecurrencePattern{Type: "daily", Interval: 1}, Range: graph.RecurrenceRange{Type: "noEnd"}}

	exception := outlookEvent("m1-x", "Standup (moved)")
	exception.Type = "exception"
	exception.OriginalStart = "2026-03-02T08:00:00Z"

	withFile := outlookEvent("e2", "Review")
	withFile.HasAttachments = true

	srv.SetEvents("cal-default", master, withFile)
	srv.SetSeries("m1", []graph.Event{exception}, []string{"OID.m1.2026-03-03"})
	srv.SetAttachments("e2", graph.Attachment{ODataType: "#microsoft.graph.fileAttachment", ContentType: "text/plain", ContentBytes: "aGk="})
	srv.SetEvents("cal-work", outlookEvent("e3", "Planning"))
	srv.SetEvents("cal-bday", outlookEvent("b1", "Ann"))
	srv.SetEvents("cal-shared", outlookEvent("s1", "Shared"))

	root := filepath.Join(t.TempDir(), "calendars")
	run := &backup.OutlookCalendars{
		Client: graph.New(http.DefaultClient, srv.URL),
		Store:  archive.NewStore(root, ".ics").KeepingAside(root + "-aside"),
		Logger: slog.New(slog.DiscardHandler),
		Owner:  "User@Outlook.Example.com",
	}

	res, st, err := run.Run(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, backup.Result{Files: 3, Downloaded: 3}, res)
	assert.Equal(t, []string{"Calendar/e2.ics", "Calendar/m1.ics", "Work/e3.ics"}, listFiles(t, root))

	series, err := os.ReadFile(filepath.Join(root, "Calendar", "m1.ics")) //nolint:gosec // G304: test path.
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(series), "BEGIN:VEVENT"))
	assert.Contains(t, string(series), "EXDATE;TZID=Europe/Kiev:20260303T100000\r\n")

	withAttachment, err := os.ReadFile(filepath.Join(root, "Calendar", "e2.ics")) //nolint:gosec // G304: test path.
	require.NoError(t, err)
	assert.Contains(t, string(withAttachment), ":aGk=\r\n")

	// Every run lists in full; unchanged events are not rewritten, removed
	// ones are deleted.
	srv.SetEvents("cal-work")

	res, _, err = run.Run(t.Context(), st)
	require.NoError(t, err)
	assert.Equal(t, backup.Result{Files: 2, Deleted: 1}, res)
}
