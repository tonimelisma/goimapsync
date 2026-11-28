package googleapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "x"}) //nolint:errcheck // test server.
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/expired":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Sync token is expired.","status":"FAILED_PRECONDITION",` + //nolint:errcheck // test server.
				`"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"EXPIRED_SYNC_TOKEN"}]}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Bad things"}}`)) //nolint:errcheck // test server.
		}
	}))
	t.Cleanup(srv.Close)

	var out struct {
		ID string `json:"id"`
	}

	require.NoError(t, googleapi.GetJSON(t.Context(), http.DefaultClient, srv.URL+"/ok", &out))
	assert.Equal(t, "x", out.ID)

	require.ErrorIs(t, googleapi.GetJSON(t.Context(), http.DefaultClient, srv.URL+"/missing", &out), googleapi.ErrNotFound)

	// Seen live: messages.list with a field mask answers 204 when empty.
	empty := struct {
		ID string `json:"id"`
	}{ID: "unchanged"}
	require.NoError(t, googleapi.GetJSON(t.Context(), http.DefaultClient, srv.URL+"/empty", &empty))
	assert.Equal(t, "unchanged", empty.ID)

	err := googleapi.GetJSON(t.Context(), http.DefaultClient, srv.URL+"/bad", &out)

	var apiErr *googleapi.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Status)
	assert.Equal(t, "Bad things", apiErr.Message)

	err = googleapi.GetJSON(t.Context(), http.DefaultClient, srv.URL+"/expired", &out)
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "EXPIRED_SYNC_TOKEN", apiErr.Reason)
}

// Validates: PV-111
func TestRateLimited(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   bool
	}{
		"user rate limit": {http.StatusForbidden, `{"error":{"errors":[{"reason":"userRateLimitExceeded"}]}}`, true},
		"rate limit":      {http.StatusForbidden, `{"error":{"errors":[{"reason":"rateLimitExceeded"}]}}`, true},
		"permission":      {http.StatusForbidden, `{"error":{"errors":[{"reason":"insufficientPermissions"}]}}`, false},
		"not json":        {http.StatusForbidden, `nope`, false},
		"other status":    {http.StatusBadRequest, `{"error":{"errors":[{"reason":"rateLimitExceeded"}]}}`, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(tc.status)
			_, _ = rec.WriteString(tc.body) //nolint:errcheck // recorder writes do not fail.
			resp := rec.Result()

			assert.Equal(t, tc.want, googleapi.RateLimited(resp))

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tc.body, string(body), "body stays readable")
			require.NoError(t, resp.Body.Close())
		})
	}
}
