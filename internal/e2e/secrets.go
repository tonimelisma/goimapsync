// Package e2e runs pimvault against the real test accounts
// (D193, D202). The tests are behind the e2e build tag;
// this file holds what they need to fetch and store the accounts' tokens.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"golang.org/x/oauth2"
)

// apiVersion is the Key Vault REST API version used.
const apiVersion = "7.4"

// errorBodyLimit bounds how much of an error response is read.
const errorBodyLimit = 64 << 10

// Secret is what a test account's secret holds: the account, its token
// and, for Outlook.com, its IMAP token (O138).
type Secret struct {
	Account string        `json:"account"`
	Token   *oauth2.Token `json:"token"`
	IMAP    *oauth2.Token `json:"imap,omitempty"`
}

// Secrets reads and writes the secrets of one Azure Key Vault. BaseURL is
// the vault's URL, https://<vault>.vault.azure.net.
type Secrets struct {
	HTTP    *http.Client
	BaseURL string
}

// ErrNoCredentials means the environment names no Key Vault credentials.
var ErrNoCredentials = errors.New("set PIMVAULT_E2E_ACCESS_TOKEN and PIMVAULT_E2E_KEY_VAULT")

// vaultName is what Azure allows in a vault name; it becomes a host name.
var vaultName = regexp.MustCompile(`^[A-Za-z0-9-]{3,24}$`)

// FromEnv returns the Key Vault secrets reached with the access token in
// PIMVAULT_E2E_ACCESS_TOKEN, in the vault named in PIMVAULT_E2E_KEY_VAULT
// (D202): in GitHub Actions a short-lived token the workflow gets from Entra
// ID for GitHub's OIDC token; in a cloud session the token the session-start
// hook gets for the shared cloud app (script/live/vault-token).
func FromEnv(ctx context.Context) (*Secrets, error) {
	token := os.Getenv("PIMVAULT_E2E_ACCESS_TOKEN")
	if token == "" {
		return nil, ErrNoCredentials
	}

	vault := os.Getenv("PIMVAULT_E2E_KEY_VAULT")
	if !vaultName.MatchString(vault) {
		return nil, errors.New("read access token: PIMVAULT_E2E_KEY_VAULT is not a vault name")
	}

	return AccessToken(ctx, token, vault), nil
}

// AccessToken returns the secrets of a vault, reached with an access token.
// The token is not refreshed.
func AccessToken(ctx context.Context, token, vault string) *Secrets {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})

	return &Secrets{HTTP: oauth2.NewClient(ctx, src), BaseURL: "https://" + vault + ".vault.azure.net"}
}

// Access returns a secret's latest version.
func (s *Secrets) Access(ctx context.Context, name string) (*Secret, error) {
	var resp struct {
		Value string `json:"value"`
	}

	if err := s.do(ctx, http.MethodGet, name, nil, &resp); err != nil {
		return nil, fmt.Errorf("read secret %s: %w", name, err)
	}

	var secret Secret
	if err := json.Unmarshal([]byte(resp.Value), &secret); err != nil {
		return nil, fmt.Errorf("read secret %s: %w", name, err)
	}

	return &secret, nil
}

// Add stores a new version of a secret: setting a Key Vault secret keeps
// the earlier versions.
func (s *Secrets) Add(ctx context.Context, name string, secret *Secret) error {
	data, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("encode secret %s: %w", name, err)
	}

	body, err := json.Marshal(map[string]string{"value": string(data)})
	if err != nil {
		return fmt.Errorf("encode secret %s: %w", name, err)
	}

	if err := s.do(ctx, http.MethodPut, name, body, nil); err != nil {
		return fmt.Errorf("store secret %s: %w", name, err)
	}

	return nil
}

// do sends one Key Vault request for a secret and decodes the JSON response
// into out, unless out is nil. A failure names Key Vault's error code and
// message, which never hold a secret's value.
func (s *Secrets) do(ctx context.Context, method, name string, body []byte, out any) error {
	target := s.BaseURL + "/secrets/" + url.PathEscape(name) + "?api-version=" + apiVersion

	var reader io.Reader = http.NoBody
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // a close error changes nothing.

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return apiError(resp)
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// apiError reads Key Vault's error format, {"error":{"code","message"}}.
func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit)) //nolint:errcheck // a partial body still names the error.

	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal(raw, &parsed) == nil && parsed.Error.Code != "" {
		return fmt.Errorf("key vault: http %d: %s: %s", resp.StatusCode, parsed.Error.Code, parsed.Error.Message)
	}

	return fmt.Errorf("key vault: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
}
