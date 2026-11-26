package e2e_test

import (
	"encoding/base64"
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
		switch r.URL.Path {
		case "/projects/p/secrets/s/versions/latest:access":
			_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // test server.
				"payload": map[string]string{"data": base64.StdEncoding.EncodeToString([]byte(stored))},
			})
		case "/projects/p/secrets/s:addVersion":
			var body struct {
				Payload struct{ Data string } `json:"payload"`
			}

			raw, _ := io.ReadAll(r.Body) //nolint:errcheck // test server.
			assert.NoError(t, json.Unmarshal(raw, &body))
			added, _ = base64.StdEncoding.DecodeString(body.Payload.Data) //nolint:errcheck // checked below.
			_, _ = w.Write([]byte("{}"))                                  //nolint:errcheck // test server.
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	secrets := &e2e.Secrets{HTTP: srv.Client(), BaseURL: srv.URL, Project: "p"}

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

func TestFromEnv_AccessToken(t *testing.T) {
	t.Setenv("PIMVAULT_E2E_ACCESS_TOKEN", "wif-token")
	t.Setenv("PIMVAULT_E2E_PROJECT", "p")

	var auth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // test server.
			"payload": map[string]string{"data": base64.StdEncoding.EncodeToString([]byte(`{"account":"gmail:a@example.com"}`))},
		})
	}))
	t.Cleanup(srv.Close)

	secrets, err := e2e.FromEnv(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "p", secrets.Project)

	secrets.BaseURL = srv.URL

	s, err := secrets.Access(t.Context(), "s")
	require.NoError(t, err)
	assert.Equal(t, "gmail:a@example.com", s.Account)
	assert.Equal(t, "Bearer wif-token", auth)
}

func TestFromEnv_AccessTokenNeedsProject(t *testing.T) {
	t.Setenv("PIMVAULT_E2E_ACCESS_TOKEN", "wif-token")
	t.Setenv("PIMVAULT_E2E_PROJECT", "")

	_, err := e2e.FromEnv(t.Context())
	require.ErrorContains(t, err, "PIMVAULT_E2E_PROJECT")
}

func TestFromEnv_None(t *testing.T) {
	t.Setenv("PIMVAULT_E2E_ACCESS_TOKEN", "")

	_, err := e2e.FromEnv(t.Context())
	require.ErrorIs(t, err, e2e.ErrNoCredentials)
}
