package graphtest

import (
	"bytes"
	"maps"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"
)

// serveMessageSearch answers a messages listing over every folder, filtered
// by internetMessageId or by a sentDateTime range. Like Graph, it finds no
// message by a Message-ID that holds a ';' (F58).
func (s *Server) serveMessageSearch(w http.ResponseWriter, r *http.Request) {
	match, ok := searchFilter(r.URL.Query().Get("$filter"))
	if !ok {
		writeError(w, http.StatusBadRequest, "ErrorInvalidUrlQuery", "unsupported filter")

		return
	}

	var values []map[string]any

	for _, id := range slices.Sorted(maps.Keys(s.messages)) {
		msg, err := mail.ReadMessage(bytes.NewReader(s.messages[id].raw))
		if err != nil || !match(msg) {
			continue
		}

		var headers []map[string]string

		for _, name := range []string{"Message-ID", "Date", "From"} {
			if v := msg.Header.Get(name); v != "" {
				headers = append(headers, map[string]string{"name": name, "value": v})
			}
		}

		values = append(values, map[string]any{
			"id": id, "parentFolderId": s.messages[id].folder, "internetMessageHeaders": headers,
		})
	}

	writeJSON(w, map[string]any{"value": values})
}

// searchFilter parses the two filters restore sends.
func searchFilter(filter string) (func(*mail.Message) bool, bool) {
	if want, ok := strings.CutPrefix(filter, "internetMessageId eq '"); ok {
		want = strings.ReplaceAll(strings.TrimSuffix(want, "'"), "''", "'")

		return func(msg *mail.Message) bool {
			id := strings.Join(strings.Fields(msg.Header.Get("Message-Id")), "")

			return id == want && !strings.Contains(id, ";")
		}, true
	}

	var (
		from, to time.Time
		sender   string
	)

	rangeParts := strings.Split(filter, " and ")
	if len(rangeParts) == 3 {
		sender = strings.TrimSuffix(strings.TrimPrefix(rangeParts[2], "from/emailAddress/address eq '"), "'")
		rangeParts = rangeParts[:2]
	}

	if len(rangeParts) != 2 {
		return nil, false
	}

	for _, part := range rangeParts {
		value, isFrom := strings.CutPrefix(part, "sentDateTime ge ")
		if !isFrom {
			value, _ = strings.CutPrefix(part, "sentDateTime le ")
		}

		t, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, false
		}

		if isFrom {
			from = t
		} else {
			to = t
		}
	}

	return func(msg *mail.Message) bool {
		date, err := msg.Header.Date()
		if err != nil || date.Before(from) || date.After(to) {
			return false
		}

		if sender == "" {
			return true
		}

		a, err := mail.ParseAddress(msg.Header.Get("From"))

		return err == nil && strings.EqualFold(a.Address, sender)
	}, true
}
