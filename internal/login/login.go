// Package login signs an account in with its provider and finds out which
// account it is. It owns the provider OAuth settings: endpoints, scopes and
// client IDs. Saving the token is the account registry's job.
package login

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/oauthflow"
)

// Client IDs of pimvault's own apps (D75, D136):
// pimvault's Google Cloud project and its Entra app for personal Microsoft
// accounts. config.toml can override them. Google treats
// a desktop app's client secret as non-confidential.
//
//nolint:gosec // G101: these are public by design.
const (
	defaultGoogleClientID     = "486829218720-0894q7g3qehquf3pgkdhjla7mlla854s.apps.googleusercontent.com"
	defaultGoogleClientSecret = "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"
	defaultMicrosoftClientID  = "d56fa0d9-7adb-4aa6-9c2f-3cd10ec05e23"
)

// Clients holds client IDs from config.toml; empty fields use the defaults.
type Clients struct {
	GoogleID     string
	GoogleSecret string
	MicrosoftID  string
}

// Endpoints are the provider URLs. Tests point them at fake servers.
type Endpoints struct {
	GoogleAuth     string
	GoogleToken    string
	GoogleUserinfo string
	MicrosoftAuth  string
	MicrosoftToken string
	GraphMe        string
}

// DefaultEndpoints returns the real provider URLs. Microsoft uses the
// consumers tenant: pimvault supports personal Outlook.com accounts (D2).
func DefaultEndpoints() Endpoints {
	const msBase = "https://login.microsoftonline.com/consumers/oauth2/v2.0"

	return Endpoints{ //nolint:gosec // G101: these are public endpoint URLs, not credentials.
		GoogleAuth:     "https://accounts.google.com/o/oauth2/auth",
		GoogleToken:    "https://oauth2.googleapis.com/token",
		GoogleUserinfo: "https://openidconnect.googleapis.com/v1/userinfo",
		MicrosoftAuth:  msBase + "/authorize",
		MicrosoftToken: msBase + "/token",
		GraphMe:        "https://graph.microsoft.com/v1.0/me?$select=mail,userPrincipalName",
	}
}

// Redirect addresses for the paste-URL flow. Nothing listens there; the user
// copies the address the browser was sent to. Google desktop apps accept any
// loopback port; Microsoft matches the registered http://localhost and
// ignores the port.
const (
	googleRedirect    = "http://127.0.0.1:53682"
	microsoftRedirect = "http://localhost:53682"
)

// Scopes request read and write access at once (D135): backup, restore and
// migrate all work after one sign-in.
func scopes(p account.Provider) []string {
	if p == account.Gmail {
		return []string{
			openID,
			"email",
			"https://www.googleapis.com/auth/gmail.readonly",
			"https://www.googleapis.com/auth/gmail.insert",
			"https://www.googleapis.com/auth/gmail.labels",
			"https://www.googleapis.com/auth/calendar",
			"https://www.googleapis.com/auth/contacts",
			"https://www.googleapis.com/auth/tasks",
		}
	}

	return []string{
		offlineAccess,
		"User.Read",
		"Mail.ReadWrite",
		"Calendars.ReadWrite",
		"Contacts.ReadWrite",
		"Tasks.ReadWrite",
		"MailboxSettings.Read", // the account's time zone (D174)
	}
}

// offlineAccess asks Microsoft for a refresh token; openID is the OpenID
// Connect scope Google needs to name the signed-in account.
const (
	offlineAccess = "offline_access"
	openID        = "openid"
)

// IMAPScope allows IMAP access to Outlook.com mail, which restore and
// migrate use to append mail (D56). It is a different API from Graph, so it
// gets tokens of its own, from the same sign-in (F61, F62).
const IMAPScope = "https://outlook.office.com/IMAP.AccessAsUser.All"

// IMAPConfig returns the OAuth settings for Outlook.com's IMAP tokens.
func IMAPConfig(clients Clients, ep *Endpoints) *oauth2.Config {
	cfg, _ := OAuthConfig(account.Outlook, clients, ep) //nolint:errcheck // Outlook settings cannot fail.
	cfg.Scopes = []string{offlineAccess, IMAPScope}

	return cfg
}

// ErrNoClientID means config.toml sets a Google client ID without its
// secret.
var ErrNoClientID = errors.New("no client ID")

// googleCredentials returns the Google client ID and secret. They are a pair:
// if config.toml sets the ID, the secret comes from config.toml too, never
// from the built-in defaults.
func googleCredentials(clients Clients) (id, secret string) {
	if clients.GoogleID != "" {
		return clients.GoogleID, clients.GoogleSecret
	}

	return defaultGoogleClientID, defaultGoogleClientSecret
}

