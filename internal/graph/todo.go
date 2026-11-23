package graph

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Well-known To Do list names.
const (
	DefaultTaskList = "defaultList"
	// FlaggedEmailList is To Do's view of flagged mail, which pimvault
	// skips (D148).
	FlaggedEmailList = "flaggedEmails"
)

// TaskList is a Microsoft To Do list.
type TaskList struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	WellknownListName string `json:"wellknownListName"`
	IsOwner           bool   `json:"isOwner"`
}

// TaskLists returns the account's To Do lists.
func (c *Client) TaskLists(ctx context.Context) ([]TaskList, error) {
	var lists []TaskList

	if err := list(ctx, c, "/todo/lists", &lists); err != nil {
		return nil, fmt.Errorf("list task lists: %w", err)
	}

	return lists, nil
}

// ChecklistItem is a step inside a To Do task.
type ChecklistItem struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	IsChecked       bool   `json:"isChecked"`
	CreatedDateTime string `json:"createdDateTime"`
	CheckedDateTime string `json:"checkedDateTime"`
}

// Task is a Microsoft To Do task with its checklist, with the fields
// pimvault stores (D150).
type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  struct {
		Content string `json:"content"`
	} `json:"body"`
	Importance           string          `json:"importance"`
	Status               string          `json:"status"`
	Categories           []string        `json:"categories"`
	CreatedDateTime      string          `json:"createdDateTime"`
	LastModifiedDateTime string          `json:"lastModifiedDateTime"`
	StartDateTime        *DateTimeZone   `json:"startDateTime"`
	DueDateTime          *DateTimeZone   `json:"dueDateTime"`
	CompletedDateTime    *DateTimeZone   `json:"completedDateTime"`
	IsReminderOn         bool            `json:"isReminderOn"`
	ReminderDateTime     *DateTimeZone   `json:"reminderDateTime"`
	Recurrence           *Recurrence     `json:"recurrence"`
	ChecklistItems       []ChecklistItem `json:"checklistItems"`
	Removed              *struct{}       `json:"@removed"`
}

func (t *Task) itemID() string { return t.ID }
func (t *Task) removed() bool  { return t.Removed != nil }

// TaskDelta returns the changes to a To Do list since link, or all of its
// tasks when link is empty, each with its checklist. A checklist change
// reports its task (F47). It returns ErrDeltaExpired when link can no
// longer be used.
func (c *Client) TaskDelta(ctx context.Context, listID, link string) (Changes[Task], error) {
	target := link
	if target == "" {
		target = "/todo/lists/" + url.PathEscape(listID) + "/tasks/delta?$expand=checklistItems"
	}

	changes, err := delta[Task](ctx, c, target, preferText)
	if err != nil {
		return Changes[Task]{}, fmt.Errorf("list task changes: %w", err)
	}

	return changes, nil
}

// CreateTaskList creates a To Do list and returns its ID.
func (c *Client) CreateTaskList(ctx context.Context, name string) (string, error) {
	var created struct {
		ID string `json:"id"`
	}

	if err := c.postJSON(ctx, "/todo/lists", displayName{name}, &created); err != nil {
		return "", fmt.Errorf("create task list %q: %w", name, err)
	}

	return created.ID, nil
}

// CreateTask creates a task in a list and
// returns its ID.
func (c *Client) CreateTask(ctx context.Context, listID string, task *NewTask) (string, error) {
	var created struct {
		ID string `json:"id"`
	}

	if err := c.postJSON(ctx, "/todo/lists/"+url.PathEscape(listID)+"/tasks", task, &created); err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	return created.ID, nil
}

// DeleteTask deletes a task.
func (c *Client) DeleteTask(ctx context.Context, listID, taskID string) error {
	target := "/todo/lists/" + url.PathEscape(listID) + "/tasks/" + url.PathEscape(taskID)
	if err := c.send(ctx, http.MethodDelete, target, "", nil, nil); err != nil {
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}

	return nil
}

// AddChecklistItem adds a checklist item to a task.
func (c *Client) AddChecklistItem(ctx context.Context, listID, taskID, name string, checked bool) error {
	target := "/todo/lists/" + url.PathEscape(listID) + "/tasks/" + url.PathEscape(taskID) + "/checklistItems"
	if err := c.postJSON(ctx, target, struct {
		DisplayName string `json:"displayName"`
		IsChecked   bool   `json:"isChecked"`
	}{name, checked}, nil); err != nil {
		return fmt.Errorf("add checklist item to task %s: %w", taskID, err)
	}

	return nil
}

// ExistingTasks returns the tasks of a list with the archive IDs pimvault
// wrote them from.
func (c *Client) ExistingTasks(ctx context.Context, listID string) ([]Existing, error) {
	var tasks []struct {
		ID         string      `json:"id"`
		Extensions []sourceTag `json:"extensions"`
	}

	query := url.Values{paramExpand: {"extensions($filter=id eq '" + sourceExtension + "')"}}

	if err := list(ctx, c, "/todo/lists/"+url.PathEscape(listID)+"/tasks?"+query.Encode(), &tasks); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	out := make([]Existing, 0, len(tasks))

	for _, t := range tasks {
		e := Existing{ID: t.ID}
		for _, x := range t.Extensions {
			e.Source = x.Source
		}

		out = append(out, e)
	}

	return out, nil
}
