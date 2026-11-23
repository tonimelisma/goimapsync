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
	"github.com/tonimelisma/pimvault/internal/gpeople"
)

// GoogleContacts writes archived contacts into Google Contacts.
// contacts/Contacts/ goes to the account's contacts, other directories also
// to a contact group of the same name (D152), and each category to a group
// of its name (D67); groups are created if missing. A contact is already
// there when the account has a contact with its ID or one written from it
// by pimvault (D160), and in a migrate also when the account had a contact
// with one of its email addresses before the run (D41). Photos are set
// after the contact is created.
type GoogleContacts struct {
	Client   *gpeople.Client
	Source   archive.Store
	Migrate  bool
	Logger   *slog.Logger
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
}

type googleContactsRun struct {
	*GoogleContacts

	res    Result
	ids    map[string]bool   // resource names and archive IDs (D160) of the account's contacts
	emails map[string]bool   // addresses the account had before the run
	groups map[string]string // group name → resource name
}

// Run writes every archived contact the account does not have yet.
func (g *GoogleContacts) Run(ctx context.Context) (Result, error) {
	r := &googleContactsRun{GoogleContacts: g, ids: map[string]bool{}, emails: map[string]bool{}, groups: map[string]string{}}

	return runItems(ctx, r, &r.res, g.Source, g.Progress, g.Logger)
}

// index reads the account's contacts and contact groups.
func (r *googleContactsRun) index(ctx context.Context) error {
	groups, err := r.Client.Groups(ctx)
	if err != nil {
		return err
	}

	for resourceName, name := range groups {
		r.groups[name] = resourceName
	}

	existing, err := r.Client.ExistingContacts(ctx)
	if err != nil {
		return err
	}

	for _, e := range existing {
		r.ids[e.ResourceName] = true
		if e.Source != "" {
			r.ids[e.Source] = true
		}

		for _, a := range e.Emails {
			r.emails[strings.ToLower(a)] = true
		}
	}

	return nil
}

func (r *googleContactsRun) restore(ctx context.Context, folder, id string) error {
	raw, err := r.Source.Read(folder, id)
	if err != nil {
		return err
	}

	c, err := readGoogleContact(raw)
	if err != nil {
		return err
	}

	item := path.Join(folder, id)
	present := c.uid != "" && r.ids[c.uid] ||
		r.Migrate && slices.ContainsFunc(c.emails, func(e string) bool { return r.emails[e] })

	if present || r.DryRun {
		r.res.count(r.DryRun, r.Planned, item, present)

		return nil
	}

	if groupErr := r.addGroups(ctx, c, folder); groupErr != nil {
		return groupErr
	}

	if c.uid != "" {
		c.person.SetSource(c.uid)
	}

	resourceName, err := r.Client.CreateContact(ctx, c.person)
	if err != nil {
		return err
	}

	if c.photo != nil {
		if err := r.Client.SetContactPhoto(ctx, resourceName, c.photo.data); err != nil {
			// Deleted again, so the next run writes it whole.
			return errors.Join(err, gone(r.Client.DeleteContact(ctx, resourceName)))
		}
	}

	r.res.Written++
	r.ids[resourceName] = true

	if c.uid != "" {
		r.ids[c.uid] = true
	}

	return nil
}

// addGroups puts a contact in the groups of its categories and of its
// directory unless that is the default one.
func (r *googleContactsRun) addGroups(ctx context.Context, c *googleContact, folder string) error {
	names := slices.Clone(c.categories)
	if folder != archive.Contacts {
		names = append(names, strings.Join(archive.FolderSegments(folder), "/"))
	}

	for _, name := range slices.Compact(slices.Sorted(slices.Values(names))) {
		group, err := r.group(ctx, name)
		if err != nil {
			return err
		}

		c.person.AddGroup(group)
	}

	return nil
}

// group returns the resource name of the contact group with a name,
// creating it if missing.
func (r *googleContactsRun) group(ctx context.Context, name string) (string, error) {
	if resourceName, ok := r.groups[name]; ok {
		return resourceName, nil
	}

	resourceName, err := r.Client.CreateGroup(ctx, name)
	if err != nil {
		return "", err
	}

	r.groups[name] = resourceName

	return resourceName, nil
}
