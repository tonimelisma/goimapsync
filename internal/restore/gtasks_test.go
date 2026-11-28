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
	"github.com/tonimelisma/pimvault/internal/gtasks"
	"github.com/tonimelisma/pimvault/internal/gtasks/gtaskstest"
	"github.com/tonimelisma/pimvault/internal/restore"
)

// Validates: PV-158, PV-170, PV-184
func TestGoogleTasks_Restore(t *testing.T) {
	srv := gtaskstest.New(t)
	srv.AddList(gtasks.List{ID: "l-default", Title: "My Tasks"})
	srv.Put("l-default", gtasks.Task{ID: "g0", Title: "Already there", Status: "needsAction"}, false)

	source := archive.NewStore(filepath.Join(t.TempDir(), "tasks"), ".ics")
	write := func(folder, id string, data []byte, err error) {
		require.NoError(t, err)
		require.NoError(t, source.Write(folder, id, data))
	}

	data, err := gtasks.Encode(&gtasks.Task{ID: "g0", Title: "Already there", Status: "needsAction"})
	write(archive.Tasks, "g0", data, err)

	// A To Do task with a checklist, a due date, a weekly repeat and a
	// reminder: Google keeps the date and the checklist as subtasks.
	todo := graph.Task{
		ID: "t1", Title: "Water plants", Status: "completed", Importance: "high",
		DueDateTime:       &graph.DateTimeZone{DateTime: "2026-10-02T00:00:00.0000000", TimeZone: "UTC"},
		CompletedDateTime: &graph.DateTimeZone{DateTime: "2026-10-02T09:00:00.0000000", TimeZone: "UTC"},
		Recurrence: &graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"friday"}},
			Range:   graph.RecurrenceRange{Type: "noEnd", StartDate: "2026-10-02"},
		},
		ChecklistItems: []graph.ChecklistItem{{ID: "ci-1", DisplayName: "Kitchen", IsChecked: true}},
	}
	todo.Body.Content = "Both rooms"
	data, err = graph.EncodeTask(&todo)
	write("Home", "t1", data, err)

	run := &restore.GoogleTasks{Client: gtasks.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1, Present: 1}, res)

	lists := srv.Lists()
	require.Len(t, lists, 2)
	assert.Equal(t, "Home", lists[1].Title)

	assert.Equal(t, []gtasks.Task{
		{ID: "t-new-1", Title: "Water plants", Notes: "Both rooms", Status: "completed", Due: "2026-10-02T00:00:00Z", Completed: "2026-10-02T09:00:00Z"},
		{ID: "t-new-2", Title: "Kitchen", Status: "completed", Parent: "t-new-1"},
	}, srv.Tasks(lists[1].ID))

	// Running again finds the task by title and due date (D160).
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 2}, res)
}

// Validates: PV-183
func TestGoogleTasks_SubtasksKeepOrderAndFailedTaskIsRetriedWhole(t *testing.T) {
	srv := gtaskstest.New(t)
	srv.AddList(gtasks.List{ID: "l-default", Title: "My Tasks"})
	// A user list titled like the default directory: tasks/Tasks/ still
	// goes to the default list.
	srv.AddList(gtasks.List{ID: "l-user", Title: "Tasks"})

	source := archive.NewStore(filepath.Join(t.TempDir(), "tasks"), ".ics")
	todo := graph.Task{ID: "t1", Title: "Pack", Status: "notStarted", ChecklistItems: []graph.ChecklistItem{
		{ID: "ci-1", DisplayName: "Passport"}, {ID: "ci-2", DisplayName: "Charger"}, {ID: "ci-3", DisplayName: "Socks"},
	}}
	data, err := graph.EncodeTask(&todo)
	require.NoError(t, err)
	require.NoError(t, source.Write(archive.Tasks, "t1", data))

	run := &restore.GoogleTasks{Client: gtasks.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	srv.FailCreate["Charger"] = http.StatusInternalServerError
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Failed: 1}, res)
	assert.Empty(t, srv.Tasks("l-default"), "the half-written task is deleted again")

	delete(srv.FailCreate, "Charger")
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)

	var titles []string
	for _, task := range srv.Tasks("l-default") {
		titles = append(titles, task.Title)
	}

	assert.Equal(t, []string{"Pack", "Passport", "Charger", "Socks"}, titles)
	assert.Empty(t, srv.Tasks("l-user"))
}
