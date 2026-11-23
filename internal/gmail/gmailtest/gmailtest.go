// Package gmailtest is an in-memory fake of the parts of the Gmail API that
// pimvault uses, for tests.
package gmailtest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// firstHistoryID is the history ID of a new mailbox.
const firstHistoryID = 100

// Server is a fake Gmail mailbox served over HTTP. Its methods change the
// mailbox and record history like Gmail does.
type Server struct {
	// URL is the API root to pass to gmail.New.
	URL string

	mu       sync.Mutex
	labels   map[string]string // user label ID → name
	messages map[string]*message
	history  []record
	current  int
	// oldest is the lowest history ID still available; older starts get 404.
	oldest int
	// PageSize limits list and history pages; 0 means 500.
	PageSize int
	// FailRaw makes raw downloads of these message IDs fail with a status.
	FailRaw map[string]int
	// FailSize makes size reads (format=minimal with sizeEstimate) of these
	// message IDs fail with a status.
	FailSize map[string]int
	// Requests counts requests by path, such as "/history".
	Requests map[string]int
}

type message struct {
	raw    []byte
	labels []string
}

type record struct {
	id      int
	kind    string
	message string
}

// New starts a fake server; it is closed when the test ends.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{
		labels:   make(map[string]string),
		messages: make(map[string]*message),
		current:  firstHistoryID,
		oldest:   1,
		FailRaw:  make(map[string]int),
		FailSize: make(map[string]int),
		Requests: make(map[string]int),
	}

	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// AddLabel creates a user label.
func (s *Server) AddLabel(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.labels[id] = name
}

// Add delivers a message with the given labels.
func (s *Server) Add(id, raw string, labels ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages[id] = &message{raw: []byte(raw), labels: labels}
	s.record("messagesAdded", id)
}

// SetLabels replaces a message's labels.
func (s *Server) SetLabels(id string, labels ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages[id].labels = labels
	s.record("labelsAdded", id)
}

// Delete removes a message permanently.
func (s *Server) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.messages, id)
	s.record("messagesDeleted", id)
}

// ExpireHistory makes every history ID so far too old to use.
func (s *Server) ExpireHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.current++
	s.oldest = s.current
}

func (s *Server) record(kind, id string) {
	s.current++
	s.history = append(s.history, record{id: s.current, kind: kind, message: id})
}

