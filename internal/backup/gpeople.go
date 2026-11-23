package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gpeople"
	"github.com/tonimelisma/pimvault/internal/state"
)

// GoogleContacts backs up one Google account's contacts: one address book,
// contacts/Contacts/, with one .vcf file per contact (D34, D149).
type GoogleContacts struct {
	Client *gpeople.Client
	Store  archive.Store
	Logger *slog.Logger
	// Progress, if set, receives progress per collection.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// Run brings the contacts archive in line with the account, continuing from
// the People sync token where it can (D146).
func (g *GoogleContacts) Run(ctx context.Context, prev *state.Collections) (Result, *state.Collections, error) {
	src := &gpeopleSource{GoogleContacts: g}

	return runCollections(ctx, src, g.Store, g.Logger, g.Progress, g.Options, prev)
}

// googleContactsID is the ID of the one address book of a Google account.
const googleContactsID = "contacts"

type gpeopleSource struct {
	*GoogleContacts

	// groups are the account's contact labels by resource name; labelsHash
	// identifies their names.
	groups     map[string]string
	labelsHash string
}

func (s *gpeopleSource) collections(ctx context.Context) ([]collection, error) {
	groups, err := s.Client.Groups(ctx)
	if err != nil {
		return nil, err
	}

	s.groups = groups
	s.labelsHash = labelsHash(groups)

	return []collection{{ID: googleContactsID, Path: archive.Contacts}}, nil
}

// labelsHash identifies a set of contact labels and their names.
func labelsHash(groups map[string]string) string {
	var b strings.Builder
	for _, id := range slices.Sorted(maps.Keys(groups)) {
		b.WriteString(id + "\x00" + groups[id] + "\x00")
	}

	sum := sha256.Sum256([]byte(b.String()))

	return hex.EncodeToString(sum[:8])
}

// The collection token is the labels hash and the People sync token. Label
// changes do not show in the sync (F44), so a changed hash means a full
// listing: every member's CATEGORIES may have changed.
func (s *gpeopleSource) token(syncToken string) string {
	if syncToken == "" {
		return ""
	}

	return s.labelsHash + " " + syncToken
}

func (s *gpeopleSource) full(ctx context.Context, _ collection) (listing, error) {
	people, next, err := s.Client.Connections(ctx, "")
	if err != nil {
		return listing{}, err
	}

	return s.render(ctx, people, s.token(next)), nil
}

func (s *gpeopleSource) changes(ctx context.Context, _ collection, token string) (listing, error) {
	hash, syncToken, ok := strings.Cut(token, " ")
	if !ok || hash != s.labelsHash {
		return listing{}, tokenUnusableError("contact labels changed")
	}

	people, next, err := s.Client.Connections(ctx, syncToken)
	if errors.Is(err, gpeople.ErrSyncExpired) {
		return listing{}, errTokenExpired
	}

	if err != nil {
		return listing{}, err
	}

	return s.render(ctx, people, s.token(next)), nil
}

// render turns contacts into vCards; deleted contacts become nil items.
func (s *gpeopleSource) render(ctx context.Context, people []gpeople.Person, token string) listing {
	l := listing{items: make(map[string][]byte, len(people)), token: token}

	for i := range people {
		p := &people[i]
		id := p.ID()

		if p.Metadata.Deleted {
			l.items[id] = nil

			continue
		}

		data, err := s.contact(ctx, p)
		if err != nil {
			l.fail(id)
			s.Logger.Error("back up contact", slog.String("contact", id), slog.String("error", err.Error()))

			continue
		}

		l.items[id] = data
	}

	return l
}

// contact renders one contact with its photo.
func (s *gpeopleSource) contact(ctx context.Context, p *gpeople.Person) ([]byte, error) {
	var photo *gpeople.Photo

	if u := p.PhotoURL(); u != "" {
		var err error

		photo, err = s.Client.Photo(ctx, u)
		if errors.Is(err, gpeople.ErrPhotoGone) {
			// The photo cannot be fetched again by retrying; the contact
			// is backed up without it.
			s.Logger.Warn("contact photo is gone", slog.String("contact", p.ID()), slog.String("error", err.Error()))
		} else if err != nil {
			return nil, err
		}
	}

	return gpeople.Encode(p, s.groups, photo)
}
