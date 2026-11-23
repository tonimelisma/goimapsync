package graphtest

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// contactState is the fake's address book. Contact folders other than the
// default one ("contacts") are its children, as in Outlook.com (F46).
type contactState struct {
	folders  map[string]contactFolder
	contacts map[string]storedContact
	photos   map[string][]byte
	log      []change
	// written holds the fields of contacts created through the API.
	written map[string]map[string]any
	created int
}

type contactFolder struct {
	name, parent string
}

type storedContact struct {
	folder  string
	contact graph.Contact
}

func (s *Server) contactState() *contactState {
	if s.contactData == nil {
		s.contactData = &contactState{
			folders:  make(map[string]contactFolder),
			contacts: make(map[string]storedContact),
			photos:   make(map[string][]byte),
			written:  make(map[string]map[string]any),
		}
	}

	return s.contactData
}

// AddContactFolder creates a contact folder inside parent: the default
// folder graph.DefaultContactFolder or another contact folder.
func (s *Server) AddContactFolder(id, name, parent string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.contactState().folders[id] = contactFolder{name: name, parent: parent}
}

// PutContact creates or replaces a contact in a folder.
func (s *Server) PutContact(folderID string, c graph.Contact) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.contactState()
	if old, ok := st.contacts[c.ID]; ok && old.folder != folderID {
		s.recordContact(old.folder, c.ID)
	}

	st.contacts[c.ID] = storedContact{folder: folderID, contact: c}
	s.recordContact(folderID, c.ID)
}

// HideContact moves a contact out of the contact folders, keeping its
// properties, as Outlook.com does with contacts it takes for duplicates.
func (s *Server) HideContact(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.contactState()

	c, ok := st.contacts[id]
	if !ok {
		panic("graphtest: no contact " + id)
	}

	s.recordContact(c.folder, id)
	c.folder = HiddenContacts
	st.contacts[id] = c
}

// DeleteContact deletes a contact.
func (s *Server) DeleteContact(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.contactState()
	s.recordContact(st.contacts[id].folder, id)
	delete(st.contacts, id)
}

// SetContactPhoto sets a contact's photo. Like Graph, this is a change to
// the contact.
func (s *Server) SetContactPhoto(id string, jpeg []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.contactState()
	st.photos[id] = jpeg
	s.recordContact(st.contacts[id].folder, id)
}

func (s *Server) recordContact(folderID, id string) {
	s.seq++
	st := s.contactState()
	st.log = append(st.log, change{seq: s.seq, folder: folderID, message: id})
}

// serveContacts answers contact requests; it reports false for other paths.
func (s *Server) serveContacts(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if r.Method != http.MethodGet {
		return s.writeContacts(w, r, parts)
	}

	switch {
	case len(parts) == 3 && parts[0] == "contactFolders" && parts[2] == "contacts":
		s.serveContactList(w, parts[1])
	case len(parts) == 1 && parts[0] == "contactFolders":
		s.serveContactFolders(w, graph.DefaultContactFolder)
	case len(parts) == 3 && parts[0] == "contactFolders" && parts[2] == "childFolders":
		s.serveContactFolders(w, parts[1])
	case len(parts) == 4 && parts[0] == "contactFolders" && parts[2] == "contacts" && parts[3] == "delta":
		s.serveContactDelta(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "contacts" && parts[2] == "photo" && parts[3] == "$value":
		photo, ok := s.contactState().photos[parts[1]]
		if !ok {
			writeError(w, http.StatusNotFound, "ErrorItemNotFound", "no photo")

			return true
		}

		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(photo) //nolint:errcheck // the test client reports broken responses.
	default:
		return false
	}

	return true
}

func (s *Server) serveContactFolders(w http.ResponseWriter, parent string) {
	st := s.contactState()

	var values []map[string]string

	for _, id := range slices.Sorted(maps.Keys(st.folders)) {
		if st.folders[id].parent == parent {
			values = append(values, map[string]string{"id": id, "displayName": st.folders[id].name})
		}
	}

	writeJSON(w, map[string]any{"value": values})
}

// serveContactDelta serves a folder's contacts or their changes. Like Graph,
// contact delta takes no query options besides its own tokens (F46).
func (s *Server) serveContactDelta(w http.ResponseWriter, r *http.Request, folderID string) {
	for key := range r.URL.Query() {
		if key != "$deltatoken" && key != "$skiptoken" {
			writeError(w, http.StatusBadRequest, "ErrorInvalidUrlQuery", "not supported with change tracking: "+key)

			return
		}
	}

	st := s.contactState()
	s.serveItemDelta(w, r, folderID, st.log, slices.Sorted(maps.Keys(st.contacts)), func(id string) (any, bool) {
		stored, ok := st.contacts[id]

		return stored.contact, ok && stored.folder == folderID
	})
}

// serveItemDelta serves a delta query over a collection: every item when
// the request has no delta token, else the items changed since it. ids are
// all items' IDs in a stable order; current returns an item and whether it
// is in the collection now.
func (s *Server) serveItemDelta(
	w http.ResponseWriter, r *http.Request, collection string, log []change, ids []string, current func(id string) (any, bool),
) {
	query := r.URL.Query()

	var values []any

	if token := query.Get("$deltatoken"); token != "" {
		since, err := strconv.Atoi(token)
		if err != nil || since < s.oldest {
			writeError(w, http.StatusGone, "SyncStateNotFound", "delta token expired")

			return
		}

		seen := make(map[string]bool)

		for _, c := range log {
			if c.seq <= since || c.folder != collection || seen[c.message] {
				continue
			}

			seen[c.message] = true

			if item, ok := current(c.message); ok {
				values = append(values, item)
			} else {
				values = append(values, map[string]any{"id": c.message, "@removed": map[string]string{"reason": "deleted"}})
			}
		}
	} else {
		for _, id := range ids {
			if item, ok := current(id); ok {
				values = append(values, item)
			}
		}
	}

	start := 0
	if skip := query.Get("$skiptoken"); skip != "" {
		start, _ = strconv.Atoi(skip) //nolint:errcheck // generated below.
	}

	end := min(start+s.pageSize(), len(values))
	resp := map[string]any{"value": values[start:end]}

	if end < len(values) {
		query.Set("$skiptoken", strconv.Itoa(end))
		resp["@odata.nextLink"] = s.URL + r.URL.Path + "?" + query.Encode()
	} else {
		resp["@odata.deltaLink"] = fmt.Sprintf("%s%s?$deltatoken=%d", s.URL, r.URL.Path, s.seq)
	}

	writeJSON(w, resp)
}

// WrittenContacts returns the fields of contacts created through the API,
// with their folder under "folder", by ID.
func (s *Server) WrittenContacts() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.contactState().written)
}

