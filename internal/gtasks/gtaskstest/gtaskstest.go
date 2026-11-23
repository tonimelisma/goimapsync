// Package gtaskstest is an in-memory fake of the parts of the Google Tasks
// API that pimvault uses, for tests.
package gtaskstest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tonimelisma/pimvault/internal/gtasks"
)

// Server is a fake Google account's task lists served over HTTP.
type Server struct {
	// URL is the API root to pass to gtasks.New.
	URL string

	mu    sync.Mutex
	lists []gtasks.List
	tasks map[string][]task // list ID → tasks in order
	// PageSize limits pages; 0 means 100.
	PageSize int
	// FailCreate makes creating tasks with these titles fail with a status.
	FailCreate map[string]int
	created    int
}

type task struct {
	gtasks.Task
	hidden bool
}

// New starts a fake server whose first list is the default; it is closed
// when the test ends.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{tasks: make(map[string][]task), FailCreate: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// AddList adds a task list; the first one added is the default list.
func (s *Server) AddList(l gtasks.List) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lists = append(s.lists, l)
}

// Put creates or replaces a task. Hidden tasks, such as those completed in
// Google's apps, are listed only with showHidden.
func (s *Server) Put(listID string, t gtasks.Task, hidden bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.tasks[listID] {
		if s.tasks[listID][i].ID == t.ID {
			s.tasks[listID][i] = task{t, hidden}

			return
		}
	}

	s.tasks[listID] = append(s.tasks[listID], task{t, hidden})
}

// Delete deletes a task.
func (s *Server) Delete(listID, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.tasks[listID] {
		if s.tasks[listID][i].ID == id {
			s.tasks[listID] = append(s.tasks[listID][:i], s.tasks[listID][i+1:]...)

			return
		}
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/users/@me/lists":
		var l gtasks.List
		_ = json.NewDecoder(r.Body).Decode(&l) //nolint:errcheck // an empty title is stored as such.
		l.ID = "l-new-" + strconv.Itoa(len(s.lists)+1)
		s.lists = append(s.lists, l)
		writeJSON(w, l)
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "lists" && parts[2] == "tasks":
		var t gtasks.Task
		_ = json.NewDecoder(r.Body).Decode(&t) //nolint:errcheck // an empty task is stored as such.

		if status, fail := s.FailCreate[t.Title]; fail {
			w.WriteHeader(status)

			return
		}

		t.ID = "t-new-" + strconv.Itoa(s.created+1)
		t.Parent = r.URL.Query().Get("parent")
		s.created++
		s.insert(parts[1], t, r.URL.Query().Get("previous"))
		writeJSON(w, t)
	case r.Method == http.MethodDelete && len(parts) == 4 && parts[0] == "lists" && parts[2] == "tasks":
		s.tasks[parts[1]] = slices.DeleteFunc(s.tasks[parts[1]], func(t task) bool { return t.ID == parts[3] || t.Parent == parts[3] })
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/users/@me/lists/@default" && len(s.lists) > 0:
		writeJSON(w, s.lists[0])
	case r.URL.Path == "/users/@me/lists":
		writeJSON(w, map[string]any{"items": s.lists})
	case len(parts) == 3 && parts[0] == "lists" && parts[2] == "tasks":
		s.serveTasks(w, r, parts[1])
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// insert places a new task as Google does: right after previous, or first
// among its siblings.
func (s *Server) insert(listID string, t gtasks.Task, previous string) {
	tasks := s.tasks[listID]

	at := len(tasks)

	for i, other := range tasks {
		if previous != "" && other.ID == previous {
			at = i + 1

			break
		}

		if previous == "" && other.Parent == t.Parent {
			at = i

			break
		}
	}

	s.tasks[listID] = slices.Insert(tasks, at, task{Task: t})
}

func (s *Server) serveTasks(w http.ResponseWriter, r *http.Request, listID string) {
	q := r.URL.Query()

	var items []gtasks.Task

	for _, t := range s.tasks[listID] {
		if t.hidden && q.Get("showHidden") != "true" || t.Status == "completed" && q.Get("showCompleted") == "false" {
			continue
		}

		items = append(items, t.Task)
	}

	start, _ := strconv.Atoi(q.Get("pageToken")) //nolint:errcheck // an empty token is the first page.

	size := s.PageSize
	if size == 0 {
		size = 100
	}

	end := min(start+size, len(items))
	page := map[string]any{"items": items[start:end]}

	if end < len(items) {
		page["nextPageToken"] = strconv.Itoa(end)
	}

	writeJSON(w, page)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // a test client that went away changes nothing.
}

// Lists returns the task lists.
func (s *Server) Lists() []gtasks.List {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.lists)
}

// Tasks returns a list's tasks.
func (s *Server) Tasks(listID string) []gtasks.Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]gtasks.Task, 0, len(s.tasks[listID]))
	for _, t := range s.tasks[listID] {
		out = append(out, t.Task)
	}

	return out
}
