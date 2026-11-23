package graphtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// todoState is the fake's To Do lists.
type todoState struct {
	lists []graph.TaskList
	tasks map[string]storedTask
	log   []change
	// written holds tasks created through the API: their fields, with the
	// list under "list" and checklist items under "checklist".
	written map[string]map[string]any
	created int
}

type storedTask struct {
	list string
	task graph.Task
}

func (s *Server) todoState() *todoState {
	if s.todoData == nil {
		s.todoData = &todoState{tasks: make(map[string]storedTask), written: make(map[string]map[string]any)}
	}

	return s.todoData
}

// AddTaskList adds a To Do list.
func (s *Server) AddTaskList(l graph.TaskList) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.todoState()
	st.lists = append(st.lists, l)
}

// PutTask creates or replaces a task, with its checklist, in a list.
func (s *Server) PutTask(listID string, t graph.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.todoState()
	st.tasks[t.ID] = storedTask{list: listID, task: t}
	s.seq++
	st.log = append(st.log, change{seq: s.seq, folder: listID, message: t.ID})
}

// DeleteTask deletes a task.
func (s *Server) DeleteTask(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.todoState()
	s.seq++
	st.log = append(st.log, change{seq: s.seq, folder: st.tasks[id].list, message: id})
	delete(st.tasks, id)
}

// serveTodo answers To Do requests; it reports false for other paths.
func (s *Server) serveTodo(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if parts[0] != "todo" {
		return false
	}

	if r.Method == http.MethodDelete && len(parts) == 5 && parts[0] == "todo" && parts[3] == "tasks" {
		st := s.todoState()
		delete(st.tasks, parts[4])
		delete(st.written, parts[4])
		w.WriteHeader(http.StatusNoContent)

		return true
	}

	if r.Method == http.MethodPost {
		return s.writeTodo(w, r, parts)
	}

	switch {
	case len(parts) == 4 && parts[0] == "todo" && parts[1] == "lists" && parts[3] == "tasks":
		s.serveTaskList(w, parts[2])
	case len(parts) == 2 && parts[0] == "todo" && parts[1] == "lists":
		writeJSON(w, map[string]any{"value": s.todoState().lists})
	case len(parts) == 5 && parts[0] == "todo" && parts[1] == "lists" && parts[3] == "tasks" && parts[4] == "delta":
		s.serveTaskDelta(w, r, parts[2])
	default:
		return false
	}

	return true
}

// serveTaskDelta serves a list's tasks or their changes. Checklists are
// included only when the first request expanded them; Graph keeps the
// expansion inside its delta token, not in the link's query (F47). The fake
// assumes every delta token came from an expanded request.
func (s *Server) serveTaskDelta(w http.ResponseWriter, r *http.Request, listID string) {
	st := s.todoState()
	query := r.URL.Query()
	expand := query.Get("$expand") == "checklistItems" || query.Has("$deltatoken")

	s.serveItemDelta(w, r, listID, st.log, slices.Sorted(maps.Keys(st.tasks)), func(id string) (any, bool) {
		stored, ok := st.tasks[id]
		task := stored.task

		if !expand {
			task.ChecklistItems = nil
		}

		return task, ok && stored.list == listID
	})
}

// WrittenTasks returns tasks created through the API by ID.
func (s *Server) WrittenTasks() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.todoState().written)
}

// TaskLists returns the To Do lists.
func (s *Server) TaskLists() []graph.TaskList {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.todoState().lists)
}

// writeTodo answers To Do writes; it reports false for other paths.
func (s *Server) writeTodo(w http.ResponseWriter, r *http.Request, parts []string) bool {
	st := s.todoState()

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "ErrorInvalidRequest", err.Error())

		return true
	}

	switch {
	case len(parts) == 2 && parts[0] == "todo" && parts[1] == "lists":
		name, _ := body["displayName"].(string) //nolint:errcheck // a missing name is stored as empty.
		id := fmt.Sprintf("l-new-%d", len(st.lists)+1)
		st.lists = append(st.lists, graph.TaskList{ID: id, DisplayName: name, IsOwner: true})
		writeJSON(w, map[string]string{"id": id})
	case len(parts) == 4 && parts[0] == "todo" && parts[1] == "lists" && parts[3] == "tasks":
		st.created++
		id := fmt.Sprintf("t-new-%d", st.created)
		title, _ := body["title"].(string) //nolint:errcheck // as above.
		st.tasks[id] = storedTask{list: parts[2], task: graph.Task{ID: id, Title: title}}
		s.seq++
		st.log = append(st.log, change{seq: s.seq, folder: parts[2], message: id})
		body["list"] = parts[2]
		st.written[id] = body
		writeJSON(w, map[string]string{"id": id})
	case len(parts) == 6 && parts[0] == "todo" && parts[3] == "tasks" && parts[5] == "checklistItems":
		task, ok := st.written[parts[4]]
		if !ok {
			writeError(w, http.StatusNotFound, "ErrorItemNotFound", "no task")

			return true
		}

		items, _ := task["checklist"].([]any) //nolint:errcheck // nil starts the list.
		task["checklist"] = append(items, body)
		writeJSON(w, map[string]string{"id": "ci"})
	default:
		return false
	}

	return true
}

// serveTaskList lists a list's tasks with the open extensions they were
// created with.
func (s *Server) serveTaskList(w http.ResponseWriter, listID string) {
	st := s.todoState()

	var values []map[string]any

	for _, id := range slices.Sorted(maps.Keys(st.tasks)) {
		if st.tasks[id].list != listID {
			continue
		}

		v := map[string]any{"id": id, "title": st.tasks[id].task.Title}
		if ext, ok := st.written[id]["extensions"]; ok {
			v["extensions"] = ext
		}

		values = append(values, v)
	}

	writeJSON(w, map[string]any{"value": values})
}
