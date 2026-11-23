package gpeople

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// sourceKey is the clientData key under which pimvault keeps the archive ID
// a contact was written from (D160).
const sourceKey = "pimvaultSource"

// Value is a typed value: an email address, phone number or URL.
type Value struct {
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

// NewAddress is a postal address to write.
type NewAddress struct {
	PoBox           string `json:"poBox,omitempty"`
	ExtendedAddress string `json:"extendedAddress,omitempty"`
	StreetAddress   string `json:"streetAddress,omitempty"`
	City            string `json:"city,omitempty"`
	Region          string `json:"region,omitempty"`
	PostalCode      string `json:"postalCode,omitempty"`
	Country         string `json:"country,omitempty"`
	Type            string `json:"type,omitempty"`
}

// NewName is a name to write. UnstructuredName is used when the parts are
// unknown.
type NewName struct {
	FamilyName       string `json:"familyName,omitempty"`
	GivenName        string `json:"givenName,omitempty"`
	MiddleName       string `json:"middleName,omitempty"`
	HonorificPrefix  string `json:"honorificPrefix,omitempty"`
	HonorificSuffix  string `json:"honorificSuffix,omitempty"`
	UnstructuredName string `json:"unstructuredName,omitempty"`
}

// NewOrganization is an organization to write.
type NewOrganization struct {
	Name       string `json:"name,omitempty"`
	Department string `json:"department,omitempty"`
	Title      string `json:"title,omitempty"`
}

// NewDate is a birthday or event to write: a date, or text when the date
// cannot be read.
type NewDate struct {
	Date *Date  `json:"date,omitempty"`
	Text string `json:"text,omitempty"`
	Type string `json:"type,omitempty"`
}

// NewPerson is a contact to create.
type NewPerson struct {
	Names          []NewName         `json:"names,omitempty"`
	Nicknames      []Value           `json:"nicknames,omitempty"`
	EmailAddresses []Value           `json:"emailAddresses,omitempty"`
	PhoneNumbers   []Value           `json:"phoneNumbers,omitempty"`
	Addresses      []NewAddress      `json:"addresses,omitempty"`
	Organizations  []NewOrganization `json:"organizations,omitempty"`
	Birthdays      []NewDate         `json:"birthdays,omitempty"`
	Events         []NewDate         `json:"events,omitempty"`
	URLs           []Value           `json:"urls,omitempty"`
	Biographies    []Value           `json:"biographies,omitempty"`
	Relations      []NewRelation     `json:"relations,omitempty"`
	IMClients      []NewIMClient     `json:"imClients,omitempty"`
	SIPAddresses   []Value           `json:"sipAddresses,omitempty"`
	Genders        []Value           `json:"genders,omitempty"`
	CalendarURLs   []NewCalendarURL  `json:"calendarUrls,omitempty"`
	Memberships    []NewMembership   `json:"memberships,omitempty"`
	ClientData     []NewClientData   `json:"clientData,omitempty"`
}

// NewRelation is a related person to write.
type NewRelation struct {
	Person string `json:"person"`
	Type   string `json:"type,omitempty"`
}

// NewIMClient is an instant messaging address to write.
type NewIMClient struct {
	Username string `json:"username"`
	Protocol string `json:"protocol,omitempty"`
}

// NewCalendarURL is a calendar address to write.
type NewCalendarURL struct {
	URL string `json:"url"`
}

// NewMembership puts a new contact in a contact group.
type NewMembership struct {
	ContactGroupMembership struct {
		ContactGroupResourceName string `json:"contactGroupResourceName"`
	} `json:"contactGroupMembership"`
}

// NewClientData is an app's own key and value on a contact.
type NewClientData struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// AddGroup puts the contact in a contact group.
func (p *NewPerson) AddGroup(resourceName string) {
	var m NewMembership

	m.ContactGroupMembership.ContactGroupResourceName = resourceName
	p.Memberships = append(p.Memberships, m)
}

// SetSource records the archive ID the contact is written from (D160).
func (p *NewPerson) SetSource(id string) {
	p.ClientData = append(p.ClientData, NewClientData{Key: sourceKey, Value: id})
}

// CreateGroup creates a contact group and returns its resource name.
func (c *Client) CreateGroup(ctx context.Context, name string) (string, error) {
	body := map[string]any{"contactGroup": map[string]string{"name": name}}

	var created struct {
		ResourceName string `json:"resourceName"`
	}

	if err := c.send(ctx, http.MethodPost, c.base+"/contactGroups", body, &created); err != nil {
		return "", fmt.Errorf("create contact group %q: %w", name, err)
	}

	return created.ResourceName, nil
}

// CreateContact creates a contact and returns its resource name.
func (c *Client) CreateContact(ctx context.Context, p *NewPerson) (string, error) {
	var created struct {
		ResourceName string `json:"resourceName"`
	}

	target := c.base + "/people:createContact?" + url.Values{"personFields": {"metadata"}}.Encode()
	if err := c.send(ctx, http.MethodPost, target, p, &created); err != nil {
		return "", fmt.Errorf("create contact: %w", err)
	}

	return created.ResourceName, nil
}

// SetContactPhoto sets a contact's photo.
func (c *Client) SetContactPhoto(ctx context.Context, resourceName string, data []byte) error {
	body := map[string]string{"photoBytes": base64.StdEncoding.EncodeToString(data)}
	if err := c.send(ctx, http.MethodPatch, c.base+"/"+resourceName+":updateContactPhoto", body, nil); err != nil {
		return fmt.Errorf("set photo of contact %s: %w", resourceName, err)
	}

	return nil
}

// DeleteContact deletes a contact.
func (c *Client) DeleteContact(ctx context.Context, resourceName string) error {
	if err := googleapi.Do(ctx, c.http, http.MethodDelete, c.base+"/"+resourceName+":deleteContact", "", nil, nil); err != nil {
		return fmt.Errorf("delete contact %s: %w", resourceName, err)
	}

	return nil
}

// Existing is a contact already in the account, as restore needs it.
type Existing struct {
	ResourceName string
	// Source is the archive ID pimvault wrote the contact from, or "".
	Source string
	Emails []string
}

// ExistingContacts lists the account's contacts with their email addresses
// and the archive IDs pimvault wrote them from.
func (c *Client) ExistingContacts(ctx context.Context) ([]Existing, error) {
	query := firstPage()
	query.Set("personFields", "clientData,emailAddresses")
	query.Set("sources", "READ_SOURCE_TYPE_CONTACT")

	var out []Existing

	for {
		var page struct {
			Connections []struct {
				ResourceName   string `json:"resourceName"`
				EmailAddresses []struct {
					Value string `json:"value"`
				} `json:"emailAddresses"`
				ClientData []NewClientData `json:"clientData"`
			} `json:"connections"`
			NextPageToken string `json:"nextPageToken"`
		}

		if err := googleapi.GetJSON(ctx, c.http, c.base+"/people/me/connections?"+query.Encode(), &page); err != nil {
			return nil, fmt.Errorf("list contacts: %w", err)
		}

		for _, p := range page.Connections {
			e := Existing{ResourceName: p.ResourceName}

			for _, d := range p.ClientData {
				if d.Key == sourceKey {
					e.Source = d.Value
				}
			}

			for _, a := range p.EmailAddresses {
				e.Emails = append(e.Emails, a.Value)
			}

			out = append(out, e)
		}

		if page.NextPageToken == "" {
			return out, nil
		}

		query.Set("pageToken", page.NextPageToken)
	}
}

// send sends v as JSON and decodes the response into out, unless out is
// nil.
func (c *Client) send(ctx context.Context, method, target string, v, out any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	return googleapi.Do(ctx, c.http, method, target, "application/json", body, out)
}
