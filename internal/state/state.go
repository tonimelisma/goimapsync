// Package state stores each account's sync state, such as change tokens, as
// one JSON file in the state directory (D51, D52). State
// is a rebuildable projection: a missing or unreadable file only costs a full
// listing, never data.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/atomicfile"
)

const (
	dirPerm  = 0o700
	filePerm = 0o600
)

// Account is the sync state of one account.
type Account struct {
	GmailMail   *GmailMail   `json:"gmailMail,omitempty"`
	OutlookMail *OutlookMail `json:"outlookMail,omitempty"`
	Calendars   *Collections `json:"calendars,omitempty"`
	Contacts    *Collections `json:"contacts,omitempty"`
	Tasks       *Collections `json:"tasks,omitempty"`
}

// GmailMail is where the next Gmail mail backup continues from.
type GmailMail struct {
	// HistoryID is the mailbox history ID the archive is complete up to,
	// apart from the messages in Retry.
	HistoryID string `json:"historyId"`
	// Labels maps user label IDs to the names the archive's folders were
	// built from. A rename or a new or deleted label needs a full listing,
	// because Gmail's history does not record it.
	Labels map[string]string `json:"labels"`
	// Archive and Files are the mail directory and its number of message
	// files after the last run. A different directory or count means the
	// archive changed outside pimvault, which needs a full listing.
	Archive string `json:"archive"`
	Files   int    `json:"files"`
	// Retry lists messages that failed last time; they are checked again.
	Retry []string `json:"retry,omitempty"`
}

// Store reads and writes state files in one directory.
type Store struct {
	dir string
}

// NewStore returns the state store in dir.
func NewStore(dir string) Store {
	return Store{dir: dir}
}

func (s Store) path(id account.ID) string {
	return filepath.Join(s.dir, id.DirName()+".json")
}

// Load returns an account's state; a missing file is an empty state. An
// unreadable file returns an empty state and the error, so the caller can
// report it and continue with a full listing.
func (s Store) Load(id account.ID) (Account, error) {
	var st Account

	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}

	if err != nil {
		return st, fmt.Errorf("read state %s: %w", s.path(id), err)
	}

	if err := json.Unmarshal(data, &st); err != nil {
		return Account{}, fmt.Errorf("decode state %s: %w", s.path(id), err)
	}

	return st, nil
}

// Save writes an account's state atomically.
func (s Store) Save(id account.ID, st *Account) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	if err := atomicfile.Write(s.path(id), data, filePerm); err != nil {
		return fmt.Errorf("write state %s: %w", s.path(id), err)
	}

	return nil
}

// OutlookMail is where the next Outlook.com mail backup continues from.
type OutlookMail struct {
	// Folders maps folder IDs to the directory each was backed up to and the
	// delta link that continues its changes.
	Folders map[string]OutlookFolder `json:"folders"`
	// Archive and Files work as in GmailMail.
	Archive string `json:"archive"`
	Files   int    `json:"files"`
	// Retry maps messages that failed last time to the directory they
	// belong in ("" for a file to delete). A later move or deletion shows up
	// in a folder's changes and takes precedence.
	Retry map[string]string `json:"retry,omitempty"`
}

// OutlookFolder is one folder's backup position.
type OutlookFolder struct {
	Path      string `json:"path"`
	DeltaLink string `json:"deltaLink"`
}

// Collections is where the next backup of calendars, address books or task
// lists continues from.
type Collections struct {
	// Collections maps collection IDs to the directory each was backed up
	// to and the change token that continues it ("" when the provider has
	// none and the collection is listed in full every run).
	Collections map[string]Collection `json:"collections"`
	// Archive and Files work as in GmailMail.
	Archive string `json:"archive"`
	Files   int    `json:"files"`
}

// Collection is one collection's backup position.
type Collection struct {
	Path  string `json:"path"`
	Token string `json:"token,omitempty"`
}
