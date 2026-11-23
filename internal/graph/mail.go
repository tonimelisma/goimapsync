package graph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tonimelisma/pimvault/internal/archive"
)

// Well-known names of the folders that are backed up.
const (
	nameInbox     = "inbox"
	nameDrafts    = "drafts"
	nameSentItems = "sentitems"
	nameArchive   = "archive"
)

// wellKnownNames are Graph's well-known folder names. The first four are
// backed up (D18); the others are system folders that are not backed up,
// with everything inside them: Deleted Items, Junk Email, Conversation
// History (D18), and the rest, which are not user-created.
func wellKnownNames() []string {
	return []string{
		nameInbox, nameDrafts, nameSentItems, nameArchive,
		"deleteditems", "junkemail", "conversationhistory", "outbox", "clutter",
		"conflicts", "localfailures", "scheduled", "serverfailures", "syncissues",
		"searchfolders", "recoverableitemsdeletions",
	}
}

// systemPath returns the archive directory of a backed-up well-known folder
// (D143), or false for one that is not backed up.
func systemPath(name string) (string, bool) {
	switch name {
	case nameInbox:
		return archive.Inbox, true
	case nameDrafts:
		return archive.Drafts, true
	case nameSentItems:
		return archive.Sent, true
	case nameArchive:
		return archive.Archive, true
	default:
		return "", false
	}
}

// Folder is a mail folder to back up and its archive directory.
type Folder struct {
	ID   string
	Path string
}

type mailFolder struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	ChildFolderCount int    `json:"childFolderCount"`
}

// MailFolders returns the folders to back up (D18) with their archive
// directories: system folders by role (D143), user folders by name, nested
// folders as nested directories (D142).
func (c *Client) MailFolders(ctx context.Context) ([]Folder, error) {
	wellKnown, err := c.wellKnownFolders(ctx)
	if err != nil {
		return nil, err
	}

	var folders []Folder

	var walk func(parentID, parentPath string) error

	walk = func(parentID, parentPath string) error {
		children, err := c.childFolders(ctx, parentID)
		if err != nil {
			return err
		}

		for _, f := range children {
			path, include := folderPath(f, parentPath, wellKnown)
			if !include {
				continue
			}

			folders = append(folders, Folder{ID: f.ID, Path: path})

			if f.ChildFolderCount > 0 {
				if err := walk(f.ID, path); err != nil {
					return err
				}
			}
		}

		return nil
	}

	if err := walk("", ""); err != nil {
		return nil, fmt.Errorf("list mail folders: %w", err)
	}

	return folders, nil
}

// folderPath returns a folder's archive directory, or false for a folder
// that is not backed up.
func folderPath(f mailFolder, parentPath string, wellKnown map[string]string) (string, bool) {
	if name, known := wellKnown[f.ID]; known {
		return systemPath(name)
	}

	if parentPath == "" {
		return archive.FolderPath([]string{f.DisplayName}, archive.MailFolders()), true
	}

	return archive.Subfolder(parentPath, f.DisplayName), true
}

// wellKnownFolders maps the IDs of the account's well-known folders to their
// names. A name the account does not have or does not support (404, 400 or
// 403) is skipped.
// noSuchFolder reports whether Graph refused a well-known folder name the
// mailbox does not support: it answers 404, 400 or 403 for those.
func noSuchFolder(err error) bool {
	var apiErr *APIError

	return errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound ||
		apiErr.Status == http.StatusBadRequest || apiErr.Status == http.StatusForbidden)
}

func (c *Client) wellKnownFolders(ctx context.Context) (map[string]string, error) {
	ids := make(map[string]string)

	for _, name := range wellKnownNames() {
		var f mailFolder

		err := c.getJSON(ctx, "/mailFolders/"+name+"?$select=id", &f)

		if noSuchFolder(err) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("get %s folder: %w", name, err)
		}

		ids[f.ID] = name
	}

	return ids, nil
}

// childFolders lists the folders directly inside parentID, or the top-level
// folders when parentID is empty. Hidden folders are not listed.
func (c *Client) childFolders(ctx context.Context, parentID string) ([]mailFolder, error) {
	target := "/mailFolders?$select=id,displayName,childFolderCount&$top=100"
	if parentID != "" {
		target = "/mailFolders/" + url.PathEscape(parentID) + "/childFolders?$select=id,displayName,childFolderCount&$top=100"
	}

	var folders []mailFolder

	if err := list(ctx, c, target, &folders); err != nil {
		return nil, err
	}

	return folders, nil
}

// Delta is one round of changes to a folder's messages.
type Delta struct {
	// Added lists messages new or changed in the folder, Removed those that
	// left it (deleted, or moved to another folder).
	Added   []string
	Removed []string
	// Link continues from here next time.
	Link string
}

// MessageDelta returns the changes to a folder's messages since link, or all
// of its messages when link is empty. It returns ErrDeltaExpired when link
// can no longer be used.
func (c *Client) MessageDelta(ctx context.Context, folderID, link string) (Delta, error) {
	target := link
	if target == "" {
		target = "/mailFolders/" + url.PathEscape(folderID) + "/messages/delta?$select=id"
	}

	changes, err := delta[messageRef](ctx, c, target)
	if err != nil {
		return Delta{}, fmt.Errorf("list folder changes: %w", err)
	}

	d := Delta{Removed: changes.Removed, Link: changes.Link}
	for _, m := range changes.Changed {
		d.Added = append(d.Added, m.ID)
	}

	return d, nil
}

// messageRef is a message in a delta round.
type messageRef struct {
	ID      string    `json:"id"`
	Removed *struct{} `json:"@removed"`
}

func (m *messageRef) itemID() string { return m.ID }
func (m *messageRef) removed() bool  { return m.Removed != nil }

// expired reports whether err means a delta link must be dropped: 410 Gone,
// or a sync state error code.
func expired(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch strings.ToLower(apiErr.Code) {
	case "syncstatenotfound", "syncstateinvalid", "resyncrequired":
		return true
	default:
		return apiErr.Status == http.StatusGone
	}
}

// Raw returns one message as MIME (RFC 5322) bytes.
func (c *Client) Raw(ctx context.Context, id string) ([]byte, error) {
	body, err := c.get(ctx, "/messages/"+url.PathEscape(id)+"/$value")
	if err != nil {
		return nil, fmt.Errorf("get message %s: %w", id, err)
	}
	defer body.Close() //nolint:errcheck // read-only body; a close error changes nothing.

	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read message %s: %w", id, err)
	}

	return data, nil
}
