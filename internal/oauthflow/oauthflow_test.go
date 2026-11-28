package oauthflow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// fakeProvider is a minimal OAuth server: a token endpoint that accepts
// authorization codes and refresh tokens.
type fakeProvider struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	tokenReq url.Values
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()

	p := &fakeProvider{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, r.ParseForm())
		p.mu.Lock()
		p.tokenReq = r.PostForm
		p.mu.Unlock()
		writeJSON(t, w, map[string]any{
			"access_token":  "access-" + r.PostForm.Get("grant_type"),
			"refresh_token": "refresh-new",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)

	return p
}

func (p *fakeProvider) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:    "client",
		Scopes:      []string{"scope-a", "scope-b"},
		RedirectURL: "http://127.0.0.1:8765",
		Endpoint: oauth2.Endpoint{
			AuthURL:   p.server.URL + "/auth",
			TokenURL:  p.server.URL + "/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

func (p *fakeProvider) lastTokenRequest() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.tokenReq
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(body))
}

// pasteRun runs PasteURL and builds the pasted URL from the sign-in URL the
// flow printed, so tests can control what comes back.
func pasteRun(t *testing.T, p *fakeProvider, redirect func(authURL *url.URL) string) (*oauth2.Token, *url.URL, error) {
	t.Helper()

	var authURL *url.URL

	tok, err := PasteURL(context.Background(), p.config(),
		func(raw string) {
			parsed, parseErr := url.Parse(raw)
			require.NoError(t, parseErr)
			authURL = parsed
		},
		func() (string, error) { return redirect(authURL), nil })

	return tok, authURL, err
}

func TestPasteURL_ExchangesCodeWithPKCE(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)
	tok, authURL, err := pasteRun(t, p, func(u *url.URL) string {
		return "  http://127.0.0.1:8765/?code=the-code&state=" + u.Query().Get("state") + "\n"
	})
	require.NoError(t, err)
	assert.Equal(t, "access-authorization_code", tok.AccessToken)

	q := authURL.Query()
	assert.Equal(t, "client", q.Get("client_id"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.NotEmpty(t, q.Get("code_challenge"))
	assert.Equal(t, "offline", q.Get("access_type"))
	assert.Equal(t, "consent", q.Get("prompt"))
	assert.Equal(t, "http://127.0.0.1:8765", q.Get("redirect_uri"))

	sent := p.lastTokenRequest()
	assert.Equal(t, "the-code", sent.Get("code"))
	assert.NotEmpty(t, sent.Get("code_verifier"))
}

func TestPasteURL_PassesExchangeOptions(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)

	var state string

	_, err := PasteURL(context.Background(), p.config(),
		func(raw string) {
			u, parseErr := url.Parse(raw)
			require.NoError(t, parseErr)
			state = u.Query().Get("state")
		},
		func() (string, error) { return "http://127.0.0.1:8765/?code=c&state=" + state, nil },
		oauth2.SetAuthURLParam("scope", "scope-a"))
	require.NoError(t, err)
	assert.Equal(t, "scope-a", p.lastTokenRequest().Get("scope"))
}

func TestPasteURL_RejectsBadRedirects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		redirect func(u *url.URL) string
		wantErr  string
	}{
		{
			name:     "state mismatch",
			redirect: func(*url.URL) string { return "http://127.0.0.1:8765/?code=c&state=other" },
			wantErr:  "state does not match",
		},
		{
			name: "provider error",
			redirect: func(u *url.URL) string {
				return "http://127.0.0.1:8765/?error=access_denied&error_description=User+said+no&state=" + u.Query().Get("state")
			},
			wantErr: "provider returned access_denied: User said no",
		},
		{
			name:     "missing code",
			redirect: func(u *url.URL) string { return "http://127.0.0.1:8765/?state=" + u.Query().Get("state") },
			wantErr:  "no authorization code",
		},
		{
			name:     "not a URL",
			redirect: func(*url.URL) string { return "http://[::1" },
			wantErr:  "parse redirected URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := newFakeProvider(t)
			tok, _, err := pasteRun(t, p, tt.redirect)
			assert.Nil(t, tok)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, p.lastTokenRequest(), "no token request may be made")
		})
	}
}