// ContactFolderNames returns contact folder names by ID.
func (s *Server) ContactFolderNames() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]string)
	for id, f := range s.contactState().folders {
		out[id] = f.name
	}

	return out
}

// ContactPhoto returns a contact's photo, or nil.
func (s *Server) ContactPhoto(id string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.contactState().photos[id]
}

// writeContacts answers contact writes; it reports false for other paths.
func (s *Server) writeContacts(w http.ResponseWriter, r *http.Request, parts []string) bool {
	st := s.contactState()

	switch {
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "contacts":
		delete(st.contacts, parts[1])
		delete(st.written, parts[1])
		delete(st.photos, parts[1])
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && (len(parts) == 1 && parts[0] == "contactFolders" ||
		len(parts) == 3 && parts[0] == "contactFolders" && parts[2] == "childFolders"):
		parent := graph.DefaultContactFolder
		if len(parts) == 3 {
			parent = parts[1]
		}

		var req struct {
			DisplayName string `json:"displayName"`
		}

		_ = json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck // an empty name is stored as such.
		id := fmt.Sprintf("cf-new-%d", len(st.folders)+1)
		st.folders[id] = contactFolder{name: req.DisplayName, parent: parent}
		writeJSON(w, map[string]string{"id": id})
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "contactFolders" && parts[2] == "contacts":
		var fields map[string]any
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			writeError(w, http.StatusBadRequest, "ErrorInvalidRequest", err.Error())

			return true
		}

		raw, _ := json.Marshal(fields) //nolint:errcheck // decoded from JSON above.

		var c graph.Contact
		_ = json.Unmarshal(raw, &c) //nolint:errcheck // as above.

		st.created++
		c.ID = fmt.Sprintf("c-new-%d", st.created)
		st.contacts[c.ID] = storedContact{folder: parts[1], contact: c}
		s.recordContact(parts[1], c.ID)
		fields["folder"] = parts[1]
		st.written[c.ID] = fields
		writeJSON(w, map[string]string{"id": c.ID})
	case r.Method == http.MethodPut && len(parts) == 4 && parts[0] == "contacts" && parts[2] == "photo" && parts[3] == "$value":
		data, _ := io.ReadAll(r.Body) //nolint:errcheck // a short body is stored as such.
		st.photos[parts[1]] = data
		w.WriteHeader(http.StatusOK)
	default:
		return false
	}

	return true
}

// AllContacts is the ID of the AllContacts search folder, which lists the
// contacts of every folder; HiddenContacts is where HideContact moves a
// contact, listed only there, as Outlook.com does (F68).
const (
	AllContacts    = "allcontacts-search"
	HiddenContacts = "hidden-contacts"
)

// serveContactList lists a folder's contacts with the extended properties
// they were created with.

func (s *Server) serveContactList(w http.ResponseWriter, folderID string) {
	st := s.contactState()

	var values []map[string]any

	for _, id := range slices.Sorted(maps.Keys(st.contacts)) {
		c := st.contacts[id]
		if c.folder != folderID && folderID != AllContacts {
			continue
		}

		var v map[string]any

		raw, _ := json.Marshal(c.contact) //nolint:errcheck,errchkjson // a plain struct always encodes.
		_ = json.Unmarshal(raw, &v)       //nolint:errcheck // encoded just above.

		v["id"] = id
		if props, ok := st.written[id]["singleValueExtendedProperties"]; ok {
			v["singleValueExtendedProperties"] = props
		}

		values = append(values, v)
	}

	writeJSON(w, map[string]any{"value": values})
}
