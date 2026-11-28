package login

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/account"
)

// fakeProviders serves both providers' OAuth and identity endpoints.
type fakeProviders struct {
	server      *httptest.Server
	identity    map[string]any
	identityErr int
	refreshErr  int // status of refresh token requests, when set

	mu       sync.Mutex
	requests []url.Values // token requests
}

func newFakeProviders(t *testing.T) *fakeProviders {
	t.Helper()

	f := &fakeProviders{identity: map[string]any{"email": "User@Example.com"}}
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(body))
	}
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, r.ParseForm())
		f.mu.Lock()
		f.requests = append(f.requests, r.PostForm)
		f.mu.Unlock()

		access := "access"
		if r.PostForm.Get("grant_type") == "refresh_token" {
			if f.refreshErr != 0 {
				w.WriteHeader(f.refreshErr)
				write(w, map[string]any{"error": "invalid_grant"})

				return
			}

			access = "imap-access"
		}

		write(w, map[string]any{"access_token": access, "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer access", r.Header.Get("Authorization"))

		if f.identityErr != 0 {
			w.WriteHeader(f.identityErr)

			return
		}

		write(w, f.identity)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	return f
}

func (f *fakeProviders) endpoints() *Endpoints {
	u := f.server.URL

	return &Endpoints{
		GoogleAuth: u + "/auth", GoogleToken: u + "/token", GoogleUserinfo: u + "/whoami",
		MicrosoftAuth: u + "/auth", MicrosoftToken: u + "/token", GraphMe: u + "/whoami",
	}
}

func testClients() Clients {
	return Clients{GoogleID: "g-id", GoogleSecret: "g-secret", MicrosoftID: "m-id"}
}

// pastePrompts returns prompts that answer the paste-URL flow with a valid
// redirect, and records the sign-in URL shown.
func pastePrompts(t *testing.T, shown *url.URL) Prompts {
	t.Helper()

	return Prompts{
		ShowSignInURL: func(raw string) {
			parsed, err := url.Parse(raw)
			require.NoError(t, err)
			*shown = *parsed
		},
		ReadRedirect: func() (string, error) {
			return "http://127.0.0.1:53682/?code=c&state=" + shown.Query().Get("state"), nil
		},
	}
}

// Validates: PV-220
func TestLogin_GmailPasteURL(t *testing.T) {
	t.Parallel()

	f := newFakeProviders(t)

	var shown url.URL

	res, err := Login(context.Background(), &Options{
		Provider: account.Gmail, Clients: testClients(), Endpoints: f.endpoints(), Prompts: pastePrompts(t, &shown),
	})
	require.NoError(t, err)
	assert.Equal(t, account.ID{Provider: account.Gmail, Email: "user@example.com"}, res.ID)
	assert.Equal(t, "refresh", res.Token.RefreshToken)
	assert.Nil(t, res.IMAP)
	assert.Equal(t, "g-id", shown.Query().Get("client_id"))
	assert.Equal(t, googleRedirect, shown.Query().Get("redirect_uri"))
	assert.Contains(t, shown.Query().Get("scope"), "https://www.googleapis.com/auth/gmail.insert")
}

// Validates: PV-221
func TestLogin_OutlookOneSignInGrantsGraphAndIMAP(t *testing.T) {
	t.Parallel()

	f := newFakeProviders(t)
	f.identity = map[string]any{"mail": nil, "userPrincipalName": "user@outlook.example.com"}

	var shown url.URL

	res, err := Login(context.Background(), &Options{
		Provider: account.Outlook, Clients: testClients(), Endpoints: f.endpoints(), Prompts: pastePrompts(t, &shown),
	})
	require.NoError(t, err)
	assert.Equal(t, "outlook:user@outlook.example.com", res.ID.String())
	assert.Equal(t, microsoftRedirect, shown.Query().Get("redirect_uri"))
	assert.Equal(t, "m-id", shown.Query().Get("client_id"))

	// One sign-in asks for both APIs (D161).
	scope := shown.Query().Get("scope")
	assert.Contains(t, scope, "Mail.ReadWrite")
	assert.Contains(t, scope, IMAPScope)

	// The code is redeemed for Graph, the refresh token for IMAP.
	f.mu.Lock()
	requests := slices.Clone(f.requests)
	f.mu.Unlock()

	require.Len(t, requests, 2)
	assert.Equal(t, "authorization_code", requests[0].Get("grant_type"))
	assert.NotContains(t, requests[0].Get("scope"), IMAPScope)
	assert.Equal(t, "refresh_token", requests[1].Get("grant_type"))
	assert.Equal(t, "offline_access "+IMAPScope, requests[1].Get("scope"))
	assert.Equal(t, "access", res.Token.AccessToken)
	assert.Equal(t, "imap-access", res.IMAP.AccessToken)
}

func TestLogin_OutlookWithoutIMAPStillReturnsTheAccount(t *testing.T) {
	t.Parallel()

	f := newFakeProviders(t)
	f.identity = map[string]any{"userPrincipalName": "user@outlook.example.com"}
	f.refreshErr = http.StatusBadRequest

	var shown url.URL

	res, err := Login(context.Background(), &Options{
		Provider: account.Outlook, Clients: testClients(), Endpoints: f.endpoints(), Prompts: pastePrompts(t, &shown),
	})
	require.ErrorContains(t, err, "get imap access for outlook:user@outlook.example.com")
	require.NotNil(t, res)
	assert.Equal(t, "access", res.Token.AccessToken)
	assert.Nil(t, res.IMAP)
}

func TestLogin_IdentityFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity map[string]any
		status   int
		wantErr  string
	}{
		{name: "http error", status: http.StatusUnauthorized, wantErr: "401"},
		{name: "no email", identity: map[string]any{"name": "User"}, wantErr: "no email address"},
		{name: "invalid email", identity: map[string]any{"email": "not-an-email"}, wantErr: "invalid email"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeProviders(t)
			f.identity = tt.identity
			f.identityErr = tt.status

			var shown url.URL

			res, err := Login(context.Background(), &Options{
				Provider: account.Gmail, Clients: testClients(), Endpoints: f.endpoints(), Prompts: pastePrompts(t, &shown),
			})
			assert.Nil(t, res)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Validates: PV-230
func TestOAuthConfig_MissingClientID(t *testing.T) {
	t.Parallel()

	ep := DefaultEndpoints()

	_, err := OAuthConfig(account.Gmail, Clients{GoogleID: "id-without-secret"}, &ep)
	require.ErrorIs(t, err, ErrNoClientID)
	assert.Contains(t, err.Error(), "config.toml")

	for _, p := range []account.Provider{account.Gmail, account.Outlook} {
		cfg, err := OAuthConfig(p, Clients{}, &ep)
		require.NoError(t, err)
		assert.NotEmpty(t, cfg.ClientID, "built-in client ID for %s", p)
	}
}

// Validates: PV-230
func TestGoogleCredentials_OverrideIsAPair(t *testing.T) {
	t.Parallel()

	id, secret := googleCredentials(Clients{GoogleID: "mine"})
	assert.Equal(t, "mine", id)
	assert.Empty(t, secret, "a config ID must never be paired with the built-in secret")
}

func TestOAuthConfig_UsesConsumersTenant(t *testing.T) {
	t.Parallel()

	ep := DefaultEndpoints()
	cfg, err := OAuthConfig(account.Outlook, testClients(), &ep)
	require.NoError(t, err)
	assert.Contains(t, cfg.Endpoint.TokenURL, "/consumers/")
	assert.Contains(t, cfg.Endpoint.AuthURL, "/consumers/")
	assert.Contains(t, cfg.Scopes, "offline_access")
}
