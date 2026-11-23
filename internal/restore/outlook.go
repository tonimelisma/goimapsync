package restore

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/graph"
)

// OutlookContacts writes archived contacts into an Outlook.com account.
// contacts/Contacts/ goes to the default contact folder, other directories
// to contact folders of the same name, created if missing (D152). A contact
// is already there when the account has a contact with its ID, one written
// from it by pimvault (D160), also among those Outlook.com moved out of the
// folders as duplicates (D177), or, in its folder, one identical in the fields
// pimvault writes (D176), and in a migrate also when the account had a
// contact with one of its email addresses before the run (D41). Photos are
// set after the contact is created.
type OutlookContacts struct {
	Client   *graph.Client
	Source   archive.Store
	Migrate  bool
	Logger   *slog.Logger
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
}

type outlookContactsRun struct {
	*OutlookContacts

	res     Result
	ids     map[string]bool // IDs and archive IDs (D160) of the account's contacts
	same    map[string]bool // folder ID and fingerprint of the account's contacts (D176)
	emails  map[string]bool
	folders map[string]string // lowercased folder path (names joined by NUL) → ID
}

// Run writes every archived contact the account does not have yet.
func (o *OutlookContacts) Run(ctx context.Context) (Result, error) {
	r := &outlookContactsRun{
		OutlookContacts: o, ids: map[string]bool{}, same: map[string]bool{}, emails: map[string]bool{}, folders: map[string]string{},
	}

	return runItems(ctx, r, &r.res, o.Source, o.Progress, o.Logger)
}

// index reads the account's contact folders, contact IDs and email
// addresses.
func (r *outlookContactsRun) index(ctx context.Context) error {
	folders, err := r.Client.ContactFolders(ctx)
	if err != nil {
		return err
	}

	ids := []string{graph.DefaultContactFolder}

	for _, f := range folders {
		r.folders[strings.ToLower(strings.Join(f.Path, "\x00"))] = f.ID
		ids = append(ids, f.ID)
	}

	for _, id := range ids {
		existing, err := r.Client.ExistingContacts(ctx, id)
		if err != nil {
			return err
		}

		for _, e := range existing {
			r.ids[e.ID] = true

			if e.Fingerprint != "" {
				r.same[id+"\x00"+e.Fingerprint] = true
			}
			if e.Source != "" {
				r.ids[e.Source] = true
			}

			for _, a := range e.Emails {
				r.emails[strings.ToLower(a)] = true
			}
		}
	}

	// Contacts Outlook.com took for duplicates and moved out of the folders
	// keep the archive IDs pimvault wrote them from (F68): writing them
	// again would only be removed again.
	all, allErr := r.Client.AllContacts(ctx)
	if allErr != nil {
		return allErr
	}

	for _, e := range all {
		r.ids[e.ID] = true
		if e.Source != "" {
			r.ids[e.Source] = true
		}
	}

	return nil
}

func (r *outlookContactsRun) restore(ctx context.Context, folder, id string) error {
	raw, err := r.Source.Read(folder, id)
	if err != nil {
		return err
	}

	c, err := outlookContact(raw)
	if err != nil {
		return err
	}

	item := path.Join(folder, id)
	same := r.sameKey(folder, c)
	present := c.uid != "" && r.ids[c.uid] || same != "" && r.same[same] ||
		r.Migrate && slices.ContainsFunc(c.emails, func(e string) bool { return r.emails[e] })

	if present || r.DryRun {
		r.res.count(r.DryRun, r.Planned, item, present)

		if same != "" {
			r.same[same] = true
		}

		return nil
	}

	if err := r.write(ctx, folder, c); err != nil {
		return err
	}

	r.res.Written++

	if same != "" {
		r.same[same] = true
	}

	return nil
}

// write creates a contact in the folder of an archive directory, with its
// archive ID and photo.
func (r *outlookContactsRun) write(ctx context.Context, folder string, c *parsedContact) error {
	folderID, err := r.folderID(ctx, folder)
	if err != nil {
		return err
	}

	if c.uid != "" {
		c.contact.SetSource(c.uid)
	}

	newID, err := r.Client.CreateContact(ctx, folderID, c.contact)
	if err != nil {
		return err
	}

	if c.photo != nil {
		if err := r.Client.SetContactPhoto(ctx, newID, c.photo.data, c.photo.contentType); err != nil {
			// Deleted again, so the next run writes it whole.
			return errors.Join(err, gone(r.Client.DeleteContact(ctx, newID)))
		}
	}

	r.ids[newID] = true

	if c.uid != "" {
		r.ids[c.uid] = true
	}

	return nil
}

// sameKey returns the key under which a contact identical to c in the
// folder of an archive directory is indexed (D176), or "" when there is
// nothing to look for: the folder does not exist yet, c has none of the
// fields compared, or c has a photo, which the comparison leaves out.
func (r *outlookContactsRun) sameKey(folder string, c *parsedContact) string {
	fingerprint := c.contact.Fingerprint()
	if fingerprint == "" || c.photo != nil {
		return ""
	}

	folderID := graph.DefaultContactFolder
	if folder != archive.Contacts {
		var ok bool
		if folderID, ok = r.folders[strings.ToLower(strings.Join(archive.FolderSegments(folder), "\x00"))]; !ok {
			return ""
		}
	}

	return folderID + "\x00" + fingerprint
}

// folderID returns the contact folder for an archive directory, creating it
// and its parents when missing.
func (r *outlookContactsRun) folderID(ctx context.Context, folder string) (string, error) {
	if folder == archive.Contacts {
		return graph.DefaultContactFolder, nil
	}

	segments := archive.FolderSegments(folder)
	parent := graph.DefaultContactFolder

	for i := range segments {
		key := strings.ToLower(strings.Join(segments[:i+1], "\x00"))

		id, ok := r.folders[key]
		if !ok {
			var err error

			id, err = r.Client.CreateContactFolder(ctx, parent, segments[i])
			if err != nil {
				return "", err
			}

			r.folders[key] = id
		}

		parent = id
	}

	return parent, nil
}
