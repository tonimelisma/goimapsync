// Package gtasks reads Google Tasks through its REST API (docs/requirements.md
// D23, D140) and renders tasks as iCalendar VTODOs (D25, D150).
package gtasks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// DefaultBaseURL is the Tasks API root.
const DefaultBaseURL = "https://tasks.googleapis.com/tasks/v1"

// Query parameters: the largest page both list calls allow.
const (
	paramMaxResults = "maxResults"
	pageSize        = "100"
	valueTrue       = "true"
)

// Client calls the Tasks API with an authorized HTTP client.
type Client struct {
	http *http.Client
	base string
}

// New returns a client for baseURL, normally DefaultBaseURL.
func New(httpClient *http.Client, baseURL string) *Client {
	return &Client{http: httpClient, base: strings.TrimSuffix(baseURL, "/")}
}

// List is a task list.
type List struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Lists returns the account's task lists and the ID of the default one.
func (c *Client) Lists(ctx context.Context) (lists []List, defaultID string, err error) {
	var def List
	if err := googleapi.GetJSON(ctx, c.http, c.base+"/users/@me/lists/@default", &def); err != nil {
		return nil, "", fmt.Errorf("get default task list: %w", err)
	}

	query := url.Values{paramMaxResults: {pageSize}}

	for {
		var page struct {
			Items         []List `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}

		if err := googleapi.GetJSON(ctx, c.http, c.base+"/users/@me/lists?"+query.Encode(), &page); err != nil {
			return nil, "", fmt.Errorf("list task lists: %w", err)
		}

		lists = append(lists, page.Items...)

		if page.NextPageToken == "" {
			return lists, def.ID, nil
		}

		query.Set("pageToken", page.NextPageToken)
	}
}

// Task is one Google task; a subtask names its parent.
type Task struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Notes     string `json:"notes"`
	Status    string `json:"status"`
	Due       string `json:"due"`
	Completed string `json:"completed"`
	Updated   string `json:"updated"`
	Parent    string `json:"parent"`
}

// Tasks returns every task of a list, completed ones included (D27). Tasks
// completed in Google's own apps are hidden and need showHidden.
func (c *Client) Tasks(ctx context.Context, listID string) ([]Task, error) {
	query := url.Values{
		paramMaxResults: {pageSize},
		"showCompleted": {valueTrue},
		"showHidden":    {valueTrue},
		"showAssigned":  {valueTrue},
	}

	var tasks []Task

	for {
		var page struct {
			Items         []Task `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}

		target := c.base + "/lists/" + url.PathEscape(listID) + "/tasks?" + query.Encode()
		if err := googleapi.GetJSON(ctx, c.http, target, &page); err != nil {
			return nil, fmt.Errorf("list tasks of list %s: %w", listID, err)
		}

		tasks = append(tasks, page.Items...)

		if page.NextPageToken == "" {
			return tasks, nil
		}

		query.Set("pageToken", page.NextPageToken)
	}
}

// NewTask is a task to create. Google keeps only the date of Due.
type NewTask struct {
	Title     string `json:"title"`
	Notes     string `json:"notes,omitempty"`
	Status    string `json:"status,omitempty"`
	Due       string `json:"due,omitempty"`
	Completed string `json:"completed,omitempty"`
}

// CreateList creates a task list and returns its ID.
func (c *Client) CreateList(ctx context.Context, title string) (string, error) {
	var created List

	if err := c.post(ctx, c.base+"/users/@me/lists", map[string]string{"title": title}, &created); err != nil {
		return "", fmt.Errorf("create task list %q: %w", title, err)
	}

	return created.ID, nil
}

// CreateTask creates a task in a list, as a subtask of parent unless parent
// is empty, and returns its ID. It goes right after the sibling previous,
// or first among its siblings when previous is empty.
func (c *Client) CreateTask(ctx context.Context, listID, parent, previous string, task *NewTask) (string, error) {
	target := c.base + "/lists/" + url.PathEscape(listID) + "/tasks"

	query := url.Values{}
	if parent != "" {
		query.Set("parent", parent)
	}

	if previous != "" {
		query.Set("previous", previous)
	}

	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var created Task

	if err := c.post(ctx, target, task, &created); err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	return created.ID, nil
}

// DeleteTask deletes a task and its subtasks.
func (c *Client) DeleteTask(ctx context.Context, listID, taskID string) error {
	target := c.base + "/lists/" + url.PathEscape(listID) + "/tasks/" + url.PathEscape(taskID)
	if err := googleapi.Do(ctx, c.http, http.MethodDelete, target, "", nil, nil); err != nil {
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}

	return nil
}

func (c *Client) post(ctx context.Context, target string, v, out any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	return googleapi.Do(ctx, c.http, http.MethodPost, target, "application/json", body, out)
}
