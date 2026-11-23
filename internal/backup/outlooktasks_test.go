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
	"github.com/tonimelisma/pimvault/internal/state"
)

type outlookTasksFixture struct {
	srv   *graphtest.Server
	root  string
	run   *backup.OutlookTasks
	state *state.Collections
}

func newOutlookTasksFixture(t *testing.T) *outlookTasksFixture {
	t.Helper()

	srv := graphtest.New(t)
	srv.AddTaskList(graph.TaskList{ID: "l-default", DisplayName: "Tasks", WellknownListName: graph.DefaultTaskList, IsOwner: true})
	root := filepath.Join(t.TempDir(), "tasks")

	return &outlookTasksFixture{
		srv:  srv,
		root: root,
		run: &backup.OutlookTasks{
			Client: graph.New(http.DefaultClient, srv.URL),
			Store:  archive.NewStore(root, ".ics").KeepingAside(root + "-aside"),
			Logger: slog.New(slog.DiscardHandler),
		},
	}
}

func (f *outlookTasksFixture) backup(t *testing.T) backup.Result {
	t.Helper()

	res, next, err := f.run.Run(t.Context(), f.state)
	require.NoError(t, err)
	require.NotNil(t, next)
	f.state = next

	return res
}

func (f *outlookTasksFixture) read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(path)))
	require.NoError(t, err)

	return strings.ReplaceAll(string(data), "\r\n ", "")
}

func todoTask(id, title string) graph.Task {
	return graph.Task{
		ID: id, Title: title, Status: "notStarted", Importance: "normal",
		CreatedDateTime: "2026-09-01T10:00:00Z", LastModifiedDateTime: "2026-09-02T10:00:00Z",
	}
}

func TestOutlookTasks_FirstBackup(t *testing.T) {
	f := newOutlookTasksFixture(t)
	f.srv.PageSize = 1
	f.srv.AddTaskList(graph.TaskList{ID: "l-groceries", DisplayName: "Groceries", WellknownListName: "none", IsOwner: true})
	f.srv.AddTaskList(graph.TaskList{ID: "l-flagged", DisplayName: "Flagged email", WellknownListName: graph.FlaggedEmailList, IsOwner: true})
	f.srv.AddTaskList(graph.TaskList{ID: "l-shared", DisplayName: "Ann's list", WellknownListName: "none"})

	weekly := todoTask("t1", "Water plants")
	weekly.Importance = "high"
	weekly.Body.Content = "Both rooms\r\n"
	weekly.Categories = []string{"Home"}
	weekly.DueDateTime = &graph.DateTimeZone{DateTime: "2026-10-02T00:00:00.0000000", TimeZone: "UTC"}
	weekly.IsReminderOn = true
	weekly.ReminderDateTime = &graph.DateTimeZone{DateTime: "2026-10-02T07:00:00.0000000", TimeZone: "UTC"}
	weekly.Recurrence = &graph.Recurrence{
		Pattern: graph.RecurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"friday"}, FirstDayOfWeek: "sunday"},
		Range:   graph.RecurrenceRange{Type: "noEnd", StartDate: "2026-10-02"},
	}
	weekly.ChecklistItems = []graph.ChecklistItem{
		{ID: "ci-2", DisplayName: "Bedroom", CreatedDateTime: "2026-09-01T10:02:00Z"},
		{ID: "ci-1", DisplayName: "Kitchen", IsChecked: true, CreatedDateTime: "2026-09-01T10:01:00Z", CheckedDateTime: "2026-09-03T08:00:00Z"},
	}
	f.srv.PutTask("l-default", weekly)

	done := todoTask("t2", "File taxes")
	done.Status = "completed"
	done.CompletedDateTime = &graph.DateTimeZone{DateTime: "2026-09-05T00:00:00.0000000", TimeZone: "UTC"}
	f.srv.PutTask("l-default", done)
	f.srv.PutTask("l-groceries", todoTask("t3", "Milk"))
	f.srv.PutTask("l-flagged", todoTask("t4", "Re: invoice"))
	f.srv.PutTask("l-shared", todoTask("t5", "Ann's task"))

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 3, Downloaded: 3}, res)
	assert.Equal(t, []string{"Groceries/t3.ics", "Tasks/t1.ics", "Tasks/t2.ics"}, listFiles(t, f.root))

	got := f.read(t, "Tasks/t1.ics")
	for _, want := range []string{
		"UID:t1\r\n", "SUMMARY:Water plants\r\n", "DESCRIPTION:Both rooms\r\n", "CATEGORIES:Home\r\n", "PRIORITY:1\r\n",
		"DUE;VALUE=DATE:20261002\r\n", "DTSTART;VALUE=DATE:20261002\r\n", "RRULE:FREQ=WEEKLY;BYDAY=FR;WKST=SU\r\n",
		"TRIGGER;VALUE=DATE-TIME:20261002T070000Z\r\n", "STATUS:NEEDS-ACTION\r\n",
		"RELATED-TO;RELTYPE=PARENT:t1\r\n", "COMPLETED:20260903T080000Z\r\n",
	} {
		assert.Contains(t, got, want)
	}

	assert.Equal(t, 3, strings.Count(got, "BEGIN:VTODO"), "the task and its two checklist items")
	assert.Less(t, strings.Index(got, "SUMMARY:Bedroom"), strings.Index(got, "SUMMARY:Kitchen"), "checklist items in Graph's order")

	taxes := f.read(t, "Tasks/t2.ics")
	assert.Contains(t, taxes, "STATUS:COMPLETED\r\n")
	assert.Contains(t, taxes, "COMPLETED:20260905T000000Z\r\n")
}

func TestOutlookTasks_Changes(t *testing.T) {
	f := newOutlookTasksFixture(t)
	task := todoTask("t1", "Water plants")
	task.ChecklistItems = []graph.ChecklistItem{{ID: "ci-1", DisplayName: "Kitchen", CreatedDateTime: "2026-09-01T10:01:00Z"}}
	f.srv.PutTask("l-default", task)
	f.srv.PutTask("l-default", todoTask("t2", "File taxes"))
	f.backup(t)

	task.ChecklistItems[0].IsChecked = true
	f.srv.PutTask("l-default", task)
	f.srv.DeleteTask("t2")

	res := f.backup(t)

	assert.Equal(t, backup.Result{Files: 1, Downloaded: 1, Deleted: 1}, res)
	assert.Contains(t, f.read(t, "Tasks/t1.ics"), "STATUS:COMPLETED\r\n", "the checked checklist item")

	res = f.backup(t)
	assert.Equal(t, backup.Result{Files: 1}, res)
}
