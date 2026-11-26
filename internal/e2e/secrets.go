// Package e2e runs pimvault against the real test accounts
// (docs/requirements.md D158, D193). The tests are behind the e2e build tag;
// this file holds what they need to fetch and store the accounts' tokens.
package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/googleapi"
)

// SecretManagerURL is Google Secret Manager's API root.
const SecretManagerURL = "https://secretmanager.googleapis.com/v1" //nolint:gosec // G101: an API root, not a credential.

// Secret is what a test account's secret holds: the account, its token
// and, for Outlook.com, its IMAP token (O138).
type Secret struct {
	Account string        `json:"account"`
	Token   *oauth2.Token `json:"token"`
	IMAP    *oauth2.Token `json:"imap,omitempty"`
}

// Secrets reads and writes secrets in one project.
type Secrets struct {
	HTTP    *http.Client
	BaseURL string
	Project string
}

// ErrNoCredentials means the environment names no Secret Manager credentials.
var ErrNoCredentials = errors.New("set PIMVAULT_E2E_ACCESS_TOKEN and PIMVAULT_E2E_PROJECT")

// FromEnv returns the Secret Manager secrets reached with the access token in
// PIMVAULT_E2E_ACCESS_TOKEN, in the project in PIMVAULT_E2E_PROJECT (D199,
// D201): in GitHub Actions a short-lived token for the e2e service account
// from Workload Identity Federation; on the owner's Mac the owner's own
// `gcloud auth print-access-token`. No service account key exists.
func FromEnv(ctx context.Context) (*Secrets, error) {
	if token := os.Getenv("PIMVAULT_E2E_ACCESS_TOKEN"); token != "" {
		project := os.Getenv("PIMVAULT_E2E_PROJECT")
		if project == "" {
			return nil, errors.New("read access token: PIMVAULT_E2E_PROJECT is not set")
		}

		return AccessToken(ctx, token, project), nil
	}

	return nil, ErrNoCredentials
}

// AccessToken returns the Secret Manager secrets of a project, reached with
// an access token. The token is not refreshed.
func AccessToken(ctx context.Context, token, project string) *Secrets {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})

	return &Secrets{HTTP: oauth2.NewClient(ctx, src), BaseURL: SecretManagerURL, Project: project}
}

func (s *Secrets) path(name string) string {
	return s.BaseURL + "/projects/" + url.PathEscape(s.Project) + "/secrets/" + url.PathEscape(name)
}

// Access returns a secret's latest version.
func (s *Secrets) Access(ctx context.Context, name string) (*Secret, error) {
	var resp struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}

	if err := googleapi.GetJSON(ctx, s.HTTP, s.path(name)+"/versions/latest:access", &resp); err != nil {
		return nil, fmt.Errorf("read secret %s: %w", name, err)
	}

	data, err := base64.StdEncoding.DecodeString(resp.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("read secret %s: %w", name, err)
	}

	var secret Secret
	if err := json.Unmarshal(data, &secret); err != nil {
		return nil, fmt.Errorf("read secret %s: %w", name, err)
	}

	return &secret, nil
}

// Add stores a new version of a secret.
func (s *Secrets) Add(ctx context.Context, name string, secret *Secret) error {
	data, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("encode secret %s: %w", name, err)
	}

	body, err := json.Marshal(map[string]any{"payload": map[string]string{"data": base64.StdEncoding.EncodeToString(data)}})
	if err != nil {
		return fmt.Errorf("encode secret %s: %w", name, err)
	}

	if err := googleapi.Do(ctx, s.HTTP, http.MethodPost, s.path(name)+":addVersion", "application/json", body, nil); err != nil {
		return fmt.Errorf("store secret %s: %w", name, err)
	}

	return nil
}
