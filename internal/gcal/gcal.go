// Package gcal reads Google Calendar through its REST API (D82, D140) and
// renders events as iCalendar (D7, D43, D150).
package gcal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// DefaultBaseURL is the Calendar API root.
const DefaultBaseURL = "https://www.googleapis.com/calendar/v3"

// Page sizes: the largest events.list allows, and calendarList's.
const (
	eventPageSize    = "2500"
	calendarPageSize = "250"
)

// Query parameter names.
const (
	paramMaxResults  = "maxResults"
	paramShowDeleted = "showDeleted"
	paramFields      = "fields"
	valueTrue        = "true"
)

// ErrSyncExpired means a stored sync token can no longer be used; the
// calendar must be listed in full (D50).
var ErrSyncExpired = errors.New("sync token expired")

// Client calls the Calendar API with an authorized HTTP client.
type Client struct {
	http *http.Client
	base string
}

// New returns a client for baseURL, normally DefaultBaseURL.
func New(httpClient *http.Client, baseURL string) *Client {
	return &Client{http: httpClient, base: strings.TrimSuffix(baseURL, "/")}
}

// Reminder is a reminder before an event.
type Reminder struct {
	Method  string `json:"method"`
	Minutes int    `json:"minutes"`
}

// Calendar is one of the account's calendars. DataOwner is the email of the
// calendar's owner; it is set only for secondary calendars.
type Calendar struct {
	ID               string     `json:"id"`
	Summary          string     `json:"summary"`
	Primary          bool       `json:"primary"`
	DataOwner        string     `json:"dataOwner"`
	TimeZone         string     `json:"timeZone"`
	DefaultReminders []Reminder `json:"defaultReminders"`
}

// Calendars returns the calendars the account has the owner role on. That
// role can also be given to others by sharing, so callers check DataOwner
// for calendars the account owns itself (D20). Subscribed calendars (D21),
// including holiday calendars (D148), have a lower role and are not
// returned.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	query := url.Values{"minAccessRole": {"owner"}, "showHidden": {valueTrue}, paramMaxResults: {calendarPageSize}}

	var calendars []Calendar

	for {
		var page struct {
			Items         []Calendar `json:"items"`
			NextPageToken string     `json:"nextPageToken"`
		}

		if err := googleapi.GetJSON(ctx, c.http, c.base+"/users/me/calendarList?"+query.Encode(), &page); err != nil {
			return nil, fmt.Errorf("list calendars: %w", err)
		}

		calendars = append(calendars, page.Items...)

		if page.NextPageToken == "" {
			return calendars, nil
		}

		query.Set("pageToken", page.NextPageToken)
	}
}

// DateTime is an event time: Date for all-day events, DateTime (RFC 3339)
// otherwise, with the IANA time zone it was set in.
type DateTime struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"dateTime,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

// Person is an event's organizer.
type Person struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// Attendee is one of an event's guests.
type Attendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
	ResponseStatus string `json:"responseStatus"`
	Optional       bool   `json:"optional"`
	Resource       bool   `json:"resource"`
}

// EntryPoint is one way to join an event's conference.
type EntryPoint struct {
	EntryPointType string `json:"entryPointType"`
	URI            string `json:"uri"`
	Label          string `json:"label"`
}

// Attachment is a file attached to an event, stored as a link.
type Attachment struct {
	FileURL  string `json:"fileUrl"`
	Title    string `json:"title"`
	MimeType string `json:"mimeType"`
}

