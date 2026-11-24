// Command tokens signs pimvault in as the throwaway test accounts for live
// probes: it reads their tokens from Secret Manager (D158), with the
// credentials e2e.FromEnv reads (the service account key in a cloud session,
// a Workload Identity access token in GitHub Actions), into a fresh
// XDG_CONFIG_HOME, and prints the account IDs, never the tokens.
//
//	go run ./script/live/tokens -out DIR
//
// Then run pimvault with XDG_CONFIG_HOME=DIR. Tokens a probe refreshes are
// not stored back; the end-to-end test does that.
//
//	go run ./script/live/tokens -mask
//
// writes nothing and prints a GitHub Actions ::add-mask:: command for each
// test account's email (as stored, lowercased and URL-escaped), its local
// part and its tokens, so a workflow log never shows them (D193). Tokens the
// run refreshes later are not masked; pimvault never prints tokens.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/e2e"
)

// secrets are the test accounts' secrets (script/live/e2elogin).
var secrets = []string{"e2e-gmail-token", "e2e-outlook-token"}

func main() {
	out := flag.String("out", "", "directory to use as XDG_CONFIG_HOME")
	mask := flag.Bool("mask", false, "print GitHub Actions mask commands for the accounts instead")
	flag.Parse()

	if err := run(context.Background(), *out, *mask); err != nil {
		fmt.Fprintln(os.Stderr, "tokens:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out string, mask bool) error {
	if (out == "") == !mask {
		return fmt.Errorf("usage: tokens -out DIR | tokens -mask")
	}

	store, err := e2e.FromEnv(ctx)
	if err != nil {
		return err
	}
	registry := account.NewRegistry(filepath.Join(out, "pimvault"))

	for _, name := range secrets {
		s, err := store.Access(ctx, name)
		if err != nil {
			return err
		}

		id, err := account.Parse(s.Account)
		if err != nil {
			return fmt.Errorf("read secret %s: %w", name, err)
		}

		if mask {
			printMasks(id, s)

			continue
		}

		if err := registry.Save(id, s.Token); err != nil {
			return err
		}

		if s.IMAP != nil {
			if err := registry.SaveIMAP(id, s.IMAP); err != nil {
				return err
			}
		}

		fmt.Println(id.Provider)
	}

	return nil
}

// printMasks prints the mask commands for one test account. Masks match
// exact text, so each spelling of the address is masked.
func printMasks(id account.ID, s *e2e.Secret) {
	var values []string

	for _, email := range []string{id.Email, strings.TrimPrefix(s.Account, string(id.Provider)+":")} {
		local, _, _ := strings.Cut(email, "@")
		values = append(values, email, local, url.QueryEscape(email), url.PathEscape(email))
	}

	for _, tok := range []*oauth2.Token{s.Token, s.IMAP} {
		if tok != nil {
			values = append(values, tok.AccessToken, tok.RefreshToken)
		}
	}

	for _, v := range values {
		if v != "" {
			fmt.Printf("::add-mask::%s\n", v)
		}
	}
}
