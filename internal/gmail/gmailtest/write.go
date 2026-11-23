package gmailtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Stored is a message in the fake mailbox, for assertions.
type Stored struct {
	Raw    string
	Labels []string
}

// Messages returns every message by ID.
func (s *Server) Messages() map[string]Stored {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]Stored, len(s.messages))
	for id, m := range s.messages {
		out[id] = Stored{Raw: string(m.raw), Labels: slices.Clone(m.labels)}
	}

	return out
}

// Labels returns the user labels by ID.
func (s *Server) Labels() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]string, len(s.labels))
	for id, name := range s.labels {
		out[id] = name
	}

	return out
}

func (s *Server) createLabel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid label")

		return
	}

	// Gmail trims and collapses whitespace, and refuses its own names and
	// names over 225 characters (F71).
	req.Name = strings.Join(strings.Fields(req.Name), " ")

	system := []string{
		"inbox", "spam", "trash", "unread", "starred", "important", "sent", "draft", "drafts", "chat", "chats",
		"archive", "sent mail", "all mail", "muted",
	}
	if slices.Contains(system, strings.ToLower(req.Name)) || utf8.RuneCountInString(req.Name) > 225 || req.Name == "" {
		writeError(w, http.StatusBadRequest, "Invalid label name")

		return
	}

	// Over 255 bytes Gmail fails instead of refusing (F81).
	if len(req.Name) > maxLabelBytes {
		writeError(w, http.StatusInternalServerError, "Internal error encountered.")

		return
	}

	for _, name := range s.labels {
		if strings.EqualFold(name, req.Name) {
			writeError(w, http.StatusConflict, "Label name exists or conflicts")

			return
		}
	}

	id := fmt.Sprintf("Label_%d", len(s.labels)+1)
	s.labels[id] = req.Name
	writeJSON(w, map[string]string{"id": id, "name": req.Name, "type": "user"})
}

// systemLabels returns the system labels a message can be given.
func systemLabels() []string {
	return []string{"INBOX", "SENT", "DRAFT", "UNREAD", "STARRED", "IMPORTANT", "SPAM", "TRASH"}
}

// serveUpload stores an uploaded message, a multipart/related body of JSON
// metadata and the message as Gmail takes it.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/related" || r.URL.Query().Get("uploadType") != "multipart" {
		writeError(w, http.StatusBadRequest, "want a multipart/related upload")

		return
	}

	reader := multipart.NewReader(r.Body, params["boundary"])

	var parts [][]byte

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}

		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())

			return
		}

		data, _ := io.ReadAll(part) //nolint:errcheck // a short part fails the checks below.
		parts = append(parts, data)
	}

	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "want metadata and message")

		return
	}

	var meta struct {
		LabelIDs []string `json:"labelIds"`
	}

	if err := json.Unmarshal(parts[0], &meta); err != nil {
		writeError(w, http.StatusBadRequest, "invalid metadata")

		return
	}

	for _, l := range meta.LabelIDs {
		if _, user := s.labels[l]; !user && !slices.Contains(systemLabels(), l) {
			writeError(w, http.StatusBadRequest, "Invalid label: "+l)

			return
		}
	}

	id := fmt.Sprintf("ins-%d", len(s.messages)+1)
	s.messages[id] = &message{raw: addMissing(replaceInvalidMessageID(parts[1], id), id), labels: meta.LabelIDs}
	s.record("messagesAdded", id)
	writeJSON(w, map[string]string{"id": id})
}

// header returns a message header, or "".
func header(raw []byte, name string) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}

	return msg.Header.Get(name)
}

// search returns a filter for the messages a search query matches. It
// knows the terms pimvault uses: rfc822msgid: against the Message-ID with
// its encoded-words decoded, as Gmail indexes it (F84), from:, and after:
// and before: with Unix times compared to the Date header.
func search(q string) func(raw []byte) bool {
	var terms []func(msg *mail.Message) bool

	for term := range strings.FieldsSeq(q) {
		name, value, _ := strings.Cut(term, ":")

		switch name {
		case "rfc822msgid":
			terms = append(terms, func(msg *mail.Message) bool {
				id := msg.Header.Get("Message-Id")
				if decoded, err := new(mime.WordDecoder).DecodeHeader(id); err == nil {
					id = decoded
				}

				return id == "<"+value+">"
			})
		case "from":
			terms = append(terms, func(msg *mail.Message) bool {
				a, err := mail.ParseAddress(msg.Header.Get("From"))

				return err == nil && strings.EqualFold(a.Address, value)
			})
		case "after", "before":
			limit, _ := strconv.ParseInt(value, 10, 64) //nolint:errcheck // a bad time matches nothing useful.
			terms = append(terms, func(msg *mail.Message) bool {
				date, err := msg.Header.Date()
				if err != nil {
					return false
				}

				return name == "after" && date.Unix() > limit || name == "before" && date.Unix() < limit
			})
		}
	}

	return func(raw []byte) bool {
		msg, err := mail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			return len(terms) == 0
		}

		for _, t := range terms {
			if !t(msg) {
				return false
			}
		}

		return true
	}
}

// replaceInvalidMessageID gives a message whose Message-ID has a character
// a msg-id cannot hold a new one, as Gmail does (F52).
func replaceInvalidMessageID(raw []byte, id string) []byte {
	old := header(raw, "Message-Id")
	if old == "" || !strings.ContainsAny(old, ";[ ") {
		return raw
	}

	return bytes.Replace(raw, []byte(old), []byte("<"+id+"@mail.gmail.com>"), 1)
}

// addMissing gives a message without a Message-ID one of Gmail's, and one
// without a Date that parses the insert time, as Gmail does (F70).
func addMissing(raw []byte, id string) []byte {
	if header(raw, "Message-Id") == "" {
		raw = append([]byte("Message-ID: <"+id+"@mail.gmail.com>\r\n"), raw...)
	}

	if _, err := mail.ParseDate(header(raw, "Date")); err != nil {
		raw = append([]byte("Date: "+time.Now().Format(time.RFC1123Z)+"\r\n"), raw...)
	}

	return raw
}

// maxLabelBytes is the longest label name in bytes Gmail takes (F81).
const maxLabelBytes = 255
