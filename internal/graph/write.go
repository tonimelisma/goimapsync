package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// displayName is the body that creates a named folder or list.
type displayName struct {
	DisplayName string `json:"displayName"`
}

// Where pimvault keeps the archive ID of an item it wrote (D160): a custom
// extended property on contacts, an open extension on To Do tasks. Both are
// invisible in Outlook.
const (
	sourceProperty  = "String {7a5d4f0e-2c1b-4a6e-9f3d-5b8c1e2a7d90} Name pimvaultSource"
	sourceExtension = "net.pimvault"
	paramExpand     = "$expand"
	paramSelect     = "$select"
	paramTop        = "$top"
)

// extendedProperty is a single-value extended property.
type extendedProperty struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// sourceTag is the open extension that carries a task's archive ID.
type sourceTag struct {
	ODataType string `json:"@odata.type"` //nolint:tagliatelle // OData annotation name.
	Name      string `json:"extensionName"`
	Source    string `json:"source"`
}

// ItemBody is the text body of a task.
type ItemBody struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
}

// TextBody is a plain text body.
func TextBody(content string) *ItemBody {
	return &ItemBody{Content: content, ContentType: "text"}
}

// NewTask is a To Do task to create.
type NewTask struct {
	Title             string        `json:"title"`
	Body              *ItemBody     `json:"body,omitempty"`
	Status            string        `json:"status,omitempty"`
	Importance        string        `json:"importance,omitempty"`
	Categories        []string      `json:"categories,omitempty"`
	DueDateTime       *DateTimeZone `json:"dueDateTime,omitempty"`
	CompletedDateTime *DateTimeZone `json:"completedDateTime,omitempty"`
	IsReminderOn      bool          `json:"isReminderOn,omitempty"`
	ReminderDateTime  *DateTimeZone `json:"reminderDateTime,omitempty"`
	Recurrence        *Recurrence   `json:"recurrence,omitempty"`
	Extensions        []sourceTag   `json:"extensions,omitempty"`
}

// SetSource records the archive ID the task was written from (D160).
func (t *NewTask) SetSource(id string) {
	t.Extensions = []sourceTag{{ODataType: "microsoft.graph.openTypeExtension", Name: sourceExtension, Source: id}}
}

// NewContact is a contact to create.
type NewContact struct {
	DisplayName      string             `json:"displayName,omitempty"`
	GivenName        string             `json:"givenName,omitempty"`
	MiddleName       string             `json:"middleName,omitempty"`
	Surname          string             `json:"surname,omitempty"`
	Title            string             `json:"title,omitempty"`
	Generation       string             `json:"generation,omitempty"`
	NickName         string             `json:"nickName,omitempty"`
	EmailAddresses   []EmailAddress     `json:"emailAddresses,omitempty"`
	IMAddresses      []string           `json:"imAddresses,omitempty"`
	BusinessPhones   []string           `json:"businessPhones,omitempty"`
	HomePhones       []string           `json:"homePhones,omitempty"`
	MobilePhone      string             `json:"mobilePhone,omitempty"`
	HomeAddress      *PhysicalAddress   `json:"homeAddress,omitempty"`
	BusinessAddress  *PhysicalAddress   `json:"businessAddress,omitempty"`
	OtherAddress     *PhysicalAddress   `json:"otherAddress,omitempty"`
	CompanyName      string             `json:"companyName,omitempty"`
	Department       string             `json:"department,omitempty"`
	JobTitle         string             `json:"jobTitle,omitempty"`
	BusinessHomePage string             `json:"businessHomePage,omitempty"`
	Birthday         string             `json:"birthday,omitempty"`
	PersonalNotes    string             `json:"personalNotes,omitempty"`
	SpouseName       string             `json:"spouseName,omitempty"`
	Children         []string           `json:"children,omitempty"`
	Categories       []string           `json:"categories,omitempty"`
	Extended         []extendedProperty `json:"singleValueExtendedProperties,omitempty"`
}

// SetSource records the archive ID the contact was written from (D160).
func (c *NewContact) SetSource(id string) {
	c.Extended = []extendedProperty{{ID: sourceProperty, Value: id}}
}

// Existing is an item already in the account: its ID, the archive ID it was
// written from by pimvault (D160) or "", for contacts its email addresses
// and Fingerprint, and for events its iCalendar UID.
type Existing struct {
	ID          string
	Source      string
	Emails      []string
	Fingerprint string
	UID         string
}

// Fingerprint identifies a contact by the fields pimvault writes, or is ""
// for a contact with none of them. Outlook.com removes a new contact
// identical in these to one it has, whatever its archive ID (F67), so the
// archive ID is left out, as are differences in form Outlook introduces: a
// birthday's time, an email name repeating the address, an empty address,
// line endings.
func (c *NewContact) Fingerprint() string {
	n := *c
	n.Extended = nil

	if len(n.Birthday) > len("2006-01-02") {
		n.Birthday = n.Birthday[:len("2006-01-02")]
	}

	n.PersonalNotes = strings.ReplaceAll(n.PersonalNotes, "\r\n", "\n")

	n.EmailAddresses = slices.Clone(n.EmailAddresses)
	for i, a := range n.EmailAddresses {
		if strings.EqualFold(a.Name, a.Address) {
			n.EmailAddresses[i].Name = ""
		}
	}

	for _, a := range []**PhysicalAddress{&n.HomeAddress, &n.BusinessAddress, &n.OtherAddress} {
		if *a != nil && **a == (PhysicalAddress{}) {
			*a = nil
		}
	}

	b, _ := json.Marshal(n) //nolint:errcheck,errchkjson // strings, slices and structs of them always encode.
	if string(b) == "{}" {
		return ""
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// NewAttendee is a guest of an event to create.
type NewAttendee struct {
	EmailAddress EmailAddress `json:"emailAddress"`
	Type         string       `json:"type"`
}

// Location is an event's place.
type Location struct {
	DisplayName string `json:"displayName"`
}

// NewEvent is an event to create, or the changes to an occurrence.
// Reminders are off unless set: IsReminderOn is always sent.
type NewEvent struct {
	Subject         string             `json:"subject,omitempty"`
	Body            *ItemBody          `json:"body,omitempty"`
	Start           *DateTimeZone      `json:"start,omitempty"`
	End             *DateTimeZone      `json:"end,omitempty"`
	IsAllDay        bool               `json:"isAllDay,omitempty"`
	Location        *Location          `json:"location,omitempty"`
	Attendees       []NewAttendee      `json:"attendees,omitempty"`
	IsDraft         bool               `json:"isDraft,omitempty"`
	Recurrence      *Recurrence        `json:"recurrence,omitempty"`
	ShowAs          string             `json:"showAs,omitempty"`
	Sensitivity     string             `json:"sensitivity,omitempty"`
	Categories      []string           `json:"categories,omitempty"`
	IsReminderOn    bool               `json:"isReminderOn"`
	ReminderMinutes int                `json:"reminderMinutesBeforeStart,omitempty"`
	Extended        []extendedProperty `json:"singleValueExtendedProperties,omitempty"`
}

// SetSource records the archive UID the event was written from (D160).
func (e *NewEvent) SetSource(uid string) {
	e.Extended = []extendedProperty{{ID: sourceProperty, Value: uid}}
}
