// Package gpeople reads Google Contacts through the People API
// (D83, D140) and renders contacts as vCard 4.0 (D42).
package gpeople

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// DefaultBaseURL is the People API root.
const DefaultBaseURL = "https://people.googleapis.com/v1"

// personFields are the person fields pimvault stores (D150).
const personFields = "addresses,biographies,birthdays,calendarUrls,emailAddresses,events,genders," +
	"imClients,memberships,metadata,names,nicknames,organizations,phoneNumbers,photos,relations,sipAddresses,urls"

// photoLimit bounds a downloaded contact photo.
const photoLimit = 10 << 20

// ErrPhotoGone means a contact's photo URL no longer serves the photo.
var ErrPhotoGone = errors.New("photo gone")

// ErrSyncExpired means a stored sync token can no longer be used; People
// sync tokens expire 7 days after the full listing they came from.
var ErrSyncExpired = errors.New("sync token expired")

// Client calls the People API. Photos are fetched with a separate client
// that carries no credentials: photo URLs need none.
type Client struct {
	http   *http.Client
	photos *http.Client
	base   string
}

// New returns a client for baseURL, normally DefaultBaseURL.
func New(api, photos *http.Client, baseURL string) *Client {
	return &Client{http: api, photos: photos, base: strings.TrimSuffix(baseURL, "/")}
}

// Date is a full or partial date; zero fields are unknown.
type Date struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// FieldMetadata marks a person's primary value of a kind.
type FieldMetadata struct {
	Primary bool `json:"primary"`
}

// Name is one of a contact's names.
type Name struct {
	DisplayName     string        `json:"displayName"`
	FamilyName      string        `json:"familyName"`
	GivenName       string        `json:"givenName"`
	MiddleName      string        `json:"middleName"`
	HonorificPrefix string        `json:"honorificPrefix"`
	HonorificSuffix string        `json:"honorificSuffix"`
	Metadata        FieldMetadata `json:"metadata"`
}

// Person is one Google contact.
type Person struct {
	ResourceName string `json:"resourceName"`
	Metadata     struct {
		Deleted bool `json:"deleted"`
		Sources []struct {
			Type       string `json:"type"`
			UpdateTime string `json:"updateTime"`
		} `json:"sources"`
	} `json:"metadata"`
	Names     []Name `json:"names"`
	Nicknames []struct {
		Value string `json:"value"`
	} `json:"nicknames"`
	EmailAddresses []struct {
		Value    string        `json:"value"`
		Type     string        `json:"type"`
		Metadata FieldMetadata `json:"metadata"`
	} `json:"emailAddresses"`
	PhoneNumbers []struct {
		Value    string        `json:"value"`
		Type     string        `json:"type"`
		Metadata FieldMetadata `json:"metadata"`
	} `json:"phoneNumbers"`
	Addresses []struct {
		PoBox           string `json:"poBox"`
		ExtendedAddress string `json:"extendedAddress"`
		StreetAddress   string `json:"streetAddress"`
		City            string `json:"city"`
		Region          string `json:"region"`
		PostalCode      string `json:"postalCode"`
		Country         string `json:"country"`
		Type            string `json:"type"`
	} `json:"addresses"`
	Organizations []struct {
		Name       string `json:"name"`
		Department string `json:"department"`
		Title      string `json:"title"`
	} `json:"organizations"`
	Birthdays []struct {
		Date *Date  `json:"date"`
		Text string `json:"text"`
	} `json:"birthdays"`
	Events []struct {
		Date *Date  `json:"date"`
		Type string `json:"type"`
	} `json:"events"`
	URLs []struct {
		Value string `json:"value"`
		Type  string `json:"type"`
	} `json:"urls"`
	Biographies []struct {
		Value string `json:"value"`
	} `json:"biographies"`
	Relations []struct {
		Person string `json:"person"`
		Type   string `json:"type"`
	} `json:"relations"`
	IMClients []struct {
		Username string `json:"username"`
		Protocol string `json:"protocol"`
	} `json:"imClients"`
	SIPAddresses []struct {
		Value string `json:"value"`
	} `json:"sipAddresses"`
	Genders []struct {
		Value string `json:"value"`
	} `json:"genders"`
	CalendarURLs []struct {
		URL string `json:"url"`
	} `json:"calendarUrls"`
	Memberships []struct {
		ContactGroupMembership struct {
			ContactGroupResourceName string `json:"contactGroupResourceName"`
		} `json:"contactGroupMembership"`
	} `json:"memberships"`
	Photos []struct {
		URL     string `json:"url"`
		Default bool   `json:"default"`
	} `json:"photos"`
}

