package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/e2e"
)

func TestSecrets_AccessAndAdd(t *testing.T) {
	stored := `{"account":"gmail:a@example.com","token":{"access_token":"at","refresh_token":"rt","token_type":"Bearer"}}`

	var added []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/secrets/s" || r.URL.Query().Get("api-version") != "7.4" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"value": stored, "id": "x"}) //nolint:errcheck // test server.
		case http.MethodPut:
			var body struct{ Value string }

			raw, _ := io.ReadAll(r.Body) //nolint:errcheck // test server.
			assert.NoError(t, json.Unmarshal(raw, &body))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			added = []byte(body.Value)
			_, _ = w.Write([]byte(`{"id":"x"}`)) //nolint:errcheck // test server.
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)

	secrets := &e2e.Secrets{HTTP: srv.Client(), BaseURL: srv.URL}

	s, err := secrets.Access(t.Context(), "s")
	require.NoError(t, err)
	assert.Equal(t, "gmail:a@example.com", s.Account)
	assert.Equal(t, "rt", s.Token.RefreshToken)
	assert.Nil(t, s.IMAP)

	s.IMAP = &oauth2.Token{RefreshToken: "imap-rt"}
	require.NoError(t, secrets.Add(t.Context(), "s", s))

	var got e2e.Secret
	require.NoError(t, json.Unmarshal(added, &got))
	assert.Equal(t, "imap-rt", got.IMAP.RefreshToken)
	assert.Equal(t, "rt", got.Token.RefreshToken)
}

func TestSecrets_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"no access"}}`)) //nolint:errcheck // test server.
	}))
	t.Cleanup(srv.Close)

	secrets := &e2e.Secrets{HTTP: srv.Client(), BaseURL: srv.URL}

	_, err := secrets.Access(t.Context(), "s")
	require.EqualError(t, err, "read secret s: key vault: http 403: Forbidden: no access")

	err = secrets.Add(t.Context(), "s", &e2e.Secret{})
	require.EqualError(t, err, "store secret s: key vault: http 403: Forbidden: no access")
}

func TestFromEnv_AccessToken(t *testing.T) {
	t.Setenv("PIMVAULT_E2E_ACCESS_TOKEN", "vault-token")
	t.Setenv("PIMVAULT_E2E_KEY_VAULT", "kv-test")

	var auth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"value": `{"account":"gmail:a@example.com"}`}) //nolint:errcheck // test server.
	}))
	t.Cleanup(srv.Close)

	secrets, err := e2e.FromEnv(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "https://kv-test.vault.azure.net", secrets.BaseURL)

	secrets.BaseURL = srv.URL

	s, err := secrets.Access(t.Context(), "s")
	require.NoError(t, err)
	assert.Equal(t, "gmail:a@example.com", s.Account)
	assert.Equal(t, "Bearer vault-token", auth)
}

func TestFromEnv_AccessTokenNeedsVault(t *testing.T) {
	t.Setenv("PIMVAULT_E2E_ACCESS_TOKEN", "vault-token")

	for _, vault := range []string{"", "evil.example.com/x", "a"} {
		t.Setenv("PIMVAULT_E2E_KEY_VAULT", vault)

		_, err := e2e.FromEnv(t.Context())
		require.ErrorContains(t, err, "PIMVAULT_E2E_KEY_VAULT", vault)
	}
}

func TestFromEnv_None(t *testing.T) {
	t.Setenv("PIMVAULT_E2E_ACCESS_TOKEN", "")

	_, err := e2e.FromEnv(t.Context())
	require.ErrorIs(t, err, e2e.ErrNoCredentials)
}
