package gcal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// NewAttendee is a guest of an imported event.
type NewAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
	Resource       bool   `json:"resource,omitempty"`
}

// NewReminders are an imported event's reminders.
type NewReminders struct {
	UseDefault bool       `json:"useDefault"`
	Overrides  []Reminder `json:"overrides,omitempty"`
}

// Import is an event to import as a private copy (D62): a single or
// recurring event, or a changed instance of one (OriginalStartTime set).
type Import struct {
	ICalUID           string        `json:"iCalUID"` //nolint:tagliatelle // the API's field name.
	Summary           string        `json:"summary,omitempty"`
	Description       string        `json:"description,omitempty"`
	Location          string        `json:"location,omitempty"`
	Start             DateTime      `json:"start"`
	End               DateTime      `json:"end"`
	OriginalStartTime *DateTime     `json:"originalStartTime,omitempty"`
	Recurrence        []string      `json:"recurrence,omitempty"`
	Status            string        `json:"status,omitempty"`
	Transparency      string        `json:"transparency,omitempty"`
	Visibility        string        `json:"visibility,omitempty"`
	Sequence          int           `json:"sequence,omitempty"`
	Organizer         *Person       `json:"organizer,omitempty"`
	Attendees         []NewAttendee `json:"attendees,omitempty"`
	Reminders         NewReminders  `json:"reminders"`
}

// CreateCalendar creates a secondary calendar and returns its ID.
func (c *Client) CreateCalendar(ctx context.Context, summary string) (string, error) {
	var created Calendar

	if err := c.send(ctx, http.MethodPost, c.base+"/calendars", map[string]string{"summary": summary}, &created); err != nil {
		return "", fmt.Errorf("create calendar %q: %w", summary, err)
	}

	return created.ID, nil
}

// ImportEvent imports an event into a calendar and returns its ID. An event
// with the same iCalendar UID is updated instead of duplicated (F55).
func (c *Client) ImportEvent(ctx context.Context, calendarID string, e *Import) (string, error) {
	var created Event

	target := c.base + "/calendars/" + url.PathEscape(calendarID) + "/events/import"
	if err := c.send(ctx, http.MethodPost, target, e, &created); err != nil {
		return "", fmt.Errorf("import event %s: %w", e.ICalUID, err)
	}

	return created.ID, nil
}

// DeleteEvent deletes an event; for a recurring event, the whole series.
func (c *Client) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	target := c.base + "/calendars/" + url.PathEscape(calendarID) + "/events/" + url.PathEscape(eventID)
	if err := googleapi.Do(ctx, c.http, http.MethodDelete, target, "", nil, nil); err != nil {
		return fmt.Errorf("delete event %s: %w", eventID, err)
	}

	return nil
}

// UIDs returns the iCalendar UIDs of a calendar's events that are not
// deleted.
func (c *Client) UIDs(ctx context.Context, calendarID string) (map[string]bool, error) {
	query := url.Values{paramMaxResults: {eventPageSize}, paramFields: {"items(iCalUID,status),nextPageToken"}}

	events, _, err := c.list(ctx, calendarID, query)
	if err != nil {
		return nil, fmt.Errorf("list events of calendar %s: %w", calendarID, err)
	}

	uids := make(map[string]bool, len(events))

	for i := range events {
		if events[i].Status != statusCancelled {
			uids[events[i].ICalUID] = true
		}
	}

	return uids, nil
}

func (c *Client) send(ctx context.Context, method, target string, v, out any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	return googleapi.Do(ctx, c.http, method, target, "application/json", body, out)
}