// OAuthConfig returns the OAuth settings for a provider.
func OAuthConfig(p account.Provider, clients Clients, ep *Endpoints) (*oauth2.Config, error) {
	switch p {
	case account.Gmail:
		id, secret := googleCredentials(clients)
		if id == "" || secret == "" {
			return nil, fmt.Errorf("configure gmail sign-in: %w: set google_client_secret with google_client_id in config.toml",
				ErrNoClientID)
		}

		return &oauth2.Config{
			ClientID:     id,
			ClientSecret: secret,
			Scopes:       scopes(p),
			RedirectURL:  googleRedirect,
			Endpoint: oauth2.Endpoint{
				AuthURL:   ep.GoogleAuth,
				TokenURL:  ep.GoogleToken,
				AuthStyle: oauth2.AuthStyleInParams,
			},
		}, nil
	case account.Outlook:
		id := cmp.Or(clients.MicrosoftID, defaultMicrosoftClientID)

		return &oauth2.Config{
			ClientID:    id,
			Scopes:      scopes(p),
			RedirectURL: microsoftRedirect,
			Endpoint: oauth2.Endpoint{
				AuthURL:   ep.MicrosoftAuth,
				TokenURL:  ep.MicrosoftToken,
				AuthStyle: oauth2.AuthStyleInParams,
			},
		}, nil
	default:
		return nil, fmt.Errorf("configure sign-in: unknown provider %q", p)
	}
}

// requestTimeout bounds each HTTP request of a sign-in, so a stalled network
// fails instead of hanging.
const requestTimeout = 60 * time.Second

// Prompts is how a sign-in talks to the user.
type Prompts struct {
	// ShowSignInURL shows the URL to open.
	ShowSignInURL func(string)
	// ReadRedirect returns the address the user pasted.
	ReadRedirect func() (string, error)
}

// Options describe one sign-in. Both providers use the paste-URL flow
// (D70, D164).
type Options struct {
	Provider  account.Provider
	Clients   Clients
	Endpoints *Endpoints
	Prompts   Prompts
}

// Result is a signed-in account: its token, and for Outlook.com its IMAP
// token, which the same sign-in grants (D161).
type Result struct {
	ID    account.ID
	Token *oauth2.Token
	IMAP  *oauth2.Token
}

// Login signs in and returns the account the provider says was signed in,
// with its tokens. An Outlook.com sign-in asks for Graph and IMAP access at
// once; the code is redeemed for Graph, and the refresh token then for IMAP
// (F61, F62). When only the IMAP token fails, Login returns the result
// without it along with the error.
func Login(ctx context.Context, opts *Options) (*Result, error) {
	cfg, err := OAuthConfig(opts.Provider, opts.Clients, opts.Endpoints)
	if err != nil {
		return nil, err
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: requestTimeout})

	signIn := *cfg

	var exchange []oauth2.AuthCodeOption

	if opts.Provider == account.Outlook {
		signIn.Scopes = append(slices.Clone(cfg.Scopes), IMAPScope)
		exchange = append(exchange, oauth2.SetAuthURLParam("scope", strings.Join(cfg.Scopes, " ")))
	}

	tok, err := oauthflow.PasteURL(ctx, &signIn, opts.Prompts.ShowSignInURL, opts.Prompts.ReadRedirect, exchange...)
	if err != nil {
		return nil, fmt.Errorf("sign in to %s: %w", opts.Provider, err)
	}

	email, err := identify(ctx, opts.Provider, tok, opts.Endpoints)
	if err != nil {
		return nil, err
	}

	id, err := account.Parse(string(opts.Provider) + ":" + email)
	if err != nil {
		return nil, fmt.Errorf("identify signed-in account: %w", err)
	}

	res := &Result{ID: id, Token: tok}

	if opts.Provider == account.Outlook {
		if res.IMAP, err = oauthflow.Refresh(ctx, IMAPConfig(opts.Clients, opts.Endpoints), tok.RefreshToken); err != nil {
			return res, fmt.Errorf("get imap access for %s: %w", id, err)
		}
	}

	return res, nil
}

// maxIdentityResponse bounds the identity response read into memory.
const maxIdentityResponse = 1 << 20

// identify asks the provider which account the token belongs to.
func identify(ctx context.Context, p account.Provider, tok *oauth2.Token, ep *Endpoints) (string, error) {
	endpoint := ep.GoogleUserinfo
	if p == account.Outlook {
		endpoint = ep.GraphMe
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("identify signed-in account: %w", err)
	}

	resp, err := oauth2.NewClient(ctx, oauth2.StaticTokenSource(tok)).Do(req)
	if err != nil {
		return "", fmt.Errorf("identify signed-in account: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // reader-side close; the body was read to the end.

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdentityResponse))
	if err != nil {
		return "", fmt.Errorf("identify signed-in account: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("identify signed-in account: %s returned %s", endpoint, resp.Status)
	}

	var who struct {
		Email             string `json:"email"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}

	if err := json.Unmarshal(body, &who); err != nil {
		return "", fmt.Errorf("identify signed-in account: decode response: %w", err)
	}

	email := cmp.Or(who.Email, who.Mail, who.UserPrincipalName)
	if email == "" {
		return "", errors.New("identify signed-in account: provider returned no email address")
	}

	return email, nil
}
