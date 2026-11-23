package restore_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
	"github.com/tonimelisma/pimvault/internal/gtasks"
	"github.com/tonimelisma/pimvault/internal/restore"
)

func TestOutlookTasks_Restore(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddTaskList(graph.TaskList{ID: "l-default", DisplayName: "Tasks", WellknownListName: graph.DefaultTaskList, IsOwner: true})
	srv.PutTask("l-default", graph.Task{ID: "t0", Title: "Already there"})

	source := archive.NewStore(filepath.Join(t.TempDir(), "tasks"), ".ics")
	write := func(folder, id string, data []byte, err error) {
		require.NoError(t, err)
		require.NoError(t, source.Write(folder, id, data))
	}

	// An Outlook task with its checklist, backed up from To Do.
	weekly := graph.Task{
		ID: "t1", Title: "Water plants", Status: "notStarted", Importance: "high", Categories: []string{"Home"},
		CreatedDateTime: "2026-09-01T10:00:00Z", LastModifiedDateTime: "2026-09-02T10:00:00Z",
		DueDateTime:  &graph.DateTimeZone{DateTime: "2026-10-02T00:00:00.0000000", TimeZone: "UTC"},
		IsReminderOn: true, ReminderDateTime: &graph.DateTimeZone{DateTime: "2026-10-02T07:00:00.0000000", TimeZone: "UTC"},
		Recurrence: &graph.Recurrence{
			Pattern: graph.RecurrencePattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"friday"}, FirstDayOfWeek: "sunday"},
			Range:   graph.RecurrenceRange{Type: "noEnd", StartDate: "2026-10-02"},
		},
		ChecklistItems: []graph.ChecklistItem{
			{ID: "ci-1", DisplayName: "Kitchen", IsChecked: true, CreatedDateTime: "2026-09-01T10:01:00Z"},
			{ID: "ci-2", DisplayName: "Bedroom", CreatedDateTime: "2026-09-01T10:01:00Z"},
		},
	}
	weekly.Body.Content = "Both rooms"
	data, err := graph.EncodeTask(&weekly)
	write(archive.Tasks, "t1", data, err)

	existing := graph.Task{ID: "t0", Title: "Already there", Status: "notStarted"}
	data, err = graph.EncodeTask(&existing)
	write(archive.Tasks, "t0", data, err)

	// Google tasks: a parent, its subtask, and an hourly repeat To Do lacks.
	data, err = gtasks.Encode(&gtasks.Task{ID: "g1", Title: "Shop", Status: "needsAction", Due: "2026-10-05T00:00:00.000Z"})
	write("Groceries", "g1", data, err)
	data, err = gtasks.Encode(&gtasks.Task{ID: "g2", Title: "Milk", Status: "completed", Parent: "g1", Completed: "2026-10-01T08:00:00.000Z"})
	write("Groceries", "g2", data, err)

	run := &restore.OutlookTasks{Client: graph.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 2, Present: 1}, res)

	written := srv.WrittenTasks()
	require.Len(t, written, 2)

	var water, shop map[string]any

	for _, task := range written {
		switch task["title"] {
		case "Water plants":
			water = task
		case "Shop":
			shop = task
		}
	}

	assert.Equal(t, map[string]any{
		"title": "Water plants", "status": "notStarted", "importance": "high", "categories": []any{"Home"},
		"body":             map[string]any{"content": "Both rooms", "contentType": "text"},
		"dueDateTime":      map[string]any{"dateTime": "2026-10-02T00:00:00", "timeZone": "UTC"},
		"isReminderOn":     true,
		"reminderDateTime": map[string]any{"dateTime": "2026-10-02T07:00:00", "timeZone": "UTC"},
		"recurrence": map[string]any{
			"pattern": map[string]any{"type": "weekly", "interval": float64(1), "daysOfWeek": []any{"friday"}, "firstDayOfWeek": "sunday"},
			"range":   map[string]any{"type": "noEnd", "startDate": "2026-10-02"},
		},
		"list": "l-default",
		"extensions": []any{map[string]any{
			"@odata.type": "microsoft.graph.openTypeExtension", "extensionName": "net.pimvault", "source": "t1",
		}},
		"checklist": []any{
			map[string]any{"displayName": "Kitchen", "isChecked": true},
			map[string]any{"displayName": "Bedroom", "isChecked": false},
		},
	}, water)

	assert.Equal(t, []any{map[string]any{"displayName": "Milk", "isChecked": true}}, shop["checklist"], "a Google subtask becomes a checklist item (D26)")

	lists := srv.TaskLists()
	require.Len(t, lists, 2)
	assert.Equal(t, "Groceries", lists[1].DisplayName)
	assert.Equal(t, lists[1].ID, shop["list"])

	// Running again writes nothing: the written tasks carry the archive IDs
	// they came from (D160).
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

