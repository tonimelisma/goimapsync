// Package httpretry retries HTTP requests on throttling and transient server
// or network failures, honoring the provider's Retry-After (D69).
//
// Adapted from github.com/tonimelisma/onedrive-go (internal/retry): the
// transport loop, backoff policy and Retry-After handling, without the perf
// hooks (D111), named policies and shared throttle gate.
package httpretry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// Default backoff: 5 attempts in total (10 when throttled), 1s doubling to at
// most 60s, ±25% jitter. Ten throttled attempts wait about four minutes in
// total, longer than a per-minute quota window.
const (
	defaultAttempts   = 5
	throttledAttempts = 10
	baseDelay         = time.Second
	maxDelay          = 60 * time.Second
	// maxRetryAfter bounds a server's Retry-After: a longer wait is cut,
	// and the retry that follows uses up an attempt like any other.
	maxRetryAfter = 5 * time.Minute
	// longWait is when a wait is worth telling the user about.
	longWait   = 30 * time.Second
	multiplier = 2.0
	jitter     = 0.25
)

// SleepFunc waits for d or until ctx ends. Tests replace it.
type SleepFunc func(ctx context.Context, d time.Duration) error

// Transport is an http.RoundTripper that retries idempotent requests on
// network errors, 408, 429 and 5xx responses, and any request on 429 and
// on any response Retryable accepts. It waits for Retry-After when the response has one and backs off
// exponentially otherwise.
type Transport struct {
	// Base sends the requests; nil means http.DefaultTransport.
	Base http.RoundTripper
	// Attempts is the total number of tries; 0 means 5, or 10 for throttled
	// responses (429 and those Retryable accepts).
	Attempts int
	// Retryable marks extra responses as retryable, such as a provider's
	// rate-limit error sent with 403. It may read the body but must leave a
	// readable copy in place. Nil means none.
	Retryable func(*http.Response) bool
	// Final marks errors that another attempt cannot fix, such as a refused
	// token refresh from an authorizing Base. Nil means none: every error
	// from an idempotent request is retried as a network failure.
	Final func(error) bool
	// Sleep waits between attempts; nil means a timer.
	Sleep SleepFunc
	// Logger reports retries; nil means slog.Default().
	Logger *slog.Logger
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		send := req
		if attempt > 1 {
			var err error
			if send, err = replay(req); err != nil {
				return nil, err
			}
		}

		resp, err := t.base().RoundTrip(send)

		retry, wait := t.decide(req, resp, err, attempt)
		if !retry {
			return resp, err //nolint:wrapcheck // a RoundTripper returns the transport's error as is.
		}

		if resp != nil {
			if drainErr := drainAndClose(resp.Body); drainErr != nil {
				t.logger().Debug("discard response body", slog.String("error", drainErr.Error()))
			}
		}

		level := slog.LevelDebug
		if wait >= longWait {
			level = slog.LevelWarn
		}

		t.logger().Log(req.Context(), level, "retrying request",
			slog.String("method", req.Method),
			slog.String("url", req.URL.Redacted()),
			slog.Int("attempt", attempt),
			slog.Duration("wait", wait),
			slog.String("reason", reason(resp, err)))

		if sleepErr := t.sleep(req.Context(), wait); sleepErr != nil {
			return nil, fmt.Errorf("wait to retry request: %w", sleepErr)
		}
	}
}

// decide reports whether to retry and how long to wait first.
func (t *Transport) decide(req *http.Request, resp *http.Response, err error, attempt int) (bool, time.Duration) {
	if req.Context().Err() != nil {
		return false, 0
	}

	throttled := err == nil && (resp.StatusCode == http.StatusTooManyRequests || (t.Retryable != nil && t.Retryable(resp)))

	// A throttled request was not carried out, so it is sent again whatever
	// its method; other failures only for idempotent ones.
	if !idempotent(req.Method) && !throttled {
		return false, 0
	}

	if err != nil {
		if t.Final != nil && t.Final(err) {
			return false, 0
		}

		return attempt < t.attempts(defaultAttempts), backoff(attempt)
	}

	if !throttled && !retryableStatus(resp.StatusCode) {
		return false, 0
	}

	limit := defaultAttempts
	if throttled {
		limit = throttledAttempts
	}

	if attempt >= t.attempts(limit) {
		return false, 0
	}

	if wait, ok := retryAfter(resp, time.Now()); ok {
		return true, min(wait, maxRetryAfter)
	}

	return true, backoff(attempt)
}

func (t *Transport) attempts(fallback int) int {
	if t.Attempts > 0 {
		return t.Attempts
	}

	return fallback
}

func (t *Transport) base() http.RoundTripper {
	if t.Base == nil {
		return http.DefaultTransport
	}

	return t.Base
}

func (t *Transport) logger() *slog.Logger {
	if t.Logger == nil {
		return slog.Default()
	}

	return t.Logger
}

func (t *Transport) sleep(ctx context.Context, d time.Duration) error {
	if t.Sleep != nil {
		return t.Sleep(ctx, d)
	}

	return Timer(ctx, d)
}

// Timer is the SleepFunc that waits on a timer.
func Timer(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("sleep: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// backoff returns the jittered exponential delay before retry number attempt
// (1-based).
func backoff(attempt int) time.Duration {
	delay := math.Min(float64(baseDelay)*math.Pow(multiplier, float64(attempt-1)), float64(maxDelay))
	delay += delay * jitter * (rand.Float64()*2 - 1) //nolint:gosec // jitter does not need crypto rand.

	return time.Duration(delay)
}

// retryAfter reads a Retry-After header given in seconds or as an HTTP date.
func retryAfter(resp *http.Response, now time.Time) (time.Duration, bool) {
	value := resp.Header.Get("Retry-After")
	if value == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}

		return time.Duration(seconds) * time.Second, true
	}

	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(now), 0), true
	}

	return 0, false
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// idempotent reports whether a request can be sent twice without a second
// side effect. POST is excluded: a retried insert could create a duplicate.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func reason(resp *http.Response, err error) string {
	if err != nil {
		return err.Error()
	}

	return resp.Status
}

// replay copies req with a fresh body for another attempt; a RoundTripper
// must not modify the caller's request.
func replay(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return clone, nil
	}

	if req.GetBody == nil {
		return nil, errors.New("retry request: body cannot be replayed")
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("retry request: replay body: %w", err)
	}

	clone.Body = body

	return clone, nil
}

func drainAndClose(body io.ReadCloser) error {
	_, drainErr := io.Copy(io.Discard, body)
	closeErr := body.Close()

	return errors.Join(drainErr, closeErr)
}
