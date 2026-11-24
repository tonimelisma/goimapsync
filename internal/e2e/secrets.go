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
	"golang.org/x/oauth2/jwt"

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
var ErrNoCredentials = errors.New("set PIMVAULT_E2E_ACCESS_TOKEN and PIMVAULT_E2E_PROJECT, or PIMVAULT_E2E_SERVICE_ACCOUNT")

// FromEnv returns the Secret Manager secrets the environment's credentials
// reach (D199): in GitHub Actions a short-lived access token for the
// service account from Workload Identity Federation, in
// PIMVAULT_E2E_ACCESS_TOKEN with the project in PIMVAULT_E2E_PROJECT; in a
// cloud session the service account key in PIMVAULT_E2E_SERVICE_ACCOUNT.
func FromEnv(ctx context.Context) (*Secrets, error) {
	if token := os.Getenv("PIMVAULT_E2E_ACCESS_TOKEN"); token != "" {
		project := os.Getenv("PIMVAULT_E2E_PROJECT")
		if project == "" {
			return nil, errors.New("read access token: PIMVAULT_E2E_PROJECT is not set")
		}

		return AccessToken(ctx, token, project), nil
	}

	if key := os.Getenv("PIMVAULT_E2E_SERVICE_ACCOUNT"); key != "" {
		return ServiceAccount(ctx, []byte(key))
	}

	return nil, ErrNoCredentials
}

// AccessToken returns the Secret Manager secrets of a project, reached with
// an access token. The token is not refreshed.
func AccessToken(ctx context.Context, token, project string) *Secrets {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})

	return &Secrets{HTTP: oauth2.NewClient(ctx, src), BaseURL: SecretManagerURL, Project: project}
}

// ServiceAccount returns the Secret Manager secrets of the service account
// whose JSON key is given, in the project the key belongs to.
func ServiceAccount(ctx context.Context, key []byte) (*Secrets, error) {
	var sa struct {
		ClientEmail string `json:"client_email"` //nolint:tagliatelle // Google's key file names.
		PrivateKey  string `json:"private_key"`  //nolint:tagliatelle // as above.
		TokenURI    string `json:"token_uri"`    //nolint:tagliatelle // as above.
		ProjectID   string `json:"project_id"`   //nolint:tagliatelle // as above.
	}

	if err := json.Unmarshal(key, &sa); err != nil {
		return nil, fmt.Errorf("read service account key: %w", err)
	}

	if sa.ProjectID == "" {
		return nil, errors.New("read service account key: no project_id")
	}

	cfg := &jwt.Config{
		Email:      sa.ClientEmail,
		PrivateKey: []byte(sa.PrivateKey),
		TokenURL:   sa.TokenURI,
		Scopes:     []string{"https://www.googleapis.com/auth/cloud-platform"},
	}

	return &Secrets{HTTP: oauth2.NewClient(ctx, cfg.TokenSource(ctx)), BaseURL: SecretManagerURL, Project: sa.ProjectID}, nil
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
