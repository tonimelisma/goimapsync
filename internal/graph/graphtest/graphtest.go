// Package graphtest is an in-memory fake of the parts of Microsoft Graph that
// pimvault uses for Outlook.com mail, for tests.
package graphtest

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// Server is a fake Outlook.com mailbox served over HTTP. Its methods change
// the mailbox and record per-folder changes like Graph's delta queries.
type Server struct {
	// URL is the API root to pass to graph.New.
	URL string

	mu       sync.Mutex
	folders  map[string]*folder
	messages map[string]*message
	log      []change
	seq      int
	// oldest is the lowest change number a delta link may start from.
	oldest int
	// PageSize limits pages; 0 means 500.
	PageSize int
	// FailRaw makes downloads of these message IDs fail with a status.
	FailRaw map[string]int
	// Requests counts requests by path.
	Requests map[string]int
	// FailWrites makes writes fail with a status, by method and path:
	// "POST /contactFolders/contacts/contacts".
	FailWrites map[string]int
	// TimeZone is the mailbox's time zone; empty means UTC.
	TimeZone string

	failFolders map[string]int
	expirePage  bool

	calendars   []graph.Calendar
	events      map[string][]graph.Event      // calendar ID → events (single and masters)
	exceptions  map[string][]graph.Event      // master ID → exceptions
	canceled    map[string][]string           // master ID → canceled occurrence IDs
	attachments map[string][]graph.Attachment // event ID → attachments

	contactData *contactState
	todoData    *todoState
	eventData   *eventState
}

type folder struct {
	id, name, parent, wellKnown string
}

type message struct {
	folder string
	raw    []byte
}

type change struct {
	seq     int
	folder  string
	message string
}

// New starts a fake server with the default well-known folders; it is
// closed when the test ends.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{
		folders:     make(map[string]*folder),
		messages:    make(map[string]*message),
		FailRaw:     make(map[string]int),
		Requests:    make(map[string]int),
		FailWrites:  make(map[string]int),
		failFolders: make(map[string]int),
		events:      make(map[string][]graph.Event),
		exceptions:  make(map[string][]graph.Event),
		canceled:    make(map[string][]string),
		attachments: make(map[string][]graph.Attachment),
	}

	for _, f := range []struct{ id, name, wellKnown string }{
		{"f-inbox", "Inbox", "inbox"},
		{"f-drafts", "Drafts", "drafts"},
		{"f-sent", "Sent Items", "sentitems"},
		{"f-deleted", "Deleted Items", "deleteditems"},
		{"f-junk", "Junk Email", "junkemail"},
		{"f-archive", "Archive", "archive"},
		{"f-outbox", "Outbox", "outbox"},
		{"f-history", "Conversation History", "conversationhistory"},
	} {
		s.folders[f.id] = &folder{id: f.id, name: f.name, wellKnown: f.wellKnown}
	}

	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// AddFolder creates a user folder inside parent ("" for top level).
func (s *Server) AddFolder(id, name, parent string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.folders[id] = &folder{id: id, name: name, parent: parent}
}

// RenameFolder renames a folder.
func (s *Server) RenameFolder(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.folders[id].name = name
}

// Add delivers a message into a folder.
func (s *Server) Add(id, folderID, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages[id] = &message{folder: folderID, raw: []byte(raw)}
	s.record(folderID, id)
}

// Move moves a message to another folder; its immutable ID stays the same.
func (s *Server) Move(id, folderID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m := s.messages[id]
	s.record(m.folder, id)
	m.folder = folderID
	s.record(folderID, id)
}

// Delete removes a message permanently.
func (s *Server) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.record(s.messages[id].folder, id)
	delete(s.messages, id)
}

// ExpireDelta makes every delta link so far unusable.
func (s *Server) ExpireDelta() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	s.oldest = s.seq
}

