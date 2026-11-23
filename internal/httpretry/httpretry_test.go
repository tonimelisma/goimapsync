package httpretry_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/httpretry"
)

// step is one prepared answer: a status with an optional Retry-After, or a
// network error when code is netErr.
type step struct {
	code       int
	retryAfter string
}

const netErr = 0

// script is a RoundTripper that answers with prepared steps in order and
// records the requests and bodies it saw.
type script struct {
	steps  []step
	bodies []string
	reqs   []*http.Request
}

func (s *script) RoundTrip(req *http.Request) (*http.Response, error) {
	s.reqs = append(s.reqs, req)

	if req.Body != nil {
		data, _ := io.ReadAll(req.Body) //nolint:errcheck // test transport.
		s.bodies = append(s.bodies, string(data))
	}

	st := s.steps[len(s.reqs)-1]
	if st.code == netErr {
		return nil, errors.New("connection reset")
	}

	resp := &http.Response{
		StatusCode: st.code,
		Status:     http.StatusText(st.code),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("body")),
	}
	if st.retryAfter != "" {
		resp.Header.Set("Retry-After", st.retryAfter)
	}

	return resp, nil
}

func steps(codes ...any) *script {
	s := &script{}

	for _, c := range codes {
		switch v := c.(type) {
		case int:
			s.steps = append(s.steps, step{code: v})
		case step:
			s.steps = append(s.steps, v)
		}
	}

	return s
}

type sleeps struct{ waits []time.Duration }

func (s *sleeps) sleep(_ context.Context, d time.Duration) error {
	s.waits = append(s.waits, d)

	return nil
}

func newTransport(base http.RoundTripper, sl *sleeps) *httpretry.Transport {
	return &httpretry.Transport{Base: base, Sleep: sl.sleep}
}

// get sends a GET and returns the final status code.
func get(t *testing.T, rt http.RoundTripper) (int, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/x", http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	if err != nil {
		return 0, err //nolint:wrapcheck // test helper.
	}

	require.NoError(t, resp.Body.Close())

	return resp.StatusCode, nil
}

func TestRetryAfterSecondsIsHonored(t *testing.T) {
	base := steps(step{code: 429, retryAfter: "7"}, 200)
	sl := &sleeps{}

	code, err := get(t, newTransport(base, sl))
	require.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, []time.Duration{7 * time.Second}, sl.waits)
}

func TestRetryAfterDateIsHonored(t *testing.T) {
	at := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	base := steps(step{code: 503, retryAfter: at}, 200)
	sl := &sleeps{}

	_, err := get(t, newTransport(base, sl))
	require.NoError(t, err)
	require.Len(t, sl.waits, 1)
	assert.InDelta(t, 30*time.Second, sl.waits[0], float64(2*time.Second))
}

func TestBackoffWithoutRetryAfter(t *testing.T) {
	base := steps(500, netErr, 502, 504, 200)
	sl := &sleeps{}

	code, err := get(t, newTransport(base, sl))
	require.NoError(t, err)
	assert.Equal(t, 200, code)
	require.Len(t, sl.waits, 4)

	for i, w := range sl.waits {
		nominal := time.Second << i
		assert.InDelta(t, nominal, w, float64(nominal)/4+1, "attempt %d", i+1)
	}
}

func TestGivesUpAfterAttempts(t *testing.T) {
	base := steps(503, 503, 503)
	sl := &sleeps{}
	rt := newTransport(base, sl)
	rt.Attempts = 3

	code, err := get(t, rt)
	require.NoError(t, err)
	assert.Equal(t, 503, code)
	assert.Len(t, base.reqs, 3)
	assert.Len(t, sl.waits, 2)
}

func TestLastNetworkErrorIsReturned(t *testing.T) {
	base := steps(netErr, netErr)
	rt := newTransport(base, &sleeps{})
	rt.Attempts = 2

	_, err := get(t, rt)
	require.EqualError(t, err, "connection reset")
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	base := steps(404)

	code, err := get(t, newTransport(base, &sleeps{}))
	require.NoError(t, err)
	assert.Equal(t, 404, code)
	assert.Len(t, base.reqs, 1)
}

func TestRetryableHook(t *testing.T) {
	base := steps(403, 200)
	rt := newTransport(base, &sleeps{})
	rt.Retryable = func(resp *http.Response) bool { return resp.StatusCode == 403 }

	code, err := get(t, rt)
	require.NoError(t, err)
	assert.Equal(t, 200, code)
}

func TestPostIsNotRetried(t *testing.T) {
	base := steps(503)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/x", strings.NewReader("data"))
	require.NoError(t, err)

	resp, err := newTransport(base, &sleeps{}).RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, 503, resp.StatusCode)
	assert.Len(t, base.reqs, 1)
}

func TestThrottledPostIsRetried(t *testing.T) {
	base := steps(429, 201)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/x", strings.NewReader("data"))
	require.NoError(t, err)

	resp, err := newTransport(base, &sleeps{}).RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, []string{"data", "data"}, base.bodies)
}

func TestBodyIsReplayedWithoutChangingTheRequest(t *testing.T) {
	base := steps(500, 200)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://example.test/x", strings.NewReader("data"))
	require.NoError(t, err)

	originalBody := req.Body

	resp, err := newTransport(base, &sleeps{}).RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, []string{"data", "data"}, base.bodies)
	assert.Same(t, req, base.reqs[0])
	assert.NotSame(t, req, base.reqs[1])
	assert.Equal(t, originalBody, req.Body, "caller's body is untouched")
}

func TestCanceledWaitStops(t *testing.T) {
	base := steps(503)
	rt := &httpretry.Transport{Base: base, Sleep: func(context.Context, time.Duration) error { return context.Canceled }}

	_, err := get(t, rt)
	require.ErrorIs(t, err, context.Canceled)
}

func TestThrottledRequestsGetMoreAttempts(t *testing.T) {
	codes := make([]any, 0, 10)
	for range 9 {
		codes = append(codes, 429)
	}

	base := steps(append(codes, 200)...)
	sl := &sleeps{}

	code, err := get(t, newTransport(base, sl))
	require.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, sl.waits, 9)
}

func TestServerErrorsStopAfterFiveAttempts(t *testing.T) {
	base := steps(500, 500, 500, 500, 500, 200)

	code, err := get(t, newTransport(base, &sleeps{}))
	require.NoError(t, err)
	assert.Equal(t, 500, code)
	assert.Len(t, base.reqs, 5)
}

func TestFinalErrorIsNotRetried(t *testing.T) {
	base := steps(netErr, 200)
	sl := &sleeps{}
	rt := newTransport(base, sl)
	rt.Final = func(error) bool { return true }

	_, err := get(t, rt)
	require.Error(t, err)
	assert.Len(t, base.reqs, 1)
	assert.Empty(t, sl.waits)
}

func TestNonFinalErrorIsRetried(t *testing.T) {
	base := steps(netErr, 200)
	sl := &sleeps{}
	rt := newTransport(base, sl)
	rt.Final = func(error) bool { return false }

	code, err := get(t, rt)
	require.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, base.reqs, 2)
}
