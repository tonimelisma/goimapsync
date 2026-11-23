package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// CreateCalendar creates a calendar and returns its ID.
func (c *Client) CreateCalendar(ctx context.Context, name string) (string, error) {
	var created struct {
		ID string `json:"id"`
	}

	if err := c.postJSON(ctx, "/calendars", struct {
		Name string `json:"name"`
	}{name}, &created); err != nil {
		return "", fmt.Errorf("create calendar %q: %w", name, err)
	}

	return created.ID, nil
}

// CreateEvent creates an event in a calendar and returns its ID.
func (c *Client) CreateEvent(ctx context.Context, calendarID string, e *NewEvent) (string, error) {
	var created struct {
		ID string `json:"id"`
	}

	if err := c.postJSON(ctx, "/calendars/"+url.PathEscape(calendarID)+"/events", e, &created); err != nil {
		return "", fmt.Errorf("create event: %w", err)
	}

	return created.ID, nil
}

// UpdateEvent changes an event or one occurrence of a series.
func (c *Client) UpdateEvent(ctx context.Context, id string, e *NewEvent) error {
	if err := c.sendJSON(ctx, http.MethodPatch, "/events/"+url.PathEscape(id), e); err != nil {
		return fmt.Errorf("update event %s: %w", id, err)
	}

	return nil
}

// DeleteEvent deletes an event: a whole series, or one occurrence.
func (c *Client) DeleteEvent(ctx context.Context, id string) error {
	if err := c.send(ctx, http.MethodDelete, "/events/"+url.PathEscape(id), "", nil, nil); err != nil {
		return fmt.Errorf("delete event %s: %w", id, err)
	}

	return nil
}

// Occurrence is one occurrence of a series; OriginalStart is in UTC.
type Occurrence struct {
	ID            string `json:"id"`
	OriginalStart string `json:"originalStart"`
}

// Occurrences returns a series' occurrences that start between from and to.
func (c *Client) Occurrences(ctx context.Context, masterID string, from, to time.Time) ([]Occurrence, error) {
	query := url.Values{
		"startDateTime": {from.UTC().Format(time.RFC3339)},
		"endDateTime":   {to.UTC().Format(time.RFC3339)},
		paramSelect:     {"originalStart"},
		paramTop:        {"100"},
	}

	var out []Occurrence
	if err := list(ctx, c, "/events/"+url.PathEscape(masterID)+"/instances?"+query.Encode(), &out); err != nil {
		return nil, fmt.Errorf("list occurrences of event %s: %w", masterID, err)
	}

	return out, nil
}

// ExistingEvents returns a calendar's single events and series masters with
// their iCalendar UIDs (as backup writes them) and the archive UIDs
// pimvault wrote them from.
func (c *Client) ExistingEvents(ctx context.Context, calendarID string) ([]Existing, error) {
	var events []struct {
		ID       string             `json:"id"`
		ICalUID  string             `json:"iCalUId"`
		Extended []extendedProperty `json:"singleValueExtendedProperties"`
	}

	query := url.Values{
		paramSelect: {"iCalUId"},
		paramExpand: {"singleValueExtendedProperties($filter=id eq '" + sourceProperty + "')"},
		paramTop:    {"100"},
	}

	if err := list(ctx, c, "/calendars/"+url.PathEscape(calendarID)+"/events?"+query.Encode(), &events); err != nil {
		return nil, fmt.Errorf("list events of calendar %s: %w", calendarID, err)
	}

	out := make([]Existing, 0, len(events))

	for _, e := range events {
		x := Existing{ID: e.ID, UID: seriesUID(&Event{ICalUID: e.ICalUID})}
		for _, p := range e.Extended {
			x.Source = p.Value
		}

		out = append(out, x)
	}

	return out, nil
}

// sendJSON sends v as JSON with a method and discards the response.
func (c *Client) sendJSON(ctx context.Context, method, target string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	return c.send(ctx, method, target, "", body, nil)
}
