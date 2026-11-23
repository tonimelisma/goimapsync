package graph

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// DefaultContactFolder is the well-known name of the default contacts
// folder. Other contact folders are its children (F46).
const DefaultContactFolder = "contacts"

// photoLimit bounds a downloaded contact photo.
const photoLimit = 10 << 20

// ContactFolder is a contact folder other than the default one. Path is its
// name and the names of the folders above it, outermost first, below the
// default folder.
type ContactFolder struct {
	ID   string
	Path []string
}

// ContactFolders returns every contact folder below the default one.
func (c *Client) ContactFolders(ctx context.Context) ([]ContactFolder, error) {
	var out []ContactFolder

	var walk func(target string, parent []string) error

	walk = func(target string, parent []string) error {
		var folders []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		}

		if err := list(ctx, c, target, &folders); err != nil {
			return fmt.Errorf("list contact folders: %w", err)
		}

		for _, f := range folders {
			path := append(append([]string(nil), parent...), f.DisplayName)
			out = append(out, ContactFolder{ID: f.ID, Path: path})

			if err := walk("/contactFolders/"+url.PathEscape(f.ID)+"/childFolders", path); err != nil {
				return err
			}
		}

		return nil
	}

	if err := walk("/contactFolders", nil); err != nil {
		return nil, err
	}

	return out, nil
}

// PhysicalAddress is a postal address.
type PhysicalAddress struct {
	Street          string `json:"street"`
	City            string `json:"city"`
	State           string `json:"state"`
	CountryOrRegion string `json:"countryOrRegion"`
	PostalCode      string `json:"postalCode"`
}

// Contact is an Outlook contact, with the fields pimvault stores (D150).
type Contact struct {
	ID                   string          `json:"id"`
	DisplayName          string          `json:"displayName"`
	GivenName            string          `json:"givenName"`
	MiddleName           string          `json:"middleName"`
	Surname              string          `json:"surname"`
	Title                string          `json:"title"`
	Generation           string          `json:"generation"`
	NickName             string          `json:"nickName"`
	EmailAddresses       []EmailAddress  `json:"emailAddresses"`
	IMAddresses          []string        `json:"imAddresses"`
	BusinessPhones       []string        `json:"businessPhones"`
	HomePhones           []string        `json:"homePhones"`
	MobilePhone          string          `json:"mobilePhone"`
	HomeAddress          PhysicalAddress `json:"homeAddress"`
	BusinessAddress      PhysicalAddress `json:"businessAddress"`
	OtherAddress         PhysicalAddress `json:"otherAddress"`
	CompanyName          string          `json:"companyName"`
	Department           string          `json:"department"`
	JobTitle             string          `json:"jobTitle"`
	BusinessHomePage     string          `json:"businessHomePage"`
	Birthday             string          `json:"birthday"`
	PersonalNotes        string          `json:"personalNotes"`
	SpouseName           string          `json:"spouseName"`
	Children             []string        `json:"children"`
	Categories           []string        `json:"categories"`
	LastModifiedDateTime string          `json:"lastModifiedDateTime"`
	Removed              *struct{}       `json:"@removed"`
}

func (c *Contact) itemID() string { return c.ID }
func (c *Contact) removed() bool  { return c.Removed != nil }

// ContactDelta returns the changes to a contact folder since link, or all
// of its contacts when link is empty. It returns ErrDeltaExpired when link
// can no longer be used.
func (c *Client) ContactDelta(ctx context.Context, folderID, link string) (Changes[Contact], error) {
	target := link
	if target == "" {
		// Contact delta takes no query options (F46).
		target = "/contactFolders/" + url.PathEscape(folderID) + "/contacts/delta"
	}

	changes, err := delta[Contact](ctx, c, target)
	if err != nil {
		return Changes[Contact]{}, fmt.Errorf("list contact changes: %w", err)
	}

	return changes, nil
}

// Photo is a contact photo.
type Photo struct {
	Data        []byte
	ContentType string
}