// Event is a Google Calendar event: a single event, a recurring event, or
// one changed or canceled instance of a recurring event.
type Event struct {
	ID                string     `json:"id"`
	ICalUID           string     `json:"iCalUID"` //nolint:tagliatelle // the API's field name.
	Status            string     `json:"status"`
	EventType         string     `json:"eventType"`
	Summary           string     `json:"summary"`
	Description       string     `json:"description"`
	Location          string     `json:"location"`
	Start             DateTime   `json:"start"`
	End               DateTime   `json:"end"`
	Recurrence        []string   `json:"recurrence"`
	RecurringEventID  string     `json:"recurringEventId"`
	OriginalStartTime DateTime   `json:"originalStartTime"`
	Created           string     `json:"created"`
	Updated           string     `json:"updated"`
	Sequence          int        `json:"sequence"`
	Transparency      string     `json:"transparency"`
	Visibility        string     `json:"visibility"`
	Organizer         Person     `json:"organizer"`
	Attendees         []Attendee `json:"attendees"`
	Reminders         struct {
		UseDefault bool       `json:"useDefault"`
		Overrides  []Reminder `json:"overrides"`
	} `json:"reminders"`
	HangoutLink    string `json:"hangoutLink"`
	ConferenceData struct {
		EntryPoints []EntryPoint `json:"entryPoints"`
	} `json:"conferenceData"`
	Attachments        []Attachment `json:"attachments"`
	BirthdayProperties struct {
		// Contact is set for birthdays and other dates that Google
		// Calendar shows from the contacts (D148).
		Contact string `json:"contact"`
	} `json:"birthdayProperties"`
}

// SeriesID is the ID shared by a recurring event and its instances: the
// recurring event's own ID. It names the event's archive file (D43, D45).
func (e *Event) SeriesID() string {
	if e.RecurringEventID != "" {
		return e.RecurringEventID
	}

	return e.ID
}

// Events lists a calendar's events, including canceled ones: all of them
// when syncToken is empty, or those changed since syncToken. It returns the
// sync token for next time, or ErrSyncExpired.
func (c *Client) Events(ctx context.Context, calendarID, syncToken string) ([]Event, string, error) {
	query := url.Values{paramMaxResults: {eventPageSize}, paramShowDeleted: {valueTrue}, "singleEvents": {"false"}}
	if syncToken != "" {
		query.Set("syncToken", syncToken)
	}

	events, next, err := c.list(ctx, calendarID, query)
	if err != nil {
		return nil, "", fmt.Errorf("list events of calendar %s: %w", calendarID, err)
	}

	return events, next, nil
}

// Series returns every event with an iCalendar UID: a recurring event and
// its changed and canceled instances, or a single event.
func (c *Client) Series(ctx context.Context, calendarID, iCalUID string) ([]Event, error) {
	query := url.Values{paramMaxResults: {eventPageSize}, paramShowDeleted: {valueTrue}, "iCalUID": {iCalUID}}

	events, _, err := c.list(ctx, calendarID, query)
	if err != nil {
		return nil, fmt.Errorf("list event %s of calendar %s: %w", iCalUID, calendarID, err)
	}

	return events, nil
}

// Event returns one event, or googleapi.ErrNotFound.
func (c *Client) Event(ctx context.Context, calendarID, eventID string) (Event, error) {
	var e Event

	target := c.base + "/calendars/" + url.PathEscape(calendarID) + "/events/" + url.PathEscape(eventID)
	if err := googleapi.GetJSON(ctx, c.http, target, &e); err != nil {
		return Event{}, fmt.Errorf("get event %s of calendar %s: %w", eventID, calendarID, err)
	}

	return e, nil
}

func (c *Client) list(ctx context.Context, calendarID string, query url.Values) ([]Event, string, error) {
	base := c.base + "/calendars/" + url.PathEscape(calendarID) + "/events?"

	var events []Event

	for {
		var page struct {
			Items         []Event `json:"items"`
			NextPageToken string  `json:"nextPageToken"`
			NextSyncToken string  `json:"nextSyncToken"`
		}

		err := googleapi.GetJSON(ctx, c.http, base+query.Encode(), &page)

		var apiErr *googleapi.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusGone {
			return nil, "", ErrSyncExpired
		}

		if err != nil {
			return nil, "", err
		}

		events = append(events, page.Items...)

		if page.NextPageToken == "" {
			return events, page.NextSyncToken, nil
		}

		query.Set("pageToken", page.NextPageToken)
	}
}
