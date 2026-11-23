// Package gpeopletest is an in-memory fake of the parts of the Google People
// API that pimvault uses, for tests.
package gpeopletest

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tonimelisma/pimvault/internal/gpeople"
)

// thumbnailBytes is how much of a photo the fake serves as a thumbnail.
const thumbnailBytes = 4

// Server is a fake Google account's contacts served over HTTP. Photos are
// served under /photos/.
type Server struct {
	// URL is the API root to pass to gpeople.New.
	URL string

	mu      sync.Mutex
	people  map[string]gpeople.Person // resource name → contact
	changed map[string]int            // resource name → change number
	groups  map[string]string         // resource name → name
	photos  map[string][]byte         // photo ID → JPEG
	seq     int
	oldest  int
	// PageSize limits contact pages; 0 means 1000.
	PageSize int
	// FailPhotos makes photo downloads fail with this status; 0 means none.
	FailPhotos int
	// FailPhotoUploads makes setting photos fail with this status.
	FailPhotoUploads int
	// written holds the request bodies of contacts created through the API.
	written map[string]map[string]any
	// uploaded holds photos set through the API.
	uploaded map[string][]byte
	created  int
}

// New starts a fake server; it is closed when the test ends.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{
		people:   make(map[string]gpeople.Person),
		changed:  make(map[string]int),
		groups:   make(map[string]string),
		photos:   make(map[string][]byte),
		written:  make(map[string]map[string]any),
		uploaded: make(map[string][]byte),
	}

	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// Put creates or replaces a contact.
func (s *Server) Put(p gpeople.Person) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	s.people[p.ResourceName] = p
	s.changed[p.ResourceName] = s.seq
}

// Delete deletes a contact. Like the People API, a change listing reports
// it with metadata.deleted set.
func (s *Server) Delete(resourceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var p gpeople.Person
	p.ResourceName = resourceName
	p.Metadata.Deleted = true

	s.seq++
	s.people[resourceName] = p
	s.changed[resourceName] = s.seq
}

// SetGroup creates or renames a user contact group. Like the People API,
// this is not a change to the group's members.
func (s *Server) SetGroup(resourceName, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.groups[resourceName] = name
}

// SetPhoto stores a photo and returns its thumbnail URL, as contacts list
// it.
func (s *Server) SetPhoto(id string, jpeg []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.photos[id] = jpeg

	return s.URL + "/photos/" + id + "=s100"
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

	switch {
	case r.Method != http.MethodGet:
		s.serveWrite(w, r)
	case r.URL.Path == "/people/me/connections":
		s.serveConnections(w, r)
	case r.URL.Path == "/contactGroups":
		s.serveGroups(w)
	case strings.HasPrefix(r.URL.Path, "/photos/"):
		s.servePhoto(w, strings.TrimPrefix(r.URL.Path, "/photos/"))
	default:
		writeError(w, http.StatusNotFound, "", r.URL.Path)
	}
}

func (s *Server) serveConnections(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("personFields") == "" {
		writeError(w, http.StatusBadRequest, "", "missing personFields")

		return
	}

	since := 0

	if tok := q.Get("syncToken"); tok != "" {
		n, err := strconv.Atoi(tok)
		if err != nil || n < s.oldest {
			writeError(w, http.StatusBadRequest, "EXPIRED_SYNC_TOKEN", "sync token expired")

			return
		}

		since = n
	}

	var names []string

	for name, p := range s.people {
		if s.changed[name] > since && (since > 0 || !p.Metadata.Deleted) {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	start, _ := strconv.Atoi(q.Get("pageToken")) //nolint:errcheck // an empty token is the first page.
	size := s.PageSize
	if size == 0 {
		size = 1000
	}

	end := min(start+size, len(names))
	page := map[string]any{"connections": s.list(names[start:end], strings.Contains(q.Get("personFields"), "clientData"))}

	if end < len(names) {
		page["nextPageToken"] = strconv.Itoa(end)
	} else if q.Get("requestSyncToken") == "true" {
		page["nextSyncToken"] = strconv.Itoa(s.seq)
	}

	writeJSON(w, page)
}

// list returns contacts, with the clientData they were created with when
// asked for.
func (s *Server) list(names []string, clientData bool) []map[string]any {
	people := make([]map[string]any, 0, len(names))

	for _, name := range names {
		raw, _ := json.Marshal(s.people[name]) //nolint:errcheck // a plain struct.

		var p map[string]any
		_ = json.Unmarshal(raw, &p) //nolint:errcheck // as above.

		if data, ok := s.written[name]["clientData"]; ok && clientData {
			p["clientData"] = data
		}

		people = append(people, p)
	}

	return people
}

func (s *Server) serveGroups(w http.ResponseWriter) {
	groups := []map[string]string{
		{"resourceName": "contactGroups/myContacts", "name": "myContacts", "groupType": "SYSTEM_CONTACT_GROUP"},
	}

	for _, name := range slices.Sorted(maps.Keys(s.groups)) {
		groups = append(groups, map[string]string{"resourceName": name, "name": s.groups[name], "groupType": "USER_CONTACT_GROUP"})
	}

	writeJSON(w, map[string]any{"contactGroups": groups})
}

// servePhoto serves a photo; like Google, "=s0" is the original and other
// sizes a thumbnail (here: the first bytes).
func (s *Server) servePhoto(w http.ResponseWriter, path string) {
	id, size, _ := strings.Cut(path, "=")

	data, ok := s.photos[id]

	switch {
	case s.FailPhotos != 0:
		w.WriteHeader(s.FailPhotos)

		return
	case !ok:
		w.WriteHeader(http.StatusNotFound)

		return
	case size != "s0":
		data = data[:min(len(data), thumbnailBytes)]
	}

	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data) //nolint:errcheck // a test client that went away changes nothing.
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // a test client that went away changes nothing.
}

// writeError writes a Google error, with a reason in google.rpc.ErrorInfo
// the way the People API sends one.
func writeError(w http.ResponseWriter, status int, reason, msg string) {
	body := map[string]any{"code": status, "message": msg}
	if reason != "" {
		body["details"] = []map[string]string{{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason}}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body}) //nolint:errcheck // as above.
}
