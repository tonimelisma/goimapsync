// Package oauthflow runs OAuth 2.0 sign-in flows for command-line tools on
// headless machines, and wraps saved tokens in a token source that persists
// refreshed tokens.
//
// The flow is paste URL: authorization code with PKCE, where the user signs
// in on any device and pastes the address the browser was redirected to.
//
// Adapted from github.com/tonimelisma/onedrive-go (internal/graph auth), on
// upstream golang.org/x/oauth2. This package must not import pimvault code,
// so it can later move to its own repository.
package oauthflow

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// stateBytes is the number of random bytes in the OAuth state parameter.
const stateBytes = 16

// PasteURL runs the authorization-code flow with PKCE without a local web
// server. cfg.RedirectURL must be a loopback address registered for the app.
// prompt receives the sign-in URL; readRedirect returns the address the
// browser ended up on, pasted by the user. The redirect page itself never
// loads, because nothing listens on the loopback address. exchange adds
// parameters to the code redemption, such as a narrower scope.
func PasteURL(
	ctx context.Context,
	cfg *oauth2.Config,
	prompt func(authURL string),
	readRedirect func() (string, error),
	exchange ...oauth2.AuthCodeOption,
) (*oauth2.Token, error) {
	state, err := randomState()
	if err != nil {
		return nil, err
	}

	verifier := oauth2.GenerateVerifier()

	prompt(cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.S256ChallengeOption(verifier),
	))

	pasted, err := readRedirect()
	if err != nil {
		return nil, fmt.Errorf("read redirected URL: %w", err)
	}

	code, err := codeFromRedirect(pasted, state)
	if err != nil {
		return nil, err
	}

	tok, err := cfg.Exchange(ctx, code, append([]oauth2.AuthCodeOption{oauth2.VerifierOption(verifier)}, exchange...)...)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}

	return tok, nil
}

// ErrStateMismatch means the pasted URL does not belong to this sign-in.
var ErrStateMismatch = errors.New("state does not match this sign-in")

// codeFromRedirect extracts the authorization code from the pasted redirect
// URL after checking the state and any error the provider reported.
func codeFromRedirect(pasted, wantState string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(pasted))
	if err != nil {
		return "", fmt.Errorf("parse redirected URL: %w", err)
	}

	query := u.Query()

	if providerErr := query.Get("error"); providerErr != "" {
		if desc := query.Get("error_description"); desc != "" {
			return "", fmt.Errorf("parse redirected URL: provider returned %s: %s", providerErr, desc)
		}

		return "", fmt.Errorf("parse redirected URL: provider returned %s", providerErr)
	}

	if query.Get("state") != wantState {
		return "", fmt.Errorf("parse redirected URL: %w", ErrStateMismatch)
	}

	code := query.Get("code")
	if code == "" {
		return "", errors.New("parse redirected URL: no authorization code in it")
	}

	return code, nil
}

func randomState() (string, error) {
	b := make([]byte, stateBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}

	return hex.EncodeToString(b), nil
}

// TokenSource returns a token source for a saved token that refreshes it when
// needed and passes every refreshed token to save. A failed save is logged,
// not returned: the refreshed token still works for this run, and the next
// run refreshes again from the old refresh token if it is still valid.
func TokenSource(
	ctx context.Context,
	cfg *oauth2.Config,
	tok *oauth2.Token,
	save func(*oauth2.Token) error,
	logger *slog.Logger,
) oauth2.TokenSource {
	return &savingSource{src: cfg.TokenSource(ctx, tok), save: save, logger: logger, last: tok}
}

// savingSource wraps the config's token source, which returns the same token
// until it refreshes it: a token that differs from the last one returned is a
// refreshed one, and is saved. The lock covers the call to the wrapped source
// too, so tokens are compared and saved in the order they were issued and an
// older token is never saved over a newer one; the wrapped source serializes
// its callers anyway.
type savingSource struct {
	src    oauth2.TokenSource
	save   func(*oauth2.Token) error
	logger *slog.Logger

	mu   sync.Mutex
	last *oauth2.Token
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok, err := s.src.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // as the wrapped source returns it: callers test for *oauth2.RetrieveError.
	}

	if s.last != nil && tok.AccessToken == s.last.AccessToken && tok.RefreshToken == s.last.RefreshToken {
		return tok, nil
	}

	s.last = tok

	if err := s.save(tok); err != nil {
		s.logger.Warn("save refreshed token", slog.String("error", err.Error()))

		return tok, nil
	}

	s.logger.Debug("saved refreshed token", slog.Time("expiry", tok.Expiry))

	return tok, nil
}

// maxTokenResponse bounds a token response read into memory.
const maxTokenResponse = 1 << 20

// refreshToken is the refresh grant's name and its parameter's.
const refreshToken = "refresh_token"

// Refresh redeems a refresh token for cfg.Scopes. Unlike a token source, it
// names the scopes, which Microsoft needs to issue a token for an API other
// than the one the sign-in's code was redeemed for. A refused refresh token
// returns an *oauth2.RetrieveError.
func Refresh(ctx context.Context, cfg *oauth2.Config, token string) (*oauth2.Token, error) {
	form := url.Values{
		"grant_type": {refreshToken},
		refreshToken: {token},
		"client_id":  {cfg.ClientID},
		"scope":      {strings.Join(cfg.Scopes, " ")},
	}
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := http.DefaultClient
	if c, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		client = c
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close error changes nothing.

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse))
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	var parsed struct {
		oauth2.Token

		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"` //nolint:tagliatelle // OAuth 2.0 field name.
	}

	if err := json.Unmarshal(body, &parsed); err != nil || resp.StatusCode != http.StatusOK || parsed.AccessToken == "" {
		return nil, fmt.Errorf("refresh token: %w", &oauth2.RetrieveError{
			Response: resp, Body: body, ErrorCode: parsed.Error, ErrorDescription: parsed.ErrorDescription,
		})
	}

	tok := &parsed.Token
	tok.RefreshToken = cmp.Or(tok.RefreshToken, token)

	if tok.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	return tok, nil
}