// ContactPhoto returns a contact's photo, or nil when it has none.
func (c *Client) ContactPhoto(ctx context.Context, id string) (*Photo, error) {
	target := "/contacts/" + url.PathEscape(id) + "/photo/$value"

	body, contentType, err := c.getWithType(ctx, target)
	if errors.Is(err, ErrNotFound) {
		return nil, nil //nolint:nilnil // no photo is not an error.
	}

	if err != nil {
		return nil, fmt.Errorf("get photo of contact %s: %w", id, err)
	}
	defer body.Close() //nolint:errcheck // read-only body; a close error changes nothing.

	data, err := io.ReadAll(io.LimitReader(body, photoLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read photo of contact %s: %w", id, err)
	}

	if len(data) > photoLimit {
		return nil, fmt.Errorf("read photo of contact %s: larger than %d bytes", id, photoLimit)
	}

	return &Photo{Data: data, ContentType: contentType}, nil
}

// CreateContactFolder creates a contact folder inside parent, the default
// folder or another contact folder, and returns its ID.
func (c *Client) CreateContactFolder(ctx context.Context, parentID, name string) (string, error) {
	target := "/contactFolders"
	if parentID != DefaultContactFolder {
		target = "/contactFolders/" + url.PathEscape(parentID) + "/childFolders"
	}

	var created struct {
		ID string `json:"id"`
	}

	if err := c.postJSON(ctx, target, displayName{name}, &created); err != nil {
		return "", fmt.Errorf("create contact folder %q: %w", name, err)
	}

	return created.ID, nil
}

// CreateContact creates a contact in a folder
// and returns its ID.
func (c *Client) CreateContact(ctx context.Context, folderID string, contact *NewContact) (string, error) {
	var created struct {
		ID string `json:"id"`
	}

	target := "/contactFolders/" + url.PathEscape(folderID) + "/contacts"
	if err := c.postJSON(ctx, target, contact, &created); err != nil {
		return "", fmt.Errorf("create contact: %w", err)
	}

	return created.ID, nil
}

// SetContactPhoto sets a contact's photo.
func (c *Client) SetContactPhoto(ctx context.Context, id string, data []byte, contentType string) error {
	// Sent as the default JSON, a photo without a media type would be
	// refused.
	contentType = cmp.Or(contentType, http.DetectContentType(data))

	if err := c.send(ctx, http.MethodPut, "/contacts/"+url.PathEscape(id)+"/photo/$value", contentType, data, nil); err != nil {
		return fmt.Errorf("set photo of contact %s: %w", id, err)
	}

	return nil
}

// DeleteContact deletes a contact.
func (c *Client) DeleteContact(ctx context.Context, id string) error {
	if err := c.send(ctx, http.MethodDelete, "/contacts/"+url.PathEscape(id), "", nil, nil); err != nil {
		return fmt.Errorf("delete contact %s: %w", id, err)
	}

	return nil
}

// AllContacts returns the contacts Outlook's AllContacts search folder
// lists: those of every contact folder and those Outlook.com moved out of
// them as duplicates (F67, F68), not those in Deleted Items, with the
// archive IDs pimvault wrote them from. An account without that folder has
// none.
func (c *Client) AllContacts(ctx context.Context) ([]Existing, error) {
	var folder struct {
		ID string `json:"id"`
	}

	err := c.getJSON(ctx, "/mailFolders/allcontacts?$select=id", &folder)
	if noSuchFolder(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("find all contacts: %w", err)
	}

	return c.ExistingContacts(ctx, folder.ID)
}

// contactFields are the fields of NewContact, which ExistingContacts reads
// for fingerprints.
const contactFields = "displayName,givenName,middleName,surname,title,generation,nickName,emailAddresses,imAddresses," +
	"businessPhones,homePhones,mobilePhone,homeAddress,businessAddress,otherAddress,companyName,department,jobTitle," +
	"businessHomePage,birthday,personalNotes,spouseName,children,categories"

// ExistingContacts returns the contacts of a folder with the archive IDs
// pimvault wrote them from and their fingerprints. Contact delta takes no
// query options (F46), so this lists the folder.
func (c *Client) ExistingContacts(ctx context.Context, folderID string) ([]Existing, error) {
	var contacts []struct {
		ID string `json:"id"`
		NewContact
	}

	query := url.Values{
		paramSelect: {"id," + contactFields},
		paramExpand: {"singleValueExtendedProperties($filter=id eq '" + sourceProperty + "')"},
		paramTop:    {"500"},
	}

	if err := list(ctx, c, "/contactFolders/"+url.PathEscape(folderID)+"/contacts?"+query.Encode(), &contacts); err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}

	out := make([]Existing, 0, len(contacts))

	for i := range contacts {
		ct := &contacts[i]
		e := Existing{ID: ct.ID, Fingerprint: ct.Fingerprint()}
		for _, p := range ct.Extended {
			e.Source = p.Value
		}

		for _, a := range ct.EmailAddresses {
			e.Emails = append(e.Emails, a.Address)
		}

		out = append(out, e)
	}

	return out, nil
}
