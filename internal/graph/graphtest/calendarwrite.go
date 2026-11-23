package graphtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// eventState holds events created through the API.
type eventState struct {
	// written holds the request bodies of created events, with their
	// calendar under "calendar".
	written map[string]map[string]any
	// patched holds the changes made to occurrences, by occurrence ID.
	patched map[string]map[string]any
	// deleted holds deleted occurrence IDs.
	deleted map[string]bool
	created int
}

func (s *Server) eventState() *eventState {
	if s.eventData == nil {
		s.eventData = &eventState{
			written: make(map[string]map[string]any), patched: make(map[string]map[string]any), deleted: make(map[string]bool),
		}
	}

	return s.eventData
}

// serveCalendarWrites answers calendar writes, occurrence listings and the
// listing restore uses; it reports false for other requests.
func (s *Server) serveCalendarWrites(w http.ResponseWriter, r *http.Request, parts []string) bool {
	st := s.eventState()

	switch {
	case r.Method == http.MethodPost && len(parts) == 1 && parts[0] == "calendars":
		var c graph.Calendar
		_ = json.NewDecoder(r.Body).Decode(&c) //nolint:errcheck // an empty name is stored as such.
		st.created++
		c.ID = fmt.Sprintf("cal-new-%d", st.created)
		c.CanEdit = true
		s.calendars = append(s.calendars, c)
		writeJSON(w, c)
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "calendars" && parts[2] == "events":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "ErrorInvalidRequest", err.Error())

			return true
		}

		st.created++
		id := fmt.Sprintf("ev-new-%d", st.created)
		subject, _ := body["subject"].(string) //nolint:errcheck // a missing subject is empty.
		s.events[parts[1]] = append(s.events[parts[1]], graph.Event{ID: id, ICalUID: "uid-" + id, Subject: subject})
		body["calendar"] = parts[1]
		st.written[id] = body
		writeJSON(w, map[string]string{"id": id})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "calendars" && parts[2] == "events" &&
		r.URL.Query().Has("$expand"):
		s.serveExistingEvents(w, parts[1])
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "events" && parts[2] == "instances":
		s.serveOccurrences(w, r, parts[1])
	case r.Method == http.MethodPatch && len(parts) == 2 && parts[0] == "events":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck // an empty change is stored as such.
		st.patched[parts[1]] = body
		writeJSON(w, map[string]any{})
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "events":
		if _, ok := st.written[parts[1]]; ok {
			delete(st.written, parts[1])

			for cal, events := range s.events {
				s.events[cal] = slices.DeleteFunc(events, func(e graph.Event) bool { return e.ID == parts[1] })
			}
		} else {
			st.deleted[parts[1]] = true
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}

	return true
}

// serveExistingEvents lists a calendar's events with the extended
// properties they were created with.
func (s *Server) serveExistingEvents(w http.ResponseWriter, calendarID string) {
	values := make([]map[string]any, 0, len(s.events[calendarID]))

	for _, e := range s.events[calendarID] {
		v := map[string]any{"id": e.ID, "iCalUId": e.ICalUID}
		if props, ok := s.eventState().written[e.ID]["singleValueExtendedProperties"]; ok {
			v["singleValueExtendedProperties"] = props
		}

		values = append(values, v)
	}

	writeJSON(w, map[string]any{"value": values})
}

// serveOccurrences lists the occurrences of a created series between
// startDateTime and endDateTime. It knows daily and weekly patterns with a
// numbered or open range, enough for tests.
func (s *Server) serveOccurrences(w http.ResponseWriter, r *http.Request, masterID string) {
	from, _ := time.Parse(time.RFC3339, r.URL.Query().Get("startDateTime")) //nolint:errcheck // a bad time lists nothing.
	to, _ := time.Parse(time.RFC3339, r.URL.Query().Get("endDateTime"))     //nolint:errcheck // as above.

	var values []map[string]string

	for _, t := range occurrences(s.eventState().written[masterID]) {
		id := masterID + "_" + t.Format("20060102T150405")
		if !t.Before(from) && t.Before(to) && !s.eventState().deleted[id] {
			values = append(values, map[string]string{"id": id, "originalStart": t.Format("2006-01-02T15:04:05Z")})
		}
	}

	writeJSON(w, map[string]any{"value": values})
}

// occurrences returns a created series' occurrence starts in UTC.
func occurrences(body map[string]any) []time.Time {
	raw, _ := json.Marshal(body) //nolint:errcheck // decoded from JSON.

	var e struct {
		Start      graph.DateTimeZone `json:"start"`
		Recurrence *graph.Recurrence  `json:"recurrence"`
	}

	_ = json.Unmarshal(raw, &e) //nolint:errcheck // as above.

	if e.Recurrence == nil {
		return nil
	}

	loc, err := time.LoadLocation(e.Start.TimeZone)
	if err != nil {
		loc = time.UTC
	}

	start, err := time.ParseInLocation("2006-01-02T15:04:05", e.Start.DateTime, loc)
	if err != nil {
		return nil
	}

	limit := e.Recurrence.Range.NumberOfOccurrences
	if limit == 0 {
		limit = 100
	}

	interval := max(e.Recurrence.Pattern.Interval, 1)

	const years, daysPerWeek, hoursPerDay = 5, 7, 24

	var out []time.Time

	for day := start; len(out) < limit && day.Before(start.AddDate(years, 0, 0)); day = day.AddDate(0, 0, 1) {
		days := int(day.Sub(start).Hours() / hoursPerDay)
		weeks := days / daysPerWeek

		switch e.Recurrence.Pattern.Type {
		case "daily":
			if days%interval == 0 {
				out = append(out, day.UTC())
			}
		case "weekly":
			if weeks%interval == 0 && slices.Contains(e.Recurrence.Pattern.DaysOfWeek, strings.ToLower(day.Weekday().String())) {
				out = append(out, day.UTC())
			}
		}
	}

	return out
}

// WrittenEvents returns the request bodies of events created through the
// API by ID.
func (s *Server) WrittenEvents() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.eventState().written)
}

// PatchedOccurrences returns the changes made to occurrences by ID.
func (s *Server) PatchedOccurrences() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.eventState().patched)
}

// DeletedOccurrences returns the IDs of deleted occurrences.
func (s *Server) DeletedOccurrences() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Sorted(maps.Keys(s.eventState().deleted))
}

// Calendars returns the calendars.
func (s *Server) Calendars() []graph.Calendar {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.calendars)
}
