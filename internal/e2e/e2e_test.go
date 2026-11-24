//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/cli"
	"github.com/tonimelisma/pimvault/internal/e2e"
)

// The test accounts' secrets (script/live/e2elogin).
var secretNames = []string{"e2e-gmail-token", "e2e-outlook-token"} //nolint:gochecknoglobals // a fixed list.

// store returns the Secret Manager secrets (D158) the environment's
// credentials reach (e2e.FromEnv).
func store(t *testing.T) *e2e.Secrets {
	t.Helper()

	secrets, err := e2e.FromEnv(t.Context())
	if errors.Is(err, e2e.ErrNoCredentials) {
		t.Skip(err.Error())
	}

	require.NoError(t, err)

	return secrets
}

type harness struct {
	home     string
	registry account.Registry
	ids      []account.ID
}

// signIn puts the test accounts' tokens into a fresh config directory and
// stores tokens the run refreshed back when the test ends.
func signIn(t *testing.T, secrets *e2e.Secrets) *harness {
	t.Helper()

	h := &harness{home: t.TempDir()}
	h.registry = account.NewRegistry(filepath.Join(h.home, "config", "pimvault"))

	for _, name := range secretNames {
		s, err := secrets.Access(t.Context(), name)
		require.NoError(t, err)

		id, err := account.Parse(s.Account)
		require.NoError(t, err)
		require.NoError(t, h.registry.Save(id, s.Token))

		if s.IMAP != nil {
			require.NoError(t, h.registry.SaveIMAP(id, s.IMAP))
		}

		h.ids = append(h.ids, id)

		t.Cleanup(func() {
			tok, err := h.registry.Token(id)
			if err != nil || tok.RefreshToken == s.Token.RefreshToken {
				return
			}

			updated := *s
			updated.Token = tok

			if imapTok, err := h.registry.IMAPToken(id); err == nil {
				updated.IMAP = imapTok
			}

			assert.NoError(t, secrets.Add(context.WithoutCancel(t.Context()), name, &updated), "store refreshed token")
		})
	}

	return h
}

func (h *harness) run(t *testing.T, args ...string) (code int, stdout string) {
	t.Helper()

	code, stdout, _ = h.exec(t, append([]string{"--quiet"}, args...)...)

	return code, stdout
}

// exec runs pimvault with the arguments as given and returns its stderr too.
func (h *harness) exec(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	vars := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(h.home, "config"),
		"XDG_DATA_HOME":   filepath.Join(h.home, "data"),
		"XDG_STATE_HOME":  filepath.Join(h.home, "state"),
	}

	code = cli.Run(t.Context(), args, &cli.Runtime{
		Stdout: &out, Stderr: &errOut, Home: h.home,
		Env: func(key string) (string, bool) { v, ok := vars[key]; return v, ok },
	})

	if errOut.Len() > 0 {
		t.Logf("%s: %s", strings.Join(args, " "), errOut.String())
	}

	return code, out.String(), errOut.String()
}

// A fresh backup verifies clean and restores into each account as a no-op:
// backup, verify and restore's duplicate checks agree with the providers.
func TestLive_BackupVerifyRestore(t *testing.T) {
	h := signIn(t, store(t))

	code, out := h.run(t, "backup")
	require.Equal(t, cli.ExitOK, code, out)

	code, out = h.run(t, "verify")
	assert.Equal(t, cli.ExitOK, code, out)

	// Straight after a backup, a dry run finds nothing to do; reading the
	// change tokens in it leaves them usable for the next backup.
	code, out = h.run(t, "backup", "--dry-run")
	require.Equal(t, cli.ExitOK, code, out)

	for _, id := range h.ids {
		size := "(0 bytes)"
		if id.Provider == account.Outlook {
			size = "(size unknown)"
		}

		assert.Contains(t, out, id.String()+": mail 0 to download "+size+", 0 to move, 0 to remove;", out)
	}

	// The backup after it continues from the change tokens the dry run
	// read: nothing is listed in full again.
	code, out, log := h.exec(t, "backup")
	require.Equal(t, cli.ExitOK, code, out)
	assert.NotContains(t, out, "failed,", out)
	assert.NotContains(t, log, "listing", "a full listing after the dry run")

	for _, id := range h.ids {
		code, out = h.run(t, "restore", "--dry-run", id.String())
		require.Equal(t, cli.ExitOK, code, out)

		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.HasPrefix(line, id.String()+" ") {
				assert.Contains(t, line, ": 0 to write", "nothing to restore into the account it came from")
			}
		}
	}
}
