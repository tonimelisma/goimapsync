package gcaltest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/tonimelisma/pimvault/internal/gcal"
)

// serveWrite answers calendar creation, event imports and deletions.
func (s *Server) serveWrite(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case r.Method == http.MethodPost && len(parts) == 1 && parts[0] == "calendars":
		var c gcal.Calendar
		_ = json.NewDecoder(r.Body).Decode(&c) //nolint:errcheck // an empty name is stored as such.
		s.created++
		c.ID = fmt.Sprintf("cal-new-%d", s.created)
		s.calendars = append(s.calendars, calendar{Calendar: c, role: "owner"})
		s.events[c.ID] = make(map[string]gcal.Event)
		s.changed[c.ID] = make(map[string]int)
		writeJSON(w, c)
	case r.Method == http.MethodPost && len(parts) == 4 && parts[0] == "calendars" && parts[3] == "import":
		s.serveImport(w, r, parts[1])
	case r.Method == http.MethodDelete && len(parts) == 4 && parts[0] == "calendars" && parts[2] == "events":
		for id, e := range s.events[parts[1]] {
			if id == parts[3] || e.RecurringEventID == parts[3] {
				delete(s.events[parts[1]], id)
			}
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// serveImport stores an imported event: a new one, or an update of the
// event with the same UID (and original start, for an instance).
func (s *Server) serveImport(w http.ResponseWriter, r *http.Request, calendarID string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	raw, _ := json.Marshal(body) //nolint:errcheck // decoded from JSON above.

	var e gcal.Event
	_ = json.Unmarshal(raw, &e) //nolint:errcheck // as above.

	if status, fail := s.FailImports[e.Summary]; fail {
		writeError(w, status, "injected failure")

		return
	}

	if !s.involved(&e) {
		writeError(w, http.StatusBadRequest,
			"The owner of the calendar must either be the organizer or an attendee of an event that is imported.")

		return
	}

	e.ID = "ev-" + strings.NewReplacer("@", "-", ".", "-").Replace(e.ICalUID)
	if e.OriginalStartTime.DateTime != "" || e.OriginalStartTime.Date != "" {
		e.RecurringEventID = e.ID
		e.ID += "_" + e.OriginalStartTime.DateTime + e.OriginalStartTime.Date
	}

	e.Status = "confirmed"
	s.seq++
	s.events[calendarID][e.ID] = e
	s.changed[calendarID][e.ID] = s.seq
	s.imported[calendarID] = append(s.imported[calendarID], body)
	writeJSON(w, e)
}

// involved reports whether the owner organizes or attends an event; an
// event without an organizer is organized by the owner.
func (s *Server) involved(e *gcal.Event) bool {
	if e.Organizer.Email == "" || strings.EqualFold(e.Organizer.Email, s.Owner) {
		return true
	}

	return slices.ContainsFunc(e.Attendees, func(a gcal.Attendee) bool { return strings.EqualFold(a.Email, s.Owner) })
}

// Imported returns the import requests by calendar ID.
func (s *Server) Imported() map[string][]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.imported)
}

// Calendars returns the calendars.
func (s *Server) Calendars() []gcal.Calendar {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]gcal.Calendar, 0, len(s.calendars))
	for _, c := range s.calendars {
		out = append(out, c.Calendar)
	}

	return out
}

// Events returns a calendar's events by ID.
func (s *Server) Events(calendarID string) map[string]gcal.Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.events[calendarID])
}
