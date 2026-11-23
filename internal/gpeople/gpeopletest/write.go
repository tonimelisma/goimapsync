package gpeopletest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/tonimelisma/pimvault/internal/gpeople"
)

// serveWrite answers contact and group writes.
func (s *Server) serveWrite(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	var body map[string]any
	if r.Method != http.MethodDelete {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "", err.Error())

			return
		}
	}

	switch {
	case r.Method == http.MethodPost && path == "contactGroups":
		group, _ := body["contactGroup"].(map[string]any) //nolint:errcheck // a missing group has no name.
		name, _ := group["name"].(string)                 //nolint:errcheck // as above.
		resourceName := fmt.Sprintf("contactGroups/g-new-%d", len(s.groups)+1)
		s.groups[resourceName] = name
		writeJSON(w, map[string]string{"resourceName": resourceName, "name": name})
	case r.Method == http.MethodPost && path == "people:createContact":
		raw, _ := json.Marshal(body) //nolint:errcheck // decoded from JSON above.

		var p gpeople.Person
		_ = json.Unmarshal(raw, &p) //nolint:errcheck // as above.

		s.created++
		p.ResourceName = fmt.Sprintf("people/c-new-%d", s.created)
		s.seq++
		s.people[p.ResourceName] = p
		s.changed[p.ResourceName] = s.seq
		s.written[p.ResourceName] = body
		writeJSON(w, map[string]string{"resourceName": p.ResourceName})
	case r.Method == http.MethodPatch && strings.HasSuffix(path, ":updateContactPhoto"):
		if s.FailPhotoUploads != 0 {
			writeError(w, s.FailPhotoUploads, "", "injected failure")

			return
		}

		encoded, _ := body["photoBytes"].(string)           //nolint:errcheck // an empty photo is stored as such.
		data, _ := base64.StdEncoding.DecodeString(encoded) //nolint:errcheck // as above.
		s.uploaded[strings.TrimSuffix(path, ":updateContactPhoto")] = data
		writeJSON(w, map[string]any{})
	case r.Method == http.MethodDelete && strings.HasSuffix(path, ":deleteContact"):
		name := strings.TrimSuffix(path, ":deleteContact")
		delete(s.people, name)
		delete(s.written, name)
		delete(s.uploaded, name)
		writeJSON(w, map[string]any{})
	default:
		writeError(w, http.StatusNotFound, "", path)
	}
}

// Written returns the request bodies of contacts created through the API,
// by resource name.
func (s *Server) Written() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.written)
}

// Groups returns the user contact groups' names by resource name.
func (s *Server) Groups() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.groups)
}

// Uploaded returns the photo set on a contact through the API, or nil.
func (s *Server) Uploaded(resourceName string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.uploaded[resourceName]
}