func TestPasteURL_ReadFailure(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)
	_, err := PasteURL(context.Background(), p.config(), func(string) {},
		func() (string, error) { return "", io.ErrUnexpectedEOF })
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestTokenSource_SavesRefreshedToken(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh-old", Expiry: time.Now().Add(-time.Hour)}

	var saved []*oauth2.Token

	src := TokenSource(context.Background(), p.config(), expired, func(tok *oauth2.Token) error {
		saved = append(saved, tok)

		return nil
	}, slog.New(slog.DiscardHandler))

	tok, err := src.Token()
	require.NoError(t, err)
	assert.Equal(t, "access-refresh_token", tok.AccessToken)
	require.Len(t, saved, 1)
	assert.Equal(t, "refresh-new", saved[0].RefreshToken)
	assert.Equal(t, "refresh-old", p.lastTokenRequest().Get("refresh_token"))

	// A still-valid token is not refreshed again.
	_, err = src.Token()
	require.NoError(t, err)
	assert.Len(t, saved, 1)
}

func TestTokenSource_ValidTokenIsNotSaved(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)
	valid := &oauth2.Token{AccessToken: "still-good", RefreshToken: "refresh-old", Expiry: time.Now().Add(time.Hour)}

	saves := 0
	src := TokenSource(context.Background(), p.config(), valid, func(*oauth2.Token) error {
		saves++

		return nil
	}, slog.New(slog.DiscardHandler))

	for range 3 {
		tok, err := src.Token()
		require.NoError(t, err)
		assert.Equal(t, "still-good", tok.AccessToken)
	}

	assert.Zero(t, saves, "a token that was not refreshed is already saved")
	assert.Nil(t, p.lastTokenRequest(), "nothing was refreshed")
}

// Requests of one account share its token source: a refresh several of them
// wait for is saved once, and an older token is never saved after a newer
// one.
func TestTokenSource_ConcurrentUseSavesEachRefreshOnce(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh-old", Expiry: time.Now().Add(-time.Hour)}

	var (
		mu    sync.Mutex
		saved []*oauth2.Token
	)

	src := TokenSource(context.Background(), p.config(), expired, func(tok *oauth2.Token) error {
		mu.Lock()
		defer mu.Unlock()

		saved = append(saved, tok)

		return nil
	}, slog.New(slog.DiscardHandler))

	got := make(chan string, 8)

	var wg sync.WaitGroup
	for range cap(got) {
		wg.Go(func() {
			tok, err := src.Token()
			if err != nil {
				got <- err.Error()

				return
			}

			got <- tok.AccessToken
		})
	}
	wg.Wait()
	close(got)

	for access := range got {
		assert.Equal(t, "access-refresh_token", access)
	}

	require.Len(t, saved, 1)
	assert.Equal(t, "refresh-new", saved[0].RefreshToken)
}

func TestTokenSource_SaveFailureStillReturnsToken(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh-old", Expiry: time.Now().Add(-time.Hour)}

	src := TokenSource(context.Background(), p.config(), expired,
		func(*oauth2.Token) error { return errors.New("disk full") }, slog.New(slog.DiscardHandler))

	tok, err := src.Token()
	require.NoError(t, err)
	assert.Equal(t, "access-refresh_token", tok.AccessToken)
}

func TestRefresh_NamesTheScopes(t *testing.T) {
	t.Parallel()

	p := newFakeProvider(t)

	tok, err := Refresh(context.Background(), p.config(), "refresh-old")
	require.NoError(t, err)
	assert.Equal(t, "access-refresh_token", tok.AccessToken)
	assert.Equal(t, "refresh-new", tok.RefreshToken)
	assert.True(t, tok.Valid())

	req := p.lastTokenRequest()
	assert.Equal(t, "scope-a scope-b", req.Get("scope"))
	assert.Equal(t, "refresh-old", req.Get("refresh_token"))
	assert.Equal(t, "client", req.Get("client_id"))
}

func TestRefresh_RefusedTokenIsARetrieveError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired"}`)) //nolint:errcheck // test server.
	}))
	t.Cleanup(srv.Close)

	_, err := Refresh(context.Background(), &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: srv.URL}}, "r")

	var retrieveErr *oauth2.RetrieveError
	require.ErrorAs(t, err, &retrieveErr)
	assert.Equal(t, "invalid_grant", retrieveErr.ErrorCode)
}
