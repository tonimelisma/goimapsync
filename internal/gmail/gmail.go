// Package gmail reads Gmail through its REST API (docs/requirements.md D46,
// D140) and decides which archive folder a message belongs in (D31, D32).
package gmail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// DefaultBaseURL is the Gmail API root for the signed-in user.
const DefaultBaseURL = "https://gmail.googleapis.com/gmail/v1/users/me"

// pageSize is the largest page messages.list and history.list allow.
const pageSize = "500"

// Query parameters and values.
const (
	paramFields     = "fields"
	paramMaxResults = "maxResults"
	paramFormat     = "format"
	formatRaw       = "raw"
	formatMinimal   = "minimal"
	fieldLabelIDs   = "labelIds"
)

// System label IDs.
const (
	LabelInbox = "INBOX"
	LabelDraft = "DRAFT"
	LabelSent  = "SENT"
	LabelSpam  = "SPAM"
	LabelTrash = "TRASH"

	labelImportant = "IMPORTANT"
	labelStarred   = "STARRED"
	labelUnread    = "UNREAD"
)

// ErrHistoryExpired means the stored history ID is too old; a full listing is
// needed (D50).
var ErrHistoryExpired = errors.New("history expired")

// Client calls the Gmail API with an authorized HTTP client.
type Client struct {
	http *http.Client
	base string
}

// New returns a client for baseURL, normally DefaultBaseURL.
func New(httpClient *http.Client, baseURL string) *Client {
	return &Client{http: httpClient, base: strings.TrimSuffix(baseURL, "/")}
}

// Label is a Gmail label. Type is "system" or "user".
type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// UserLabels returns the user-created labels, keyed by ID, with their names.
func (c *Client) UserLabels(ctx context.Context) (map[string]string, error) {
	var resp struct {
		Labels []Label `json:"labels"`
	}

	if err := c.get(ctx, "/labels", nil, &resp); err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}

	labels := make(map[string]string)

	for _, l := range resp.Labels {
		if l.Type == "user" {
			labels[l.ID] = l.Name
		}
	}

	return labels, nil
}

// HistoryID returns the mailbox's current history ID.
func (c *Client) HistoryID(ctx context.Context) (string, error) {
	var resp struct {
		HistoryID string `json:"historyId"`
	}

	if err := c.get(ctx, "/profile", nil, &resp); err != nil {
		return "", fmt.Errorf("get profile: %w", err)
	}

	return resp.HistoryID, nil
}

// MessageIDs lists the IDs of all messages carrying labelID, or of all
// messages when labelID is empty. Spam and Trash are never included.
func (c *Client) MessageIDs(ctx context.Context, labelID string) ([]string, error) {
	query := url.Values{}
	if labelID != "" {
		query.Set("labelIds", labelID)
	}

	return c.listIDs(ctx, query)
}

// listIDs lists the IDs of the messages a messages.list query matches,
// following every page.
func (c *Client) listIDs(ctx context.Context, query url.Values) ([]string, error) {
	var ids []string

	query.Set(paramMaxResults, pageSize)
	query.Set(paramFields, "messages/id,nextPageToken")

	for {
		var resp struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}

		if err := c.get(ctx, "/messages", query, &resp); err != nil {
			return nil, fmt.Errorf("list messages: %w", err)
		}

		for _, m := range resp.Messages {
			ids = append(ids, m.ID)
		}

		if resp.NextPageToken == "" {
			return ids, nil
		}

		query.Set("pageToken", resp.NextPageToken)
	}
}

// History returns the IDs of messages added, deleted or relabeled since
// start, and the history ID to start from next time. It returns
// ErrHistoryExpired when start is too old.
func (c *Client) History(ctx context.Context, start string) (changed []string, latest string, err error) {
	type ref struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}

	query := url.Values{"startHistoryId": {start}, paramMaxResults: {pageSize}}
	seen := make(map[string]bool)

	for {
		var resp struct {
			History []struct {
				MessagesAdded   []ref `json:"messagesAdded"`
				MessagesDeleted []ref `json:"messagesDeleted"`
				LabelsAdded     []ref `json:"labelsAdded"`
				LabelsRemoved   []ref `json:"labelsRemoved"`
			} `json:"history"`
			HistoryID     string `json:"historyId"`
			NextPageToken string `json:"nextPageToken"`
		}

		err := c.get(ctx, "/history", query, &resp)
		if errors.Is(err, googleapi.ErrNotFound) {
			return nil, "", ErrHistoryExpired
		}

		if err != nil {
			return nil, "", fmt.Errorf("list history: %w", err)
		}

		for _, h := range resp.History {
			for _, refs := range [][]ref{h.MessagesAdded, h.MessagesDeleted, h.LabelsAdded, h.LabelsRemoved} {
				for _, r := range refs {
					if !seen[r.Message.ID] {
						seen[r.Message.ID] = true
						changed = append(changed, r.Message.ID)
					}
				}
			}
		}

		latest = resp.HistoryID

		if resp.NextPageToken == "" {
			return changed, latest, nil
		}

		query.Set("pageToken", resp.NextPageToken)
	}
}

// MessageLabels returns the label IDs of one message, or
// googleapi.ErrNotFound.
func (c *Client) MessageLabels(ctx context.Context, id string) ([]string, error) {
	var resp struct {
		LabelIDs []string `json:"labelIds"`
	}

	query := url.Values{paramFormat: {formatMinimal}, paramFields: {fieldLabelIDs}}
	if err := c.get(ctx, "/messages/"+url.PathEscape(id), query, &resp); err != nil {
		return nil, fmt.Errorf("get message %s labels: %w", id, err)
	}

	return resp.LabelIDs, nil
}

// Size returns a message's size in bytes, which equals the length of its raw
// form (F78), or googleapi.ErrNotFound.
func (c *Client) Size(ctx context.Context, id string) (int64, error) {
	var resp struct {
		SizeEstimate int64 `json:"sizeEstimate"`
	}

	query := url.Values{paramFormat: {formatMinimal}, paramFields: {"sizeEstimate"}}
	if err := c.get(ctx, "/messages/"+url.PathEscape(id), query, &resp); err != nil {
		return 0, fmt.Errorf("get message %s size: %w", id, err)
	}

	return resp.SizeEstimate, nil
}

// Raw returns one message as RFC 5322 bytes, or googleapi.ErrNotFound.
func (c *Client) Raw(ctx context.Context, id string) ([]byte, error) {
	var resp struct {
		Raw string `json:"raw"`
	}

	query := url.Values{paramFormat: {formatRaw}, paramFields: {formatRaw}}
	if err := c.get(ctx, "/messages/"+url.PathEscape(id), query, &resp); err != nil {
		return nil, fmt.Errorf("get message %s: %w", id, err)
	}

	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(resp.Raw, "="))
	if err != nil {
		return nil, fmt.Errorf("decode message %s: %w", id, err)
	}

	return data, nil
}

// get sends a GET request for a path under the base URL.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	return googleapi.GetJSON(ctx, c.http, target, out)
}
