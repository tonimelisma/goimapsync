package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/gcal"
	"github.com/tonimelisma/pimvault/internal/gcal/gcaltest"
	"github.com/tonimelisma/pimvault/internal/gmail/gmailtest"
	"github.com/tonimelisma/pimvault/internal/gpeople"
	"github.com/tonimelisma/pimvault/internal/gpeople/gpeopletest"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
	"github.com/tonimelisma/pimvault/internal/gtasks"
	"github.com/tonimelisma/pimvault/internal/gtasks/gtaskstest"
	"github.com/tonimelisma/pimvault/internal/login"
	"github.com/tonimelisma/pimvault/internal/state"
)

// backupEnv is a test home with Google client settings and a fake Gmail.
func backupEnv(t *testing.T) (*testEnv, *gmailtest.Server) {
	t.Helper()

	env := newTestEnv(t)
	srv := gmailtest.New(t)
	env.apis.Gmail = srv.URL
	env.calendar = gcaltest.New(t)
	env.calendar.AddCalendar(gcal.Calendar{ID: "primary", Summary: "Primary", Primary: true}, "owner")
	env.apis.Calendar = env.calendar.URL
	env.contacts = gpeopletest.New(t)
	env.apis.People = env.contacts.URL
	env.tasks = gtaskstest.New(t)
	env.tasks.AddList(gtasks.List{ID: "default", Title: "My Tasks"})
	env.apis.Tasks = env.tasks.URL

	require.NoError(t, os.MkdirAll(env.configDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(env.configDir, "config.toml"),
		[]byte("google_client_id = \"id\"\ngoogle_client_secret = \"secret\"\n"), 0o600))

	return env, srv
}

// addSignedIn registers an account whose access token is still valid.
func (e *testEnv) addSignedIn(t *testing.T, id string) {
	t.Helper()

	parsed, err := account.Parse(id)
	require.NoError(t, err)
	require.NoError(t, account.NewRegistry(e.configDir).Save(parsed, &oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}))
}

func TestBackup_GmailAccount(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	srv.Add("m1", "one", "INBOX")
	srv.Add("m2", "two")

	code, stdout, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)
	assert.Equal(t, "gmail:user@gmail.example.com: mail 2 files, 2 downloaded, 0 moved, 0 deleted, 0 failed; calendars 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)

	mail := filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com", "mail")
	data, err := os.ReadFile(filepath.Join(mail, "Inbox", "m1.eml")) //nolint:gosec // G304: test path.
	require.NoError(t, err)
	assert.Equal(t, "one", string(data))
	assert.FileExists(t, filepath.Join(mail, "Archive", "m2.eml"))

	id, err := account.Parse("gmail:user@gmail.example.com")
	require.NoError(t, err)
	st, err := state.NewStore(filepath.Join(env.home, "state", "pimvault")).Load(id)
	require.NoError(t, err)
	require.NotNil(t, st.GmailMail)
	assert.NotEmpty(t, st.GmailMail.HistoryID)

	srv.Delete("m2")

	code, stdout, stderr = env.run(t, "backup", "user")
	require.Equal(t, ExitOK, code, stderr)
	assert.Equal(t, "gmail:user@gmail.example.com: mail 1 files, 0 downloaded, 0 moved, 1 deleted, 0 failed; calendars 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)
}

func TestBackup_NoAccounts(t *testing.T) {
	env, _ := backupEnv(t)

	code, stdout, stderr := env.run(t, "backup")
	assert.Equal(t, ExitError, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "no accounts; sign in with pimvault login")
}

func TestBackup_UnknownAccountQuery(t *testing.T) {
	env, _ := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")

	code, _, stderr := env.run(t, "backup", "nobody")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, "nobody")
}

func TestBackup_FailedItemExitsOne(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	srv.Add("m1", "one", "INBOX")
	srv.Add("m2", "two", "INBOX")
	srv.FailRaw["m2"] = http.StatusBadRequest

	code, stdout, stderr := env.run(t, "backup")
	assert.Equal(t, ExitError, code)
	assert.Equal(t, "gmail:user@gmail.example.com: mail 1 files, 1 downloaded, 0 moved, 0 deleted, 1 failed; calendars 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)
	assert.Contains(t, stderr, "m2")
	assert.Contains(t, stderr, "1 of 1 accounts had failures")
	assert.Contains(t, stderr, "mail: 1 items failed")
}

func TestBackup_OtherAccountsContinueAfterFailure(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	env.addSignedIn(t, "outlook:user@outlook.example.com")
	srv.Add("m1", "one", "INBOX")

	// Outlook answers with server errors.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BadRequest","message":"broken"}}`)) //nolint:errcheck // test server.
	}))
	t.Cleanup(broken.Close)
	env.apis.Graph = broken.URL

	code, stdout, stderr := env.run(t, "backup")
	assert.Equal(t, ExitError, code)
	assert.Equal(t, "gmail:user@gmail.example.com: mail 1 files, 1 downloaded, 0 moved, 0 deleted, 0 failed; calendars 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed\n"+
		"outlook:user@outlook.example.com: mail failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; "+
		"calendars failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks failed, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)
	assert.Contains(t, stderr, "broken")
}

