// Package gcaltest is an in-memory fake of the parts of the Google Calendar
// API that pimvault uses, for tests.
package gcaltest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tonimelisma/pimvault/internal/gcal"
)

// Server is a fake Google Calendar account served over HTTP.
type Server struct {
	// URL is the API root to pass to gcal.New.
	URL string

	mu        sync.Mutex
	calendars []calendar
	events    map[string]map[string]gcal.Event // calendar ID → event ID → event
	changed   map[string]map[string]int        // calendar ID → event ID → change number
	seq       int
	oldest    int
	// PageSize limits event pages; 0 means 2500.
	PageSize int
	// FailSeries makes lookups of these iCalendar UIDs fail with a status.
	FailSeries map[string]int
	// Owner is the account's email address; imported events must have it
	// as organizer or attendee, as in Google Calendar (F54).
	Owner string
	// FailImports makes imports of events with these summaries fail.
	FailImports map[string]int
	imported    map[string][]map[string]any // calendar ID → import requests
	created     int
}

type calendar struct {
	gcal.Calendar
	role string
}

// New starts a fake server; it is closed when the test ends.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{
		events:      make(map[string]map[string]gcal.Event),
		changed:     make(map[string]map[string]int),
		FailSeries:  make(map[string]int),
		FailImports: make(map[string]int),
		imported:    make(map[string][]map[string]any),
	}

	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// AddCalendar adds a calendar with the account's access role, such as
// "owner" or "reader".
func (s *Server) AddCalendar(c gcal.Calendar, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calendars = append(s.calendars, calendar{Calendar: c, role: role})
	s.events[c.ID] = make(map[string]gcal.Event)
	s.changed[c.ID] = make(map[string]int)
}

// RenameCalendar changes a calendar's name.
func (s *Server) RenameCalendar(id, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.calendars {
		if s.calendars[i].ID == id {
			s.calendars[i].Summary = summary
		}
	}
}

// Put creates or replaces an event.
func (s *Server) Put(calendarID string, e gcal.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e.Status == "" {
		e.Status = "confirmed"
	}

	s.seq++
	s.events[calendarID][e.ID] = e
	s.changed[calendarID][e.ID] = s.seq
}

// Delete cancels an event, as deleting does in Google Calendar: the event
// stays listed as canceled with little more than its ID.
func (s *Server) Delete(calendarID, eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old := s.events[calendarID][eventID]
	s.seq++
	s.events[calendarID][eventID] = gcal.Event{
		ID: eventID, Status: "cancelled",
		RecurringEventID: old.RecurringEventID, OriginalStartTime: old.OriginalStartTime,
	}
	s.changed[calendarID][eventID] = s.seq
}

// ExpireTokens makes every sync token so far unusable.
func (s *Server) ExpireTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	s.oldest = s.seq
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	// "primary" names the primary calendar, as in Google Calendar.
	if len(parts) > 1 && parts[0] == "calendars" && parts[1] == "primary" {
		for _, c := range s.calendars {
			if c.Primary {
				parts[1] = c.ID
			}
		}
	}

	switch {
	case r.Method != http.MethodGet:
		s.serveWrite(w, r, parts)
	case r.URL.Path == "/users/me/calendarList":
		s.serveCalendars(w, r)
	case len(parts) == 3 && parts[0] == "calendars" && parts[2] == "events":
		s.serveEvents(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "calendars" && parts[2] == "events":
		s.serveEvent(w, parts[1], parts[3])
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) serveCalendars(w http.ResponseWriter, r *http.Request) {
	items := []gcal.Calendar{}

	for _, c := range s.calendars {
		if r.URL.Query().Get("minAccessRole") != "owner" || c.role == "owner" {
			items = append(items, c.Calendar)
		}
	}

	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request, calendarID string) {
	query := r.URL.Query()
	events := s.events[calendarID]

	var ids []string

	switch {
	case query.Get("syncToken") != "":
		since, err := strconv.Atoi(query.Get("syncToken"))
		if err != nil || since < s.oldest {
			writeError(w, http.StatusGone, "Sync token is no longer valid, a full sync is required.")

			return
		}

		for id, n := range s.changed[calendarID] {
			if n > since {
				ids = append(ids, id)
			}
		}
	case query.Get("iCalUID") != "":
		if status, fail := s.FailSeries[query.Get("iCalUID")]; fail {
			writeError(w, status, "injected failure")

			return
		}

		for id, e := range events {
			if e.ICalUID == query.Get("iCalUID") || (e.ICalUID == "" && e.RecurringEventID != "" &&
				events[e.RecurringEventID].ICalUID == query.Get("iCalUID")) {
				ids = append(ids, id)
			}
		}
	default:
		for id := range events {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)

	start, _ := strconv.Atoi(query.Get("pageToken")) //nolint:errcheck // tokens are generated below.
	size := 2500

	if s.PageSize > 0 {
		size = s.PageSize
	}

	end := min(start+size, len(ids))

	items := make([]gcal.Event, 0, end-start)
	for _, id := range ids[start:end] {
		items = append(items, events[id])
	}

	resp := map[string]any{"items": items}
	if end < len(ids) {
		resp["nextPageToken"] = strconv.Itoa(end)
	} else {
		resp["nextSyncToken"] = strconv.Itoa(s.seq)
	}

	writeJSON(w, resp)
}

func (s *Server) serveEvent(w http.ResponseWriter, calendarID, eventID string) {
	e, ok := s.events[calendarID][eventID]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")

		return
	}

	writeJSON(w, e)
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
