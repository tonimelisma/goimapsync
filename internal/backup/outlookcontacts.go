package backup

import (
	"context"
	"log/slog"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/state"
)

// OutlookContacts backs up one Outlook.com account's contacts: the default
// contact folder in contacts/Contacts/ (D149), each other folder in its own
// directory, one .vcf file per contact (D34).
type OutlookContacts struct {
	Client *graph.Client
	Store  archive.Store
	Logger *slog.Logger
	// Progress, if set, receives progress per collection.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// Run brings the contacts archive in line with the account, continuing from
// each folder's delta link where it can (D146).
func (o *OutlookContacts) Run(ctx context.Context, prev *state.Collections) (Result, *state.Collections, error) {
	return runCollections(ctx, &outlookContactSource{o}, o.Store, o.Logger, o.Progress, o.Options, prev)
}

type outlookContactSource struct {
	*OutlookContacts
}

func (s *outlookContactSource) collections(ctx context.Context) ([]collection, error) {
	folders, err := s.Client.ContactFolders(ctx)
	if err != nil {
		return nil, err
	}

	paths := map[string]string{graph.DefaultContactFolder: archive.Contacts}
	for _, f := range folders {
		paths[f.ID] = archive.FolderPath(f.Path, []string{archive.Contacts})
	}

	colls := make([]collection, 0, len(paths))
	for id, path := range archive.Unique(paths) {
		colls = append(colls, collection{ID: id, Path: path})
	}

	return colls, nil
}

func (s *outlookContactSource) full(ctx context.Context, c collection) (listing, error) {
	return s.changes(ctx, c, "")
}

func (s *outlookContactSource) changes(ctx context.Context, c collection, token string) (listing, error) {
	changes, err := s.Client.ContactDelta(ctx, c.ID, token)

	return graphListing(changes, err, s.Logger, c, "contact",
		func(contact *graph.Contact) string { return contact.ID },
		func(contact *graph.Contact) ([]byte, error) { return s.render(ctx, contact) })
}

// render fetches a contact's photo and renders its file.
func (s *outlookContactSource) render(ctx context.Context, c *graph.Contact) ([]byte, error) {
	photo, err := s.Client.ContactPhoto(ctx, c.ID)
	if err != nil {
		return nil, err
	}

	return graph.EncodeContact(c, photo)
}