func TestBackup_RevokedSignInNeedsLogin(t *testing.T) {
	env, _ := backupEnv(t)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)) //nolint:errcheck // test server.
	}))
	t.Cleanup(tokenSrv.Close)

	endpoints := login.DefaultEndpoints()
	endpoints.GoogleToken = tokenSrv.URL
	env.endpoints = &endpoints

	env.addAccount(t, "gmail:user@gmail.example.com") // refresh token only: the first request refreshes

	code, stdout, stderr := env.run(t, "backup")
	assert.Equal(t, ExitNeedsLogin, code, stderr)
	assert.Equal(t, "gmail:user@gmail.example.com: mail failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; "+
		"calendars failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks failed, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)
	assert.Contains(t, stderr, "run pimvault login gmail and sign in as user@gmail.example.com")
}

func TestBackup_RejectedAccessTokenNeedsLogin(t *testing.T) {
	env := newTestEnv(t)
	require.NoError(t, os.MkdirAll(env.configDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(env.configDir, "config.toml"),
		[]byte("google_client_id = \"id\"\ngoogle_client_secret = \"secret\"\n"), 0o600))

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"Invalid Credentials"}}`)) //nolint:errcheck // test server.
	}))
	t.Cleanup(api.Close)
	env.apis.Gmail = api.URL
	env.apis.Calendar = api.URL

	// The Outlook account fails for another reason.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(broken.Close)
	env.apis.Graph = broken.URL

	env.addSignedIn(t, "gmail:user@gmail.example.com")
	env.addSignedIn(t, "outlook:user@outlook.example.com")

	code, _, stderr := env.run(t, "backup")
	assert.Equal(t, ExitNeedsLogin, code, "one account needing login wins over other failures")
	assert.Contains(t, stderr, "2 of 2 accounts had failures, 1 sign-in needed")
}

func TestBackup_RemovesLeftoverTempFiles(t *testing.T) {
	env, _ := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")

	stateDir := filepath.Join(env.home, "state", "pimvault")
	require.NoError(t, os.MkdirAll(stateDir, 0o700))

	leftovers := []string{
		filepath.Join(stateDir, ".pimvault-1.tmp"),
		filepath.Join(env.configDir, ".pimvault-2.tmp"),
	}
	for _, path := range leftovers {
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}

	// login writes the config directory without the run lock: only an old
	// temp file there is a leftover.
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(leftovers[1], twoHoursAgo, twoHoursAgo))

	young := filepath.Join(env.configDir, ".pimvault-3.tmp")
	require.NoError(t, os.WriteFile(young, nil, 0o600))

	code, _, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)

	for _, path := range leftovers {
		assert.NoFileExists(t, path)
	}

	assert.FileExists(t, young)
}

func TestBackup_OutlookAccount(t *testing.T) {
	env := newTestEnv(t)
	srv := graphtest.New(t)
	env.apis.Graph = srv.URL

	require.NoError(t, os.MkdirAll(env.configDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(env.configDir, "config.toml"),
		[]byte("microsoft_client_id = \"id\"\n"), 0o600))
	env.addSignedIn(t, "outlook:user@outlook.example.com")

	srv.AddFolder("u-work", "Work", "")
	srv.Add("m1", "f-inbox", "one")
	srv.Add("m2", "u-work", "two")
	srv.Add("m3", "f-junk", "spam")

	code, stdout, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)
	assert.Equal(t, "outlook:user@outlook.example.com: mail 2 files, 2 downloaded, 0 moved, 0 deleted, 0 failed; "+
		"calendars 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)

	mail := filepath.Join(env.home, "data", "pimvault", "outlook_user@outlook.example.com", "mail")
	assert.FileExists(t, filepath.Join(mail, "Inbox", "m1.eml"))
	assert.FileExists(t, filepath.Join(mail, "Work", "m2.eml"))

	srv.Move("m1", "f-archive")

	code, stdout, stderr = env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)
	assert.Equal(t, "outlook:user@outlook.example.com: mail 2 files, 0 downloaded, 1 moved, 0 deleted, 0 failed; "+
		"calendars 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)
	assert.FileExists(t, filepath.Join(mail, "Archive", "m1.eml"))
}

func TestBackup_GmailCalendars(t *testing.T) {
	env, _ := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	env.calendar.Put("primary", gcal.Event{
		ID: "e1", ICalUID: "e1@google.com", Summary: "Dentist", Updated: "2026-01-01T00:00:00Z",
		Start: gcal.DateTime{DateTime: "2026-03-01T10:00:00Z"}, End: gcal.DateTime{DateTime: "2026-03-01T11:00:00Z"},
	})

	code, stdout, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)
	assert.Contains(t, stdout, "; calendars 1 files, 1 downloaded, 0 moved, 0 deleted, 0 failed;")
	assert.FileExists(t, filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com", "calendars", "Calendar", "e1.ics"))
}

func TestBackup_GmailContacts(t *testing.T) {
	env, _ := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")

	var ann gpeople.Person
	require.NoError(t, json.Unmarshal([]byte(`{"resourceName": "people/c1", "names": [{"displayName": "Ann"}]}`), &ann))
	env.contacts.Put(ann)

	code, stdout, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)
	assert.Contains(t, stdout, "; contacts 1 files, 1 downloaded, 0 moved, 0 deleted, 0 failed;")
	assert.FileExists(t, filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com", "contacts", "Contacts", "c1.vcf"))
}

func TestBackup_DeletionsAreKeptAsideAndMassDeletionIsWithheld(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	env.now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.Local) }

	for i := range 60 {
		srv.Add(fmt.Sprintf("m%02d", i), "Subject: x\r\n\r\nbody", "INBOX")
	}

	data := filepath.Join(env.home, "data", "pimvault")
	expired := filepath.Join(data, ".deleted", "gmail_user@gmail.example.com", "2026-09-01", "mail", "Inbox")
	require.NoError(t, os.MkdirAll(expired, 0o700))

	code, _, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)
	assert.NoDirExists(t, filepath.Dir(filepath.Dir(expired)), "kept 30 days, then deleted")

	for i := range 13 {
		srv.Delete(fmt.Sprintf("m%02d", i))
	}

	code, stdout, stderr := env.run(t, "backup")
	assert.Equal(t, ExitSkipped, code)
	assert.Contains(t, stdout, "mail 60 files, 0 downloaded, 0 moved, 0 deleted, 0 failed")
	assert.Contains(t, stderr, "more than a fifth of a folder was deleted in the account")
	assert.Contains(t, stderr, "kept the files of 13 items deleted in the account")
	assert.Contains(t, stderr, "pimvault backup --allow-deletions")

	code, stdout, stderr = env.run(t, "backup", "--allow-deletions")
	require.Equal(t, ExitOK, code, stderr)
	assert.Contains(t, stdout, "mail 47 files, 0 downloaded, 0 moved, 13 deleted, 0 failed")
	assert.FileExists(t, filepath.Join(data, ".deleted", "gmail_user@gmail.example.com", "2026-10-26", "mail", "Inbox", "m00.eml"))

	// Restore and verify pass the kept-aside files by.
	code, stdout, stderr = env.run(t, "verify")
	require.Equal(t, ExitOK, code, stderr)
	assert.Contains(t, stdout, "gmail:user@gmail.example.com")
}

func TestBackup_DryRunCountsAndWritesNothing(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	srv.Add("m1", "one", "INBOX")
	srv.Add("m2", "twelve", "SENT")

	code, stdout, stderr := env.run(t, "backup", "--dry-run")
	require.Equal(t, ExitOK, code, stderr)
	assert.Equal(t, "gmail:user@gmail.example.com: mail 2 to download (9 bytes), 0 to move, 0 to remove; "+
		"calendars 0 to download, 0 to move, 0 to remove; contacts 0 to download, 0 to move, 0 to remove; "+
		"tasks 0 to download, 0 to move, 0 to remove\n", stdout)
	assert.NoDirExists(t, filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com"))
	assert.NoFileExists(t, filepath.Join(env.home, "state", "pimvault", "gmail_user@gmail.example.com.json"))

	code, stdout, stderr = env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)
	assert.Contains(t, stdout, "mail 2 files, 2 downloaded")
}

// Accounts are backed up at the same time: Gmail's answers are held until
// Outlook has been asked, which one account after another never reaches,
// since Gmail comes first. Results still print in the accounts' order.
func TestBackup_AccountsRunAtTheSameTime(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	env.addSignedIn(t, "outlook:user@outlook.example.com")
	srv.Add("m1", "one", "INBOX")

	outlookAsked := make(chan struct{})

	var once sync.Once

	outlook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(outlookAsked) })
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BadRequest","message":"outlook"}}`)) //nolint:errcheck // test server.
	}))
	t.Cleanup(outlook.Close)
	env.apis.Graph = outlook.URL

	target, err := url.Parse(srv.URL)
	require.NoError(t, err)

	gmail := httputil.NewSingleHostReverseProxy(target)
	held := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-outlookAsked:
			gmail.ServeHTTP(w, r)
		case <-time.After(10 * time.Second):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"outlook never asked"}}`)) //nolint:errcheck // test server.
		}
	}))
	t.Cleanup(held.Close)
	env.apis.Gmail = held.URL

	code, stdout, stderr := env.run(t, "backup")
	assert.Equal(t, ExitError, code)
	assert.NotContains(t, stderr, "outlook never asked")
	assert.Equal(t, "gmail:user@gmail.example.com: mail 1 files, 1 downloaded, 0 moved, 0 deleted, 0 failed; calendars 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks 0 files, 0 downloaded, 0 moved, 0 deleted, 0 failed\n"+
		"outlook:user@outlook.example.com: mail failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; "+
		"calendars failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; contacts failed, 0 downloaded, 0 moved, 0 deleted, 0 failed; tasks failed, 0 downloaded, 0 moved, 0 deleted, 0 failed\n", stdout)
}