func (s *Server) record(folderID, id string) {
	s.seq++
	s.log = append(s.log, change{seq: s.seq, folder: folderID, message: id})
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

	s.Requests[r.URL.Path]++

	if !slices.Contains(r.Header.Values("Prefer"), `IdType="ImmutableId"`) {
		writeError(w, http.StatusBadRequest, "TestMissingImmutableId", "request without immutable IDs")

		return
	}

	if status, ok := s.FailWrites[r.Method+" "+r.URL.Path]; ok {
		writeError(w, status, "ErrorInternalServerError", "injected failure")

		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	if s.serveContacts(w, r, parts) || s.serveTodo(w, r, parts) || s.serveCalendarWrites(w, r, parts) {
		return
	}

	switch {
	case len(parts) == 1 && parts[0] == "messages":
		s.serveMessageSearch(w, r)
	case len(parts) == 1 && parts[0] == "mailFolders":
		s.serveFolders(w, r, "")
	case len(parts) == 2 && parts[0] == "mailFolders":
		s.serveWellKnown(w, parts[1])
	case len(parts) == 3 && parts[0] == "mailFolders" && parts[2] == "childFolders":
		s.serveFolders(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "mailFolders" && parts[2] == "messages" && parts[3] == "delta":
		s.serveDelta(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "messages" && parts[2] == "$value":
		s.serveRaw(w, parts[1])
	case len(parts) == 1 && parts[0] == "calendars":
		writeJSON(w, map[string]any{"value": s.calendars})
	case len(parts) == 1 && parts[0] == "mailboxSettings":
		// As a personal account answers: the zone only in working hours.
		writeJSON(w, map[string]any{"workingHours": map[string]any{"timeZone": map[string]string{"name": cmp.Or(s.TimeZone, "UTC")}}})
	case len(parts) == 3 && parts[0] == "calendars" && parts[2] == "events":
		writeJSON(w, map[string]any{"value": s.events[parts[1]]})
	case len(parts) == 2 && parts[0] == "events":
		s.serveSeries(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "events" && parts[2] == "attachments":
		writeJSON(w, map[string]any{"value": s.attachments[parts[1]]})
	default:
		writeError(w, http.StatusNotFound, "ErrorInvalidPath", r.URL.Path)
	}
}

// serveSeries serves a series master with its exceptions expanded. Like
// Graph, a $select on the master reduces expanded exceptions to their IDs.
func (s *Server) serveSeries(w http.ResponseWriter, r *http.Request, masterID string) {
	var exceptions any = s.exceptions[masterID]
	if r.URL.Query().Has("$select") {
		ids := make([]map[string]string, 0, len(s.exceptions[masterID]))
		for i := range s.exceptions[masterID] {
			ids = append(ids, map[string]string{"id": s.exceptions[masterID][i].ID})
		}

		exceptions = ids
	}

	writeJSON(w, map[string]any{
		"id": masterID, "exceptionOccurrences": exceptions, "cancelledOccurrences": s.canceled[masterID],
	})
}

func (s *Server) serveWellKnown(w http.ResponseWriter, name string) {
	if name == "allcontacts" {
		writeJSON(w, map[string]string{"id": AllContacts})

		return
	}

	if name == "clutter" {
		// Some mailboxes reject names they do not support.
		writeError(w, http.StatusBadRequest, "ErrorInvalidRequest", "unsupported folder")

		return
	}

	for _, f := range s.folders {
		if f.wellKnown == name {
			writeJSON(w, map[string]string{"id": f.id})

			return
		}
	}

	writeError(w, http.StatusNotFound, "ErrorItemNotFound", "no folder "+name)
}

func (s *Server) serveFolders(w http.ResponseWriter, r *http.Request, parent string) {
	var ids []string

	for id, f := range s.folders {
		if f.parent == parent {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)

	page, next := paginate(ids, r.URL.Query().Get("$skip"), s.pageSize())

	values := make([]map[string]any, 0, len(page))

	for _, id := range page {
		children := 0

		for _, f := range s.folders {
			if f.parent == id {
				children++
			}
		}

		values = append(values, map[string]any{"id": id, "displayName": s.folders[id].name, "childFolderCount": children})
	}

	resp := map[string]any{"value": values}
	if next != "" {
		q := r.URL.Query()
		q.Set("$skip", next)
		resp["@odata.nextLink"] = s.URL + r.URL.Path + "?" + q.Encode()
	}

	writeJSON(w, resp)
}

func (s *Server) serveDelta(w http.ResponseWriter, r *http.Request, folderID string) {
	query := r.URL.Query()

	if status, fail := s.failFolders[folderID]; fail {
		writeError(w, status, "TestInjected", "injected failure")

		return
	}

	if s.expirePage && query.Get("$skiptoken") != "" {
		s.expirePage = false
		writeError(w, http.StatusGone, "SyncStateNotFound", "listing expired")

		return
	}

	type item struct {
		id      string
		removed bool
	}

	var items []item

	if token := query.Get("$deltatoken"); token != "" {
		since, err := strconv.Atoi(token)
		if err != nil || since < s.oldest {
			writeError(w, http.StatusGone, "SyncStateNotFound", "delta token expired")

			return
		}

		seen := make(map[string]bool)

		for _, c := range s.log {
			if c.seq <= since || c.folder != folderID || seen[c.message] {
				continue
			}

			seen[c.message] = true
			m, exists := s.messages[c.message]
			items = append(items, item{id: c.message, removed: !exists || m.folder != folderID})
		}
	} else {
		var ids []string

		for id, m := range s.messages {
			if m.folder == folderID {
				ids = append(ids, id)
			}
		}

		slices.Sort(ids)

		for _, id := range ids {
			items = append(items, item{id: id})
		}
	}

	start := 0
	if skip := query.Get("$skiptoken"); skip != "" {
		start, _ = strconv.Atoi(skip) //nolint:errcheck // generated below.
	}

	end := min(start+s.pageSize(), len(items))

	values := make([]map[string]any, 0, end-start)
	for _, it := range items[start:end] {
		v := map[string]any{"id": it.id}
		if it.removed {
			v["@removed"] = map[string]string{"reason": "deleted"}
		}

		values = append(values, v)
	}

	resp := map[string]any{"value": values}
	if end < len(items) {
		query.Set("$skiptoken", strconv.Itoa(end))
		resp["@odata.nextLink"] = s.URL + r.URL.Path + "?" + query.Encode()
	} else {
		resp["@odata.deltaLink"] = fmt.Sprintf("%s%s?$deltatoken=%d", s.URL, r.URL.Path, s.seq)
	}

	writeJSON(w, resp)
}

func (s *Server) serveRaw(w http.ResponseWriter, id string) {
	m, ok := s.messages[id]
	if !ok {
		writeError(w, http.StatusNotFound, "ErrorItemNotFound", "no message")

		return
	}

	if status, fail := s.FailRaw[id]; fail {
		writeError(w, status, "TestInjected", "injected failure")

		return
	}

	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write(m.raw) //nolint:errcheck // the test client reports broken responses.
}

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

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // as above.
		"error": map[string]string{"code": code, "message": msg},
	})
}

// FailFolder makes delta queries of a folder fail with a status.
// A status of 0 ends the failure.
func (s *Server) FailFolder(folderID string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status == 0 {
		delete(s.failFolders, folderID)

		return
	}

	s.failFolders[folderID] = status
}

// ExpireListings makes the next delta page that continues a listing in
// progress fail as expired, once.
func (s *Server) ExpireListings() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.expirePage = true
}

// Edit changes a message's content in place, as saving a draft does; its ID
// stays the same.
func (s *Server) Edit(id, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m := s.messages[id]
	m.raw = []byte(raw)
	s.record(m.folder, id)
}

// AddCalendar adds a calendar.
func (s *Server) AddCalendar(c graph.Calendar) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calendars = append(s.calendars, c)
}

// SetEvents replaces a calendar's single events and series masters.
func (s *Server) SetEvents(calendarID string, events ...graph.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[calendarID] = events
}

// SetSeries sets a series master's exceptions and canceled occurrences.
func (s *Server) SetSeries(masterID string, exceptions []graph.Event, canceled []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.exceptions[masterID] = exceptions
	s.canceled[masterID] = canceled
}

// SetAttachments sets an event's attachments.
func (s *Server) SetAttachments(eventID string, attachments ...graph.Attachment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.attachments[eventID] = attachments
}
