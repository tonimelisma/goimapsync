package backup_test

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gtasks"
	"github.com/tonimelisma/pimvault/internal/gtasks/gtaskstest"
	"github.com/tonimelisma/pimvault/internal/state"
)

func TestGoogleTasks(t *testing.T) {
	srv := gtaskstest.New(t)
	srv.PageSize = 1
	srv.AddList(gtasks.List{ID: "l-default", Title: "My Tasks"})
	srv.AddList(gtasks.List{ID: "l-work", Title: "Work"})

	srv.Put("l-default", gtasks.Task{
		ID: "t1", Title: "Pay rent", Notes: "Line one\nline two", Status: "needsAction",
		Due: "2026-10-01T00:00:00.000Z", Updated: "2026-09-20T10:00:00.000Z",
	}, false)
	srv.Put("l-default", gtasks.Task{ID: "t2", Title: "Call bank", Parent: "t1", Status: "needsAction", Updated: "2026-09-20T10:00:00.000Z"}, false)
	srv.Put("l-work", gtasks.Task{
		ID: "t3", Title: "Report", Status: "completed", Completed: "2026-09-21T08:30:00.000Z", Updated: "2026-09-21T08:30:00.000Z",
	}, true)

	root := filepath.Join(t.TempDir(), "tasks")
	run := &backup.GoogleTasks{
		Client: gtasks.New(http.DefaultClient, srv.URL),
		Store:  archive.NewStore(root, ".ics").KeepingAside(root + "-aside"),
		Logger: slog.New(slog.DiscardHandler),
	}

	var prev *state.Collections

	backupOnce := func() backup.Result {
		res, next, err := run.Run(t.Context(), prev)
		require.NoError(t, err)
		prev = next

		return res
	}

	assert.Equal(t, backup.Result{Files: 3, Downloaded: 3}, backupOnce())
	assert.Equal(t, []string{"Tasks/t1.ics", "Tasks/t2.ics", "Work/t3.ics"}, listFiles(t, root))

	read := func(path string) string {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) //nolint:gosec // G304: test path.
		require.NoError(t, err)

		return string(data)
	}

	rent := read("Tasks/t1.ics")
	assert.Contains(t, rent, "DUE;VALUE=DATE:20261001\r\n")
	assert.Contains(t, rent, "DESCRIPTION:Line one\\nline two\r\n")
	assert.Contains(t, rent, "STATUS:NEEDS-ACTION\r\n")
	assert.Contains(t, read("Tasks/t2.ics"), "RELATED-TO;RELTYPE=PARENT:t1\r\n")

	report := read("Work/t3.ics")
	assert.Contains(t, report, "STATUS:COMPLETED\r\n", "a hidden completed task is kept (D27)")
	assert.Contains(t, report, "COMPLETED:20260921T083000Z\r\n")

	assert.Equal(t, backup.Result{Files: 3}, backupOnce(), "nothing changed, nothing is rewritten")

	srv.Delete("l-default", "t2")
	srv.Put("l-work", gtasks.Task{ID: "t3", Title: "Report v2", Status: "completed", Updated: "2026-09-22T08:30:00.000Z"}, true)

	assert.Equal(t, backup.Result{Files: 2, Downloaded: 1, Deleted: 1}, backupOnce())
}