func (s *Server) pageSize() int {
	if s.PageSize > 0 {
		return s.PageSize
	}

	return 500
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := r.URL.Path
	s.Requests[path]++

	switch {
	case path == "/labels" && r.Method == http.MethodPost:
		s.createLabel(w, r)
	case path == "/upload/messages" && r.Method == http.MethodPost:
		s.serveUpload(w, r)
	case path == "/labels":
		s.serveLabels(w)
	case path == "/profile":
		writeJSON(w, map[string]string{"historyId": strconv.Itoa(s.current)})
	case path == "/messages":
		s.serveList(w, r)
	case path == "/history":
		s.serveHistory(w, r)
	case strings.HasPrefix(path, "/messages/"):
		s.serveMessage(w, r, strings.TrimPrefix(path, "/messages/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveLabels(w http.ResponseWriter) {
	type label struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}

	labels := []label{
		{ID: "INBOX", Name: "INBOX", Type: "system"},
		{ID: "SENT", Name: "SENT", Type: "system"},
		{ID: "DRAFT", Name: "DRAFT", Type: "system"},
		{ID: "SPAM", Name: "SPAM", Type: "system"},
		{ID: "TRASH", Name: "TRASH", Type: "system"},
	}

	for id, name := range s.labels {
		labels = append(labels, label{ID: id, Name: name, Type: "user"})
	}

	writeJSON(w, map[string]any{"labels": labels})
}

func (s *Server) serveList(w http.ResponseWriter, r *http.Request) {
	want := r.URL.Query().Get("labelIds")
	matches := search(r.URL.Query().Get("q"))

	var ids []string

	for id, m := range s.messages {
		if (slices.Contains(m.labels, "SPAM") || slices.Contains(m.labels, "TRASH")) && r.URL.Query().Get("includeSpamTrash") != "true" {
			continue
		}

		if matches(m.raw) && (want == "" || slices.Contains(m.labels, want)) {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)

	page, next := paginate(ids, r.URL.Query().Get("pageToken"), s.pageSize())

	type ref struct {
		ID string `json:"id"`
	}

	refs := make([]ref, len(page))
	for i, id := range page {
		refs[i] = ref{ID: id}
	}

	resp := map[string]any{"resultSizeEstimate": len(ids)}
	if len(refs) > 0 {
		resp["messages"] = refs
	}

	if next != "" {
		resp["nextPageToken"] = next
	}

	writeJSON(w, resp)
}

func (s *Server) serveHistory(w http.ResponseWriter, r *http.Request) {
	start, err := strconv.Atoi(r.URL.Query().Get("startHistoryId"))
	if err != nil || start < s.oldest {
		writeError(w, http.StatusNotFound, "Requested entity was not found.")

		return
	}

	var newer []record

	for _, rec := range s.history {
		if rec.id > start {
			newer = append(newer, rec)
		}
	}

	indexes := make([]string, len(newer))
	for i := range newer {
		indexes[i] = strconv.Itoa(i)
	}

	page, next := paginate(indexes, r.URL.Query().Get("pageToken"), s.pageSize())

	var history []map[string]any

	for _, idx := range page {
		i, _ := strconv.Atoi(idx) //nolint:errcheck // indexes are generated above.
		rec := newer[i]
		history = append(history, map[string]any{
			"id":     strconv.Itoa(rec.id),
			rec.kind: []map[string]any{{"message": map[string]string{"id": rec.message}}},
		})
	}

	resp := map[string]any{"historyId": strconv.Itoa(s.current)}
	if len(history) > 0 {
		resp["history"] = history
	}

	if next != "" {
		resp["nextPageToken"] = next
	}

	writeJSON(w, resp)
}

func (s *Server) serveMessage(w http.ResponseWriter, r *http.Request, id string) {
	m, ok := s.messages[id]
	if !ok {
		writeError(w, http.StatusNotFound, "Requested entity was not found.")

		return
	}

	switch r.URL.Query().Get("format") {
	case "metadata":
		var headers []map[string]string

		for _, name := range r.URL.Query()["metadataHeaders"] {
			if v := header(m.raw, name); v != "" {
				headers = append(headers, map[string]string{"name": name, "value": v})
			}
		}

		writeJSON(w, map[string]any{"payload": map[string]any{"headers": headers}})
	case "minimal":
		if status, fail := s.FailSize[id]; fail && strings.Contains(r.URL.Query().Get("fields"), "sizeEstimate") {
			writeError(w, status, "injected failure")

			return
		}

		// sizeEstimate is the raw length, as on Gmail (F78).
		writeJSON(w, map[string]any{"labelIds": m.labels, "sizeEstimate": len(m.raw)})
	case "raw":
		if status, fail := s.FailRaw[id]; fail {
			writeError(w, status, "injected failure")

			return
		}

		writeJSON(w, map[string]string{"raw": base64.URLEncoding.EncodeToString(m.raw)})
	default:
		writeError(w, http.StatusBadRequest, "unsupported format")
	}
}

// paginate returns the page starting at token (an index) and the next token.
func paginate(items []string, token string, size int) (page []string, next string) {
	start := 0
	if token != "" {
		start, _ = strconv.Atoi(token) //nolint:errcheck // tokens are generated here.
	}

	end := min(start+size, len(items))
	if end < len(items) {
		next = strconv.Itoa(end)
	}

	return items[start:end], next
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // the test client reports broken responses.
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // as above.
		"error": map[string]any{"code": status, "message": msg},
	})
}
