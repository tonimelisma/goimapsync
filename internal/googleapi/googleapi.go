// Package googleapi holds what pimvault's Google REST clients share
// (docs/requirements.md D140): GET with JSON decoding, Google's error format
// and its rate-limit errors.
package googleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// errorBodyLimit bounds how much of an error response is read.
const errorBodyLimit = 64 << 10

// ErrNotFound means the requested item no longer exists (HTTP 404).
var ErrNotFound = errors.New("not found")

// APIError is an error response from a Google API. Reason is the machine
// readable reason, when Google gives one.
type APIError struct {
	Status  int
	Message string
	Reason  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("google api: http %d: %s", e.Status, e.Message)
}

// GetJSON sends a GET request to target and decodes the JSON response into
// out. A 204 leaves out unchanged: Google answers that way when a field
// mask (the fields parameter) selects nothing, such as an empty list. A 404
// returns ErrNotFound; other failures return an *APIError.
func GetJSON(ctx context.Context, client *http.Client, target string, out any) error {
	return Do(ctx, client, http.MethodGet, target, "", nil, out)
}

// Do sends a request with an optional body of the given content type and
// decodes a JSON response into out, unless out is nil. Responses are read
// as GetJSON reads them; any 2xx is success.
func Do(ctx context.Context, client *http.Client, method, target, contentType string, body []byte, out any) error {
	var reader io.Reader = http.NoBody
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close error changes nothing.

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return apiError(resp)
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// errorResponse is Google's JSON error format: older APIs list reasons in
// errors, newer ones in details (google.rpc.ErrorInfo).
type errorResponse struct {
	Error struct {
		Message string `json:"message"`
		Errors  []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

func (e *errorResponse) reason() string {
	for _, d := range e.Error.Details {
		if d.Reason != "" {
			return d.Reason
		}
	}

	for _, d := range e.Error.Errors {
		if d.Reason != "" {
			return d.Reason
		}
	}

	return ""
}

func apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit)) //nolint:errcheck // a partial body still names the error.

	var parsed errorResponse
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
		return &APIError{Status: resp.StatusCode, Message: parsed.Error.Message, Reason: parsed.reason()}
	}

	return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}

// RateLimited reports whether a response is Google's rate-limit error sent
// with status 403, which is retried like a 429. The body is left readable.
func RateLimited(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden {
		return false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	resp.Body = readCloser{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}

	if err != nil {
		return false
	}

	var parsed errorResponse
	if json.Unmarshal(body, &parsed) != nil {
		return false
	}

	switch parsed.reason() {
	case "rateLimitExceeded", "userRateLimitExceeded", "RATE_LIMIT_EXCEEDED":
		return true
	default:
		return false
	}
}

// readCloser pairs a replacement reader with the original body's Close.
type readCloser struct {
	io.Reader
	io.Closer
}
