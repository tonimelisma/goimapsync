// Command seed fills two throwaway test accounts, one Gmail and one
// Outlook.com, with varied mail, calendars, contacts and tasks for live
// end-to-end runs. Each run adds a new batch tagged with the run's time.
//
// It writes into the accounts, so it refuses to run unless -confirm names
// both accounts exactly. Never point it at an account that holds real data.
//
//	go run ./script/live/seed -gmail-config DIR -outlook-config DIR -confirm gmail:a@gmail.example.com,outlook:b@outlook.example.com
//
// DIR is an XDG_CONFIG_HOME that pimvault login wrote the account's token
// into.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/httpretry"
	"github.com/tonimelisma/pimvault/internal/login"
	"github.com/tonimelisma/pimvault/internal/oauthflow"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	gmailConfig := flag.String("gmail-config", "", "XDG_CONFIG_HOME holding the Gmail test account's token")
	outlookConfig := flag.String("outlook-config", "", "XDG_CONFIG_HOME holding the Outlook test account's token")
	confirm := flag.String("confirm", "", "both account IDs, comma-separated, confirming they are throwaway accounts")
	only := flag.String("only", "", "seed only this provider: gmail or outlook")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	gmailID, gmailClient, err := client(ctx, *gmailConfig, account.Gmail, logger)
	if err != nil {
		return err
	}

	outlookID, outlookClient, err := client(ctx, *outlookConfig, account.Outlook, logger)
	if err != nil {
		return err
	}

	if want := gmailID.String() + "," + outlookID.String(); *confirm != want {
		return fmt.Errorf("refusing to write: -confirm must be exactly %q", want)
	}

	s := &seeder{
		gmail:   gmailClient,
		outlook: outlookClient,
		gAddr:   gmailID.Email,
		oAddr:   outlookID.Email,
		tag:     time.Now().UTC().Format("0102-1504"),
		rng:     rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1)), //nolint:gosec // G404: test data, not secrets.
		now:     time.Now().UTC().Truncate(time.Hour),
		logger:  logger,
	}

	s.mail = s.threads(60)

	var errs []error

	if *only != "outlook" {
		errs = append(errs, s.seedGmail(ctx))
	}

	if *only != "gmail" {
		errs = append(errs, s.seedOutlook(ctx))
	}

	return errors.Join(errs...)
}

// client returns the one account of a provider in a config directory and an
// HTTP client authorized as it.
func client(ctx context.Context, configHome string, p account.Provider, logger *slog.Logger) (account.ID, *http.Client, error) {
	if configHome == "" {
		return account.ID{}, nil, fmt.Errorf("no config directory for %s", p)
	}

	registry := account.NewRegistry(filepath.Join(configHome, "pimvault"))

	ids, err := registry.List()
	if err != nil {
		return account.ID{}, nil, err
	}

	var id account.ID

	for _, candidate := range ids {
		if candidate.Provider == p {
			id = candidate
		}
	}

	if id.Email == "" {
		return account.ID{}, nil, fmt.Errorf("no %s account in %s", p, configHome)
	}

	tok, err := registry.Token(id)
	if err != nil {
		return account.ID{}, nil, err
	}

	endpoints := login.DefaultEndpoints()

	cfg, err := login.OAuthConfig(p, login.Clients{}, &endpoints)
	if err != nil {
		return account.ID{}, nil, err
	}

	save := func(t *oauth2.Token) error { return registry.Save(id, t) }

	return id, &http.Client{
		Transport: &oauth2.Transport{
			Source: oauthflow.TokenSource(ctx, cfg, tok, save, logger),
			Base:   &httpretry.Transport{Logger: logger},
		},
		Timeout: 2 * time.Minute,
	}, nil
}

// seeder holds what every seeding step shares.
type seeder struct {
	gmail, outlook *http.Client
	// gAddr and oAddr are the two accounts' addresses; mail threads go
	// back and forth between them.
	gAddr, oAddr string
	// tag marks this run's items.
	tag    string
	rng    *rand.Rand
	now    time.Time
	logger *slog.Logger
	// mail is this run's conversations; those between the two accounts go
	// into both.
	mail []thread
}

// call sends a JSON request and decodes a JSON response into out, if not
// nil. body is sent as JSON unless it is a []byte.
func call(ctx context.Context, c *http.Client, method, url string, body, out any) error {
	var (
		reader      io.Reader = http.NoBody
		contentType           = "application/json"
	)

	switch b := body.(type) {
	case nil:
	case rawBody:
		reader, contentType = bytes.NewReader(b.data), b.contentType
	default:
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s: %w", method, url, err)
		}

		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, url, err)
	}

	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body.

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}

	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s %s: http %d: %s", method, url, resp.StatusCode, bytes.TrimSpace(data))
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, url, err)
		}
	}

	return nil
}

// rawBody is a request body sent as is.
type rawBody struct {
	data        []byte
	contentType string
}

// pick returns a random element.
func pick[T any](s *seeder, items []T) T {
	return items[s.rng.IntN(len(items))]
}

// chance is true with probability p.
func (s *seeder) chance(p float64) bool {
	return s.rng.Float64() < p
}

// step logs a finished step with its count and first error, and returns
// the error.
func (s *seeder) step(name string, n int, errs []error) error {
	err := errors.Join(errs...)
	if err != nil {
		s.logger.Error(name, slog.Int("created", n), slog.Int("failed", len(errs)), slog.String("first error", errs[0].Error()))
	} else {
		s.logger.Info(name, slog.Int("created", n))
	}

	return err
}
