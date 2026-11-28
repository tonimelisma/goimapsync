package cli

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
	"github.com/tonimelisma/pimvault/internal/login"
	"github.com/tonimelisma/pimvault/internal/outlookimap/outlookimaptest"
)

// Validates: PV-150, PV-172, PV-224
func TestRestore_GmailMailRoundTrip(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	srv.Add("m1", "Message-ID: <m1@x>\r\nDate: Mon, 02 Mar 2026 10:00:00 +0200\r\nFrom: ann@example.com\r\nSubject: one\r\n\r\nhi\r\n", "INBOX")

	code, _, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)

	// A message only the archive has: restore writes it, and leaves the
	// one the account has alone.
	inbox := filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com", "mail", "Inbox")
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "old.eml"),
		[]byte("Message-ID: <old@x>\r\nDate: Sun, 01 Mar 2026 10:00:00 +0200\r\nFrom: bob@example.com\r\nSubject: old\r\n\r\nhi\r\n"), 0o600))

	code, stdout, stderr := env.run(t, "restore", "--dry-run", "gmail:user@gmail.example.com")
	require.Equal(t, ExitOK, code, stderr)
	assert.Equal(t, "already there mail/Inbox/m1\nwould write mail/Inbox/old\n"+
		"gmail:user@gmail.example.com mail: 1 to write, 1 already there, 0 skipped, 0 failed\n"+
		"gmail:user@gmail.example.com calendars: 0 to write, 0 already there, 0 skipped, 0 failed\n"+
		"gmail:user@gmail.example.com contacts: 0 to write, 0 already there, 0 skipped, 0 failed\n"+
		"gmail:user@gmail.example.com tasks: 0 to write, 0 already there, 0 skipped, 0 failed\n", stdout)
	assert.Len(t, srv.Messages(), 1, "a dry run writes nothing")

	code, stdout, stderr = env.run(t, "restore", "gmail:user@gmail.example.com")
	require.Equal(t, ExitOK, code, stderr)
	assert.Contains(t, stdout, "mail: 1 written, 1 already there, 0 skipped, 0 failed")
	assert.Len(t, srv.Messages(), 2)
}

// Validates: PV-151
func TestMigrate_RefusesSameAccount(t *testing.T) {
	env, _ := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	require.NoError(t, os.MkdirAll(filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com"), 0o700))

	code, _, stderr := env.run(t, "migrate", "gmail:user@gmail.example.com", "gmail:user@gmail.example.com")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, "use restore")
}

func TestMigrate_RefusesMissingArchive(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")

	code, _, stderr := env.run(t, "migrate", "outlook:typo@outlook.example.com", "gmail:user@gmail.example.com")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, "run backup first")
	assert.Empty(t, srv.Messages())
}

// Validates: PV-151
func TestMigrate_FromMatchesArchivedAccounts(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	require.NoError(t, os.MkdirAll(filepath.Join(env.home, "data", "pimvault", "gmail_ann@gmail.example.com"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(env.home, "data", "pimvault", "gmail_bob@gmail.example.com"), 0o700))

	code, _, stderr := env.run(t, "migrate", "gmail", "user")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, "gmail:ann@gmail.example.com, gmail:bob@gmail.example.com", "an ambiguous <from> lists the archived accounts")

	code, _, stderr = env.run(t, "migrate", "carol", "user")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, "run backup first")
	assert.Empty(t, srv.Messages())
}

// Validates: PV-151, PV-163, PV-185
func TestMigrate_GmailMailIntoOutlookOverIMAP(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	env.addSignedIn(t, "outlook:user@outlook.example.com")
	srv.Add("m1", "Message-ID: <m1@x>\r\nDate: Mon, 02 Mar 2026 10:00:00 +0200\r\nFrom: ann@example.com\r\nSubject: one\r\n\r\nhi\r\n", "INBOX")

	graphSrv := graphtest.New(t)
	env.apis.Graph = graphSrv.URL
	mailbox := outlookimaptest.New(t, "user@outlook.example.com", "imap-access")
	env.dialIMAP = func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", mailbox.Address)
	}

	code, _, stderr := env.run(t, "backup", "gmail:user@gmail.example.com")
	require.Equal(t, ExitOK, code, stderr)

	// Without the IMAP sign-in, mail cannot be written: sign in again.
	// Both accounts may be named by a unique part (D122).
	code, stdout, _ := env.run(t, "migrate", "gmail", "user@outlook")
	assert.Equal(t, ExitNeedsLogin, code)
	assert.Contains(t, stdout, "outlook:user@outlook.example.com mail: 0 written, 0 already there, 0 skipped, 1 failed, then stopped")

	id, err := account.Parse("outlook:user@outlook.example.com")
	require.NoError(t, err)

	// A token the IMAP server refuses: sign in again.
	require.NoError(t, account.NewRegistry(env.configDir).SaveIMAP(id, &oauth2.Token{
		AccessToken: "revoked", RefreshToken: "imap-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour),
	}))

	code, _, stderr = env.run(t, "migrate", "gmail:user@gmail.example.com", "outlook:user@outlook.example.com")
	assert.Equal(t, ExitNeedsLogin, code)
	assert.Contains(t, stderr, "run pimvault login outlook and sign in as user@outlook.example.com")

	// The account's IMAP setting is off: say so, not "sign in again" (D175).
	require.NoError(t, account.NewRegistry(env.configDir).SaveIMAP(id, &oauth2.Token{
		AccessToken: "imap-access", RefreshToken: "imap-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour),
	}))
	mailbox.SetIMAPOff(true)

	code, _, stderr = env.run(t, "migrate", "gmail:user@gmail.example.com", "outlook:user@outlook.example.com")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, `Let devices and apps use IMAP`)
	assert.NotContains(t, stderr, "sign-in needed")

	mailbox.SetIMAPOff(false)

	// An expired token is refreshed for the IMAP scope and saved.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "imap-refresh", r.FormValue("refresh_token"))
		assert.Equal(t, "offline_access "+login.IMAPScope, r.FormValue("scope"))
		w.Header().Set("Content-Type", "application/json")
		_, writeErr := w.Write([]byte(`{"access_token":"imap-access","refresh_token":"imap-refresh-2","token_type":"Bearer","expires_in":3600}`))
		assert.NoError(t, writeErr)
	}))
	t.Cleanup(tokenSrv.Close)

	env.endpoints = &login.Endpoints{MicrosoftToken: tokenSrv.URL}

	require.NoError(t, account.NewRegistry(env.configDir).SaveIMAP(id, &oauth2.Token{
		AccessToken: "stale", RefreshToken: "imap-refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Hour),
	}))

	code, stdout, stderr = env.run(t, "migrate", "gmail:user@gmail.example.com", "outlook:user@outlook.example.com")
	require.Equal(t, ExitOK, code, stderr)

	saved, err := account.NewRegistry(env.configDir).IMAPToken(id)
	require.NoError(t, err)
	assert.Equal(t, "imap-refresh-2", saved.RefreshToken)
	assert.Contains(t, stdout, "outlook:user@outlook.example.com mail: 1 written, 0 already there, 0 skipped, 0 failed")

	appended := mailbox.Appended()
	require.Len(t, appended, 1)
	assert.Equal(t, "INBOX", appended[0].Folder)
}
