// Package graph reads Outlook.com through Microsoft Graph as plain REST
// (docs/requirements.md D47, D85). Items are addressed by immutable IDs, which
// stay the same when an item moves between folders.
package graph

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultBaseURL is the Graph root for the signed-in user.
const DefaultBaseURL = "https://graph.microsoft.com/v1.0/me"

// Every request asks for immutable IDs and large pages, as two Prefer
// headers.
const (
	preferImmutableID = `IdType="ImmutableId"`
	preferPageSize    = "odata.maxpagesize=500"
)

// errorBodyLimit bounds how much of an error response is read.
const errorBodyLimit = 64 << 10

// ErrNotFound means the item or folder no longer exists.
var ErrNotFound = errors.New("not found")

// ErrDeltaExpired means a stored delta link can no longer be used; the
// folder must be listed again from the start (D50).
var ErrDeltaExpired = errors.New("delta link expired")

// APIError is an error response from Graph.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("graph api: http %d: %s: %s", e.Status, e.Code, e.Message)
}

// Client calls Graph with an authorized HTTP client.
type Client struct {
	http *http.Client
	base string
}

// New returns a client for baseURL, normally DefaultBaseURL.
func New(httpClient *http.Client, baseURL string) *Client {
	return &Client{http: httpClient, base: strings.TrimSuffix(baseURL, "/")}
}

// getJSON fetches a path under the base URL, or an absolute link Graph
// returned, and decodes the JSON response into out. prefer adds Prefer
// headers to the standard ones.
func (c *Client) getJSON(ctx context.Context, target string, out any, prefer ...string) error {
	body, err := c.get(ctx, target, prefer...)
	if err != nil {
		return err
	}
	defer body.Close() //nolint:errcheck // read-only body; a close error changes nothing.

	if err := json.NewDecoder(body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// get sends a GET request and returns the body of a 200 response.
func (c *Client) get(ctx context.Context, target string, prefer ...string) (io.ReadCloser, error) {
	body, _, err := c.getWithType(ctx, target, prefer...)

	return body, err
}

// getWithType is get that also returns the response's content type.
func (c *Client) getWithType(ctx context.Context, target string, prefer ...string) (io.ReadCloser, string, error) {
	if !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "http://") {
		target = c.base + target
	}

	if err := c.sameHost(target); err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}

	req.Header.Add("Prefer", preferImmutableID)
	req.Header.Add("Prefer", preferPageSize)

	for _, p := range prefer {
		req.Header.Add("Prefer", p)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		return resp.Body, resp.Header.Get("Content-Type"), nil
	}

	defer resp.Body.Close() //nolint:errcheck // error body; a close error changes nothing.

	apiErr := apiError(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("%w: %w", ErrNotFound, apiErr)
	}

	return nil, "", apiErr
}

// send sends a request with a body (JSON unless contentType says
// otherwise) and decodes a JSON response into out, unless out is nil.
func (c *Client) send(ctx context.Context, method, target, contentType string, body []byte, out any) error {
	if !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "http://") {
		target = c.base + target
	}

	if err := c.sameHost(target); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", cmp.Or(contentType, "application/json"))
	req.Header.Add("Prefer", preferImmutableID)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close error changes nothing.

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := apiError(resp)
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: %w", ErrNotFound, apiErr)
		}

		return apiErr
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// postJSON posts v as JSON.
func (c *Client) postJSON(ctx context.Context, target string, v, out any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	return c.send(ctx, http.MethodPost, target, "", body, out)
}

// sameHost refuses links to other hosts, so the account's token is only
// ever sent to Graph.
func (c *Client) sameHost(target string) error {
	base, err := url.Parse(c.base)
	if err != nil {
		return fmt.Errorf("parse base url: %w", err)
	}

	link, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("parse link: %w", err)
	}

	if link.Scheme != base.Scheme || link.Host != base.Host {
		return fmt.Errorf("refuse link to %s: not the Graph host", link.Host)
	}

	return nil
}

func apiError(resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit)) //nolint:errcheck // a partial body still names the error.

	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Code != "" {
		return &APIError{Status: resp.StatusCode, Code: parsed.Error.Code, Message: parsed.Error.Message}
	}

	return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}
