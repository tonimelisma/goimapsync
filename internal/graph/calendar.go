package graph

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
)

// preferText asks for event bodies as plain text instead of HTML.
const preferText = `outlook.body-content-type="text"`

// Calendar is one of the account's calendars.
type Calendar struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IsDefaultCalendar bool   `json:"isDefaultCalendar"`
	CanEdit           bool   `json:"canEdit"`
	IsRemovable       bool   `json:"isRemovable"`
	Owner             struct {
		Address string `json:"address"`
	} `json:"owner"`
}

// MailboxTimeZone returns the account's time zone as Outlook names it: a
// Windows zone name, or an IANA one. A personal account's settings may
// lack the time zone and carry only its working hours' (F65); "" when
// neither is set.
func (c *Client) MailboxTimeZone(ctx context.Context) (string, error) {
	var out struct {
		TimeZone     string `json:"timeZone"`
		WorkingHours struct {
			TimeZone struct {
				Name string `json:"name"`
			} `json:"timeZone"`
		} `json:"workingHours"`
	}

	if err := c.getJSON(ctx, "/mailboxSettings", &out); err != nil {
		return "", fmt.Errorf("read mailbox time zone: %w", err)
	}

	return cmp.Or(out.TimeZone, out.WorkingHours.TimeZone.Name), nil
}

// Calendars returns all of the account's calendars.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	var calendars []Calendar

	if err := list(ctx, c, "/calendars?$top=100", &calendars); err != nil {
		return nil, fmt.Errorf("list calendars: %w", err)
	}

	return calendars, nil
}

// DateTimeZone is a Graph date-time with its time zone. Without a time zone
// preference Graph returns UTC.
type DateTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// EmailAddress is a person's name and address.
type EmailAddress struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// EventAttendee is one of an event's guests.
type EventAttendee struct {
	EmailAddress EmailAddress `json:"emailAddress"`
	Type         string       `json:"type"`
	Status       struct {
		Response string `json:"response"`
	} `json:"status"`
}

// RecurrencePattern and RecurrenceRange describe a series (Graph's
// patternedRecurrence).
type RecurrencePattern struct {
	Type           string   `json:"type,omitempty"`
	Interval       int      `json:"interval,omitempty"`
	Month          int      `json:"month,omitempty"`
	DayOfMonth     int      `json:"dayOfMonth,omitempty"`
	DaysOfWeek     []string `json:"daysOfWeek,omitempty"`
	FirstDayOfWeek string   `json:"firstDayOfWeek,omitempty"`
	Index          string   `json:"index,omitempty"`
}

// Recurrence is how an event series or a recurring task repeats.
type Recurrence struct {
	Pattern RecurrencePattern `json:"pattern,omitempty"`
	Range   RecurrenceRange   `json:"range,omitempty"`
}

// RecurrenceRange is when a series starts and ends.
type RecurrenceRange struct {
	Type                string `json:"type,omitempty"`
	StartDate           string `json:"startDate,omitempty"`
	EndDate             string `json:"endDate,omitempty"`
	NumberOfOccurrences int    `json:"numberOfOccurrences,omitempty"`
	RecurrenceTimeZone  string `json:"recurrenceTimeZone,omitempty"`
}

// Event is an Outlook event: a single event, a series master or an
// exception of a series.
type Event struct {
	ID                    string                              `json:"id"`
	ICalUID               string                              `json:"iCalUId"`
	Type                  string                              `json:"type"`
	SeriesMasterID        string                              `json:"seriesMasterId"`
	Subject               string                              `json:"subject"`
	Body                  struct{ Content string }            `json:"body"`
	Start                 DateTimeZone                        `json:"start"`
	End                   DateTimeZone                        `json:"end"`
	OriginalStart         string                              `json:"originalStart"`
	OriginalStartTimeZone string                              `json:"originalStartTimeZone"`
	OriginalEndTimeZone   string                              `json:"originalEndTimeZone"`
	IsAllDay              bool                                `json:"isAllDay"`
	IsCancelled           bool                                `json:"isCancelled"`
	ShowAs                string                              `json:"showAs"`
	Sensitivity           string                              `json:"sensitivity"`
	Importance            string                              `json:"importance"`
	Categories            []string                            `json:"categories"`
	Location              struct{ DisplayName string }        `json:"location"`
	Organizer             struct{ EmailAddress EmailAddress } `json:"organizer"`
	Attendees             []EventAttendee                     `json:"attendees"`
	IsReminderOn          bool                                `json:"isReminderOn"`
	ReminderMinutes       int                                 `json:"reminderMinutesBeforeStart"`
	OnlineMeeting         *struct {
		JoinURL string `json:"joinUrl"`
	} `json:"onlineMeeting"`
	HasAttachments       bool        `json:"hasAttachments"`
	CreatedDateTime      string      `json:"createdDateTime"`
	LastModifiedDateTime string      `json:"lastModifiedDateTime"`
	Recurrence           *Recurrence `json:"recurrence"`
}

// Events returns a calendar's single events and series masters, with plain
// text bodies. Series exceptions come from Series.
func (c *Client) Events(ctx context.Context, calendarID string) ([]Event, error) {
	var events []Event

	target := "/calendars/" + url.PathEscape(calendarID) + "/events?$top=100"
	if err := list(ctx, c, target, &events, preferText); err != nil {
		return nil, fmt.Errorf("list events of calendar %s: %w", calendarID, err)
	}

	return events, nil
}

// Series returns a series master's changed occurrences (exceptions) and the
// occurrence IDs of its canceled ones ("OID.<master id>.<date>").
func (c *Client) Series(ctx context.Context, masterID string) (exceptions []Event, canceled []string, err error) {
	var master struct {
		ExceptionOccurrences []Event  `json:"exceptionOccurrences"`
		CancelledOccurrences []string `json:"cancelledOccurrences"`
	}

	// No $select: with one, Graph returns expanded exceptions with only their
	// IDs (verified live). The master's default properties include
	// cancelledOccurrences.
	target := "/events/" + url.PathEscape(masterID) + "?$expand=exceptionOccurrences"
	if err := c.getJSON(ctx, target, &master, preferText); err != nil {
		return nil, nil, fmt.Errorf("get series %s: %w", masterID, err)
	}

	return master.ExceptionOccurrences, master.CancelledOccurrences, nil
}

// Attachment is a file attached to an event. Other attachment kinds (items,
// links) come back without content.
type Attachment struct {
	ODataType    string `json:"@odata.type"` //nolint:tagliatelle // OData annotation name.
	Name         string `json:"name"`
	ContentType  string `json:"contentType"`
	ContentBytes string `json:"contentBytes"`
}

// Attachments returns an event's attachments.
func (c *Client) Attachments(ctx context.Context, eventID string) ([]Attachment, error) {
	var attachments []Attachment

	if err := list(ctx, c, "/events/"+url.PathEscape(eventID)+"/attachments", &attachments); err != nil {
		return nil, fmt.Errorf("list attachments of event %s: %w", eventID, err)
	}

	return attachments, nil
}

// list follows a collection's pages into out.
func list[T any](ctx context.Context, c *Client, target string, out *[]T, prefer ...string) error {
	for target != "" {
		var page struct {
			Value    []T    `json:"value"`
			NextLink string `json:"@odata.nextLink"` //nolint:tagliatelle // OData annotation name.
		}

		if err := c.getJSON(ctx, target, &page, prefer...); err != nil {
			return err
		}

		*out = append(*out, page.Value...)
		target = page.NextLink
	}

	return nil
}
