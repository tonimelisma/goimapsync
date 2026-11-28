package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// rotatingSource hands out a new access token on every call, standing in for
// a token that expired and was refreshed while a retry waited.
type rotatingSource struct{ calls int }

func (s *rotatingSource) Token() (*oauth2.Token, error) {
	s.calls++

	return &oauth2.Token{AccessToken: fmt.Sprintf("t%d", s.calls), TokenType: "Bearer"}, nil
}

// throttleOnce answers the first request 429 and later ones 200, recording
// each request's Authorization header.
type throttleOnce struct{ auth []string }

func (t *throttleOnce) RoundTrip(req *http.Request) (*http.Response, error) {
	t.auth = append(t.auth, req.Header.Get("Authorization"))

	code := http.StatusOK
	if len(t.auth) == 1 {
		code = http.StatusTooManyRequests
	}

	return &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// A retry asks the token source again, so a token that expired during the
// wait is not sent: Gmail answers an expired token with 401, and the item
// failed (seen hourly on a long first backup, 2026-10-31).
// Validates: PV-235
func TestAuthorizedRetryGetsFreshToken(t *testing.T) {
	t.Parallel()

	base := &throttleOnce{}
	rt := authorized(&rotatingSource{}, base, nil, slog.New(slog.DiscardHandler))
	rt.Sleep = func(context.Context, time.Duration) error { return nil }

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/messages/1", http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"Bearer t1", "Bearer t2"}, base.auth)
	assert.Empty(t, req.Header.Get("Authorization"), "the caller's request is not modified")
}

// failingSource answers every call with the token endpoint's refusal.
type failingSource struct {
	code  int
	calls int
}

func (s *failingSource) Token() (*oauth2.Token, error) {
	s.calls++

	return nil, fmt.Errorf("refresh: %w", &oauth2.RetrieveError{
		Response:  &http.Response{StatusCode: s.code},
		ErrorCode: "invalid_grant",
	})
}

// A refused refresh is final: retrying it would cost every remaining item
// several token requests and backoffs before the run reports the sign-in.
// A token endpoint that fails or throttles is retried.
// Validates: PV-233
func TestAuthorizedRefusedRefresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		code      int
		wantCalls int
	}{
		{name: "refused", code: http.StatusBadRequest, wantCalls: 1},
		{name: "unauthorized client", code: http.StatusUnauthorized, wantCalls: 1},
		{name: "throttled", code: http.StatusTooManyRequests, wantCalls: 5},
		{name: "server error", code: http.StatusBadGateway, wantCalls: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := &failingSource{code: tt.code}
			base := &throttleOnce{}
			rt := authorized(source, base, nil, slog.New(slog.DiscardHandler))
			rt.Sleep = func(context.Context, time.Duration) error { return nil }

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/messages/1", http.NoBody)
			require.NoError(t, err)

			resp, err := rt.RoundTrip(req) //nolint:bodyclose // no response on error.
			require.Nil(t, resp)

			var retrieveErr *oauth2.RetrieveError
			require.ErrorAs(t, err, &retrieveErr)
			assert.Equal(t, tt.wantCalls, source.calls)
			assert.Empty(t, base.auth, "no request is sent without a token")
		})
	}
}