func TestOutlookTasks_UnrepresentableRepeatGoesToBody(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddTaskList(graph.TaskList{ID: "l-default", DisplayName: "Tasks", WellknownListName: graph.DefaultTaskList, IsOwner: true})

	source := archive.NewStore(filepath.Join(t.TempDir(), "tasks"), ".ics")
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//test//EN", "BEGIN:VTODO", "UID:x1", "DTSTAMP:20260101T000000Z",
		"SUMMARY:Stretch", "DTSTART;VALUE=DATE:20261002", "RRULE:FREQ=HOURLY;INTERVAL=2", "END:VTODO", "END:VCALENDAR", "",
	}, "\r\n")
	require.NoError(t, source.Write(archive.Tasks, "x1", []byte(ics)))

	run := &restore.OutlookTasks{Client: graph.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)

	for _, task := range srv.WrittenTasks() {
		assert.Equal(t, map[string]any{"content": "Repeats: FREQ=HOURLY;INTERVAL=2", "contentType": "text"}, task["body"])
		assert.NotContains(t, task, "recurrence")
	}
}

func TestOutlookTasks_FailedChecklistIsRetriedWhole(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddTaskList(graph.TaskList{ID: "l-default", DisplayName: "Tasks", WellknownListName: graph.DefaultTaskList, IsOwner: true})

	source := archive.NewStore(filepath.Join(t.TempDir(), "tasks"), ".ics")
	parent, err := gtasks.Encode(&gtasks.Task{ID: "g1", Title: "Shop", Status: "needsAction"})
	require.NoError(t, err)
	require.NoError(t, source.Write(archive.Tasks, "g1", parent))
	child, err := gtasks.Encode(&gtasks.Task{ID: "g2", Title: "Milk", Status: "needsAction", Parent: "g1"})
	require.NoError(t, err)
	require.NoError(t, source.Write(archive.Tasks, "g2", child))
	// A user list named "Tasks", archived under an escaped directory name,
	// goes to a list of its own, not the default one.
	userList := archive.FolderPath([]string{archive.Tasks}, []string{archive.Tasks})
	require.NoError(t, source.Write(userList, "g3", mustEncode(t, &gtasks.Task{ID: "g3", Title: "Other", Status: "needsAction"})))

	run := &restore.OutlookTasks{Client: graph.New(http.DefaultClient, srv.URL), Source: source, Logger: slog.New(slog.DiscardHandler)}

	// The escaped directory sorts first: its task is t-new-1, Shop t-new-2.
	srv.FailWrites["POST /todo/lists/l-default/tasks/t-new-2/checklistItems"] = http.StatusInternalServerError
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1, Failed: 1}, res)

	clear(srv.FailWrites)
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1, Present: 1}, res)

	byList := map[string][]string{}
	for _, task := range srv.WrittenTasks() {
		list, title := fmt.Sprint(task["list"]), fmt.Sprint(task["title"])
		byList[list] = append(byList[list], title)
	}

	lists := srv.TaskLists()
	require.Len(t, lists, 2)
	assert.Equal(t, "Tasks", lists[1].DisplayName)
	assert.Equal(t, map[string][]string{"l-default": {"Shop"}, lists[1].ID: {"Other"}}, byList)
}

func mustEncode(t *testing.T, task *gtasks.Task) []byte {
	t.Helper()

	data, err := gtasks.Encode(task)
	require.NoError(t, err)

	return data
}
