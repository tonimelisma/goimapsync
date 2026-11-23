package graph

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// MessageHeaders are what tells two copies of a message apart (D37): its
// Message-ID, Date and From headers, and the folder it is in. Without
// internet headers (mail Outlook made itself), Graph's own fields stand in.
type MessageHeaders struct {
	MessageID, Date, From, FolderID string
}

// WithMessageID returns the mailbox's messages that have a Message-ID, in
// any folder. Graph finds no message by a Message-ID that is not valid
// (F58); search those with Around.
func (c *Client) WithMessageID(ctx context.Context, messageID string) ([]MessageHeaders, error) {
	return c.messageHeaders(ctx, "internetMessageId eq '"+strings.ReplaceAll(messageID, "'", "''")+"'")
}

// Around returns the mailbox's messages sent within a day of t, from an
// address unless from is empty.
func (c *Client) Around(ctx context.Context, from string, t time.Time) ([]MessageHeaders, error) {
	const day = 24 * time.Hour

	filter := fmt.Sprintf("sentDateTime ge %s and sentDateTime le %s",
		t.Add(-day).UTC().Format(time.RFC3339), t.Add(day).UTC().Format(time.RFC3339))
	if from != "" {
		filter += " and from/emailAddress/address eq '" + strings.ReplaceAll(from, "'", "''") + "'"
	}

	return c.messageHeaders(ctx, filter)
}

func (c *Client) messageHeaders(ctx context.Context, filter string) ([]MessageHeaders, error) {
	var messages []struct {
		InternetMessageID string `json:"internetMessageId"`
		SentDateTime      string `json:"sentDateTime"`
		ParentFolderID    string `json:"parentFolderId"`
		From              struct {
			EmailAddress EmailAddress `json:"emailAddress"`
		} `json:"from"`
		Headers []struct {
			Name, Value string
		} `json:"internetMessageHeaders"`
	}

	query := url.Values{
		"$filter":   {filter},
		paramSelect: {"internetMessageId,sentDateTime,parentFolderId,from,internetMessageHeaders"},
		paramTop:    {"100"},
	}

	if err := list(ctx, c, "/messages?"+query.Encode(), &messages); err != nil {
		return nil, fmt.Errorf("find messages (%s): %w", filter, err)
	}

	out := make([]MessageHeaders, 0, len(messages))

	for _, m := range messages {
		h := MessageHeaders{
			MessageID: m.InternetMessageID, Date: m.SentDateTime, From: m.From.EmailAddress.Address, FolderID: m.ParentFolderID,
		}

		for _, header := range m.Headers {
			switch strings.ToLower(header.Name) {
			case "message-id":
				h.MessageID = header.Value
			case "date":
				h.Date = header.Value
			case "from":
				h.From = header.Value
			}
		}

		out = append(out, h)
	}

	return out, nil
}

// RestoreFolders is what restore needs to know of an account's mail
// folders.
type RestoreFolders struct {
	// Skipped holds the IDs of the folders backup leaves out: the
	// well-known ones such as Deleted Items and Junk Email, with the
	// folders inside them (D18).
	Skipped map[string]bool
	// User holds the lowercased names of the top-level folders that are not
	// well-known, such as the user's and Snoozed; System those of the
	// well-known ones, such as Inbox and Deleted Items, in the account's
	// language.
	User, System map[string]bool
}

// RestoreFolders lists the account's mail folders for restore.
func (c *Client) RestoreFolders(ctx context.Context) (RestoreFolders, error) {
	wellKnown, err := c.wellKnownFolders(ctx)
	if err != nil {
		return RestoreFolders{}, err
	}

	folders := RestoreFolders{Skipped: map[string]bool{}, User: map[string]bool{}, System: map[string]bool{}}

	top, err := c.childFolders(ctx, "")
	if err != nil {
		return RestoreFolders{}, fmt.Errorf("list mail folders: %w", err)
	}

	for _, f := range top {
		if _, known := wellKnown[f.ID]; known {
			folders.System[strings.ToLower(f.DisplayName)] = true
		} else {
			folders.User[strings.ToLower(f.DisplayName)] = true
		}
	}

	var walk func(id string) error

	walk = func(id string) error {
		folders.Skipped[id] = true

		children, err := c.childFolders(ctx, id)
		if err != nil {
			return err
		}

		for _, child := range children {
			if err := walk(child.ID); err != nil {
				return err
			}
		}

		return nil
	}

	for id, name := range wellKnown {
		if _, backedUp := systemPath(name); !backedUp {
			if err := walk(id); err != nil {
				return RestoreFolders{}, err
			}
		}
	}

	return folders, nil
}
