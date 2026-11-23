package gmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// uploadBase is the media upload root that matches the API root: Google
// serves uploads under /upload. A root without the usual path (a test fake)
// gets /upload appended.
func (c *Client) uploadBase() string {
	if up := strings.Replace(c.base, "/gmail/v1/", "/upload/gmail/v1/", 1); up != c.base {
		return up
	}

	return c.base + "/upload"
}

// SystemLabel returns the ID of the system label a folder name names, such
// as SPAM for "Spam", whatever the case.
func SystemLabel(name string) (string, bool) {
	id := strings.ToUpper(name)

	switch id {
	case LabelSpam, LabelTrash, labelImportant, labelStarred, labelUnread:
		return id, true
	default:
		return "", false
	}
}

// InvalidLabelName reports whether Gmail refused a label name, such as one
// of its own ("Chats") or one over 225 characters (F71).
func InvalidLabelName(err error) bool {
	var apiErr *googleapi.APIError

	return errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest && strings.Contains(apiErr.Message, "Invalid label name")
}

// CreateLabel creates a user label and returns its ID. Gmail trims and
// collapses the name's whitespace (F71).
func (c *Client) CreateLabel(ctx context.Context, name string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"name": name, "labelListVisibility": "labelShow", "messageListVisibility": "show",
	})
	if err != nil {
		return "", fmt.Errorf("encode label %q: %w", name, err)
	}

	var created Label
	if err := googleapi.Do(ctx, c.http, http.MethodPost, c.base+"/labels", "application/json", body, &created); err != nil {
		return "", fmt.Errorf("create label %q: %w", name, err)
	}

	return created.ID, nil
}

// Insert adds a message to the mailbox with the given labels, as delivered
// mail would arrive but without scanning or classification (D55). Its date
// in Gmail is its Date header. Without the UNREAD label it is read (D153);
// with the DRAFT label it is a draft that keeps its headers (D165, F63).
func (c *Client) Insert(ctx context.Context, raw []byte, labelIDs []string) error {
	target := c.uploadBase() + "/messages?uploadType=multipart&internalDateSource=dateHeader"

	return c.upload(ctx, target, map[string]any{fieldLabelIDs: labelIDs}, raw)
}

// upload sends a multipart upload: JSON metadata, then the message.
func (c *Client) upload(ctx context.Context, target string, metadata any, raw []byte) error {
	meta, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode upload metadata: %w", err)
	}

	var body bytes.Buffer

	w := multipart.NewWriter(&body)

	for _, p := range []struct {
		contentType string
		data        []byte
	}{{"application/json; charset=UTF-8", meta}, {"message/rfc822", raw}} {
		part, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {p.contentType}})
		if err != nil {
			return fmt.Errorf("build upload: %w", err)
		}

		if _, err := part.Write(p.data); err != nil {
			return fmt.Errorf("build upload: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("build upload: %w", err)
	}

	contentType := "multipart/related; boundary=" + w.Boundary()
	if err := googleapi.Do(ctx, c.http, http.MethodPost, target, contentType, body.Bytes(), nil); err != nil {
		return fmt.Errorf("upload message: %w", err)
	}

	return nil
}

// Headers are the headers that tell two copies of a message apart (D37),
// and its subject, as Gmail gives them: display names decoded (F52).
type Headers struct {
	MessageID, Date, From, Subject string
}

// WithMessageID returns the Headers of the mailbox's messages that have
// the given Message-ID, in Spam and Trash too when spamTrash is set.
func (c *Client) WithMessageID(ctx context.Context, messageID string, spamTrash bool) ([]Headers, error) {
	return c.search(ctx, "rfc822msgid:"+strings.Trim(messageID, "<>"), spamTrash)
}

// Around returns the Headers of the mailbox's messages from an address and
// dated within a day of t, in Spam and Trash too when spamTrash is set. An
// empty from matches every sender.
func (c *Client) Around(ctx context.Context, from string, t time.Time, spamTrash bool) ([]Headers, error) {
	const day = 24 * time.Hour

	q := fmt.Sprintf("after:%d before:%d", t.Add(-day).Unix(), t.Add(day).Unix())
	if from != "" {
		q = "from:" + from + " " + q
	}

	return c.search(ctx, q, spamTrash)
}

// search returns the Headers of the messages a search matches.
func (c *Client) search(ctx context.Context, q string, spamTrash bool) ([]Headers, error) {
	query := url.Values{"q": {q}}
	if spamTrash {
		query.Set("includeSpamTrash", "true")
	}

	ids, err := c.listIDs(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", q, err)
	}

	out := make([]Headers, 0, len(ids))

	for _, id := range ids {
		var msg struct {
			Payload struct {
				Headers []struct {
					Name, Value string
				} `json:"headers"`
			} `json:"payload"`
		}

		query := url.Values{
			paramFormat: {"metadata"}, "metadataHeaders": {"Message-ID", "Date", "From", "Subject"}, paramFields: {"payload/headers"},
		}
		if err := c.get(ctx, "/messages/"+url.PathEscape(id), query, &msg); err != nil {
			return nil, fmt.Errorf("get headers of message %s: %w", id, err)
		}

		var h Headers

		for _, header := range msg.Payload.Headers {
			switch strings.ToLower(header.Name) {
			case "message-id":
				h.MessageID = header.Value
			case "date":
				h.Date = header.Value
			case "from":
				h.From = header.Value
			case "subject":
				h.Subject = header.Value
			}
		}

		out = append(out, h)
	}

	return out, nil
}