// ID is the contact's archive file name: its resource name without the
// "people/" prefix (D45).
func (p *Person) ID() string {
	return strings.TrimPrefix(p.ResourceName, "people/")
}

// sizeSuffix is the size option Google appends to photo URLs, such as
// "=s100" for a 100-pixel thumbnail.
var sizeSuffix = regexp.MustCompile(`=s\d+$`)

// PhotoURL returns the URL of the contact's photo at full size, or "" when
// the contact has only a default avatar (D151). Listed URLs end in "=s100",
// a 100-pixel thumbnail; "=s0" serves the original size (F44).
func (p *Person) PhotoURL() string {
	for _, photo := range p.Photos {
		if !photo.Default && photo.URL != "" {
			return sizeSuffix.ReplaceAllString(photo.URL, "=s0")
		}
	}

	return ""
}

// Connections lists the account's contacts (not "Other contacts", D22): all
// of them when syncToken is empty, or those changed since syncToken,
// including deleted ones. It returns the sync token for next time, or
// ErrSyncExpired.
func (c *Client) Connections(ctx context.Context, syncToken string) ([]Person, string, error) {
	query := firstPage()
	query.Set("personFields", personFields)
	query.Set("requestSyncToken", "true")
	query.Set("sources", "READ_SOURCE_TYPE_CONTACT")

	if syncToken != "" {
		query.Set("syncToken", syncToken)
	}

	var people []Person

	for {
		var page struct {
			Connections   []Person `json:"connections"`
			NextPageToken string   `json:"nextPageToken"`
			NextSyncToken string   `json:"nextSyncToken"`
		}

		err := googleapi.GetJSON(ctx, c.http, c.base+"/people/me/connections?"+query.Encode(), &page)

		var apiErr *googleapi.APIError
		if errors.As(err, &apiErr) && (apiErr.Reason == "EXPIRED_SYNC_TOKEN" || apiErr.Status == http.StatusGone) {
			return nil, "", ErrSyncExpired
		}

		if err != nil {
			return nil, "", fmt.Errorf("list contacts: %w", err)
		}

		people = append(people, page.Connections...)

		if page.NextPageToken == "" {
			return people, page.NextSyncToken, nil
		}

		query.Set("pageToken", page.NextPageToken)
	}
}

// Groups returns the names of the account's own contact groups ("labels")
// by resource name. System groups such as "myContacts" are left out.
func (c *Client) Groups(ctx context.Context) (map[string]string, error) {
	query := firstPage()
	groups := make(map[string]string)

	for {
		var page struct {
			ContactGroups []struct {
				ResourceName string `json:"resourceName"`
				Name         string `json:"name"`
				GroupType    string `json:"groupType"`
			} `json:"contactGroups"`
			NextPageToken string `json:"nextPageToken"`
		}

		if err := googleapi.GetJSON(ctx, c.http, c.base+"/contactGroups?"+query.Encode(), &page); err != nil {
			return nil, fmt.Errorf("list contact groups: %w", err)
		}

		for _, g := range page.ContactGroups {
			if g.GroupType == "USER_CONTACT_GROUP" {
				groups[g.ResourceName] = g.Name
			}
		}

		if page.NextPageToken == "" {
			return groups, nil
		}

		query.Set("pageToken", page.NextPageToken)
	}
}

// Photo is a downloaded contact photo.
type Photo struct {
	Data        []byte
	ContentType string
}

// Photo downloads a photo from its URL.
func (c *Client) Photo(ctx context.Context, photoURL string) (*Photo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, photoURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build photo request: %w", err)
	}

	resp, err := c.photos.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download photo: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close error changes nothing.

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		return nil, fmt.Errorf("download photo: %w: http %d", ErrPhotoGone, resp.StatusCode)
	default:
		return nil, fmt.Errorf("download photo: http %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, photoLimit+1))
	if err != nil {
		return nil, fmt.Errorf("download photo: %w", err)
	}

	if len(data) > photoLimit {
		return nil, fmt.Errorf("download photo: larger than %d bytes", photoLimit)
	}

	return &Photo{Data: data, ContentType: resp.Header.Get("Content-Type")}, nil
}

// firstPage returns a query for the first page of a listing, with the
// largest page size the People API serves.
func firstPage() url.Values {
	return url.Values{"pageSize": {"1000"}}
}
