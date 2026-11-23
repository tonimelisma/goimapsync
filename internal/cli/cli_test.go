package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/config"
	"github.com/tonimelisma/pimvault/internal/gcal/gcaltest"
	"github.com/tonimelisma/pimvault/internal/gpeople/gpeopletest"
	"github.com/tonimelisma/pimvault/internal/gtasks/gtaskstest"
	"github.com/tonimelisma/pimvault/internal/login"
	"github.com/tonimelisma/pimvault/internal/state"
	"github.com/tonimelisma/pimvault/internal/tokenfile"
)

// testEnv is an isolated home with XDG variables pointing inside it.
type testEnv struct {
	home      string
	configDir string
	vars      map[string]string
	// apis and endpoints point the CLI at fake providers.
	apis      APIs
	endpoints *login.Endpoints
	calendar  *gcaltest.Server
	contacts  *gpeopletest.Server
	tasks     *gtaskstest.Server
	dialIMAP  func(ctx context.Context) (net.Conn, error)
	// now is the clock; nil means the real one.
	now func() time.Time
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	home := t.TempDir()
	vars := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(home, "config"),
		"XDG_DATA_HOME":   filepath.Join(home, "data"),
		"XDG_STATE_HOME":  filepath.Join(home, "state"),
	}

	return &testEnv{home: home, configDir: filepath.Join(home, "config", "pimvault"), vars: vars}
}

func (e *testEnv) addAccount(t *testing.T, id string) {
	t.Helper()

	parsed, err := account.Parse(id)
	require.NoError(t, err)
	path := account.NewRegistry(e.configDir).TokenPath(parsed)
	require.NoError(t, tokenfile.Save(path, &oauth2.Token{RefreshToken: "refresh"}))
}

func (e *testEnv) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = Run(context.Background(), args, &Runtime{
		Stdout: &out,
		Stderr: &errOut,
		Env: func(key string) (string, bool) {
			value, ok := e.vars[key]

			return value, ok
		},
		Home:           e.home,
		APIs:           e.apis,
		LoginEndpoints: e.endpoints,
		DialIMAP:       e.dialIMAP,
		Now:            e.now,
	})

	return code, out.String(), errOut.String()
}

func TestAccountList_Empty(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := newTestEnv(t).run(t, "account", "list")
	assert.Equal(t, ExitOK, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestAccountList_PrintsSortedIDs(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.addAccount(t, "outlook:user@outlook.example.com")
	env.addAccount(t, "gmail:user@gmail.example.com")

	code, stdout, _ := env.run(t, "account", "list")
	assert.Equal(t, ExitOK, code)
	assert.Equal(t, "gmail:user@gmail.example.com\noutlook:user@outlook.example.com\n", stdout)
}

func TestAccountList_BrokenTokenFileFailsButListsValidOnes(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.addAccount(t, "gmail:good@gmail.example.com")
	broken := filepath.Join(env.configDir, "token_gmail_broken@gmail.example.com.json")
	require.NoError(t, os.WriteFile(broken, []byte("{not json"), 0o600))

	code, stdout, stderr := env.run(t, "account", "list")
	assert.Equal(t, ExitNeedsLogin, code)
	assert.Equal(t, "gmail:good@gmail.example.com\n", stdout)
	assert.Contains(t, stderr, "gmail:broken@gmail.example.com")
	assert.Contains(t, stderr, "sign-in needed")
}

func TestAccountRemove_BySubstring(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.addAccount(t, "gmail:user@gmail.example.com")
	env.addAccount(t, "outlook:user@outlook.example.com")

	code, stdout, _ := env.run(t, "account", "remove", "outlook")
	assert.Equal(t, ExitOK, code)
	assert.Equal(t, "removed outlook:user@outlook.example.com\n", stdout)

	_, listed, _ := env.run(t, "account", "list")
	assert.Equal(t, "gmail:user@gmail.example.com\n", listed)
}

func TestAccountRemove_BrokenTokenFileCanBeRemoved(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	require.NoError(t, os.MkdirAll(env.configDir, 0o700))
	broken := filepath.Join(env.configDir, "token_gmail_broken@gmail.example.com.json")
	require.NoError(t, os.WriteFile(broken, []byte("{not json"), 0o600))

	code, stdout, _ := env.run(t, "account", "remove", "broken")
	assert.Equal(t, ExitOK, code)
	assert.Equal(t, "removed gmail:broken@gmail.example.com\n", stdout)
	assert.NoFileExists(t, broken)
}

func TestAccountRemove_AmbiguousListsCandidates(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.addAccount(t, "gmail:user@gmail.example.com")
	env.addAccount(t, "outlook:user@outlook.example.com")

	code, _, stderr := env.run(t, "account", "remove", "user")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, "ambiguous account")
	assert.Contains(t, stderr, "gmail:user@gmail.example.com, outlook:user@outlook.example.com")
}

func TestAccountRemove_KeepsArchiveAndState(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.addAccount(t, "gmail:user@gmail.example.com")
	archived := filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com", "mail", "Inbox", "1.eml")
	state := filepath.Join(env.home, "state", "pimvault", "gmail_user@gmail.example.com.json")

	for _, path := range []string{archived, state} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	}

	code, _, _ := env.run(t, "account", "remove", "gmail:user@gmail.example.com")
	assert.Equal(t, ExitOK, code)
	assert.FileExists(t, archived)
	assert.FileExists(t, state)
}

func TestConfigFlagMovesTokenDirectory(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	otherConfigDir := filepath.Join(env.home, "elsewhere")
	parsed, err := account.Parse("gmail:user@gmail.example.com")
	require.NoError(t, err)
	require.NoError(t, tokenfile.Save(account.NewRegistry(otherConfigDir).TokenPath(parsed), &oauth2.Token{RefreshToken: "r"}))
	require.NoError(t, os.WriteFile(filepath.Join(otherConfigDir, "config.toml"), nil, 0o600))

	code, stdout, _ := env.run(t, "--config", filepath.Join(otherConfigDir, "config.toml"), "account", "list")
	assert.Equal(t, ExitOK, code)
	assert.Equal(t, "gmail:user@gmail.example.com\n", stdout)
}

func TestMissingExplicitConfigFails(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	code, stdout, stderr := env.run(t, "--config", filepath.Join(env.home, "confg.toml"), "account", "list")
	assert.Equal(t, ExitError, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "confg.toml")
}

func TestUnknownConfigKeyFails(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	require.NoError(t, os.MkdirAll(env.configDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(env.configDir, "config.toml"), []byte("archive_dir = \"/x\"\n"), 0o600))

	code, _, stderr := env.run(t, "account", "list")
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr, "unknown keys: archive_dir")
}

func TestGlobalFlagValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "verbose and quiet", args: []string{"-v", "-q", "account", "list"}, want: "cannot be combined"},
		{name: "bad log format", args: []string{"--log-format", "xml", "account", "list"}, want: "want text or json"},
		{name: "unknown command", args: []string{"frobnicate"}, want: "unknown command"},
		{name: "remove needs an account", args: []string{"account", "remove"}, want: "accepts 1 arg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, _, stderr := newTestEnv(t).run(t, tt.args...)
			assert.Equal(t, ExitError, code)
			assert.Contains(t, stderr, tt.want)
		})
	}
}

func TestJSONLogFormat(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	code, _, stderr := env.run(t, "--log-format", "json", "-v", "account", "remove", "nobody")
	assert.Equal(t, ExitError, code)

	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	require.NotEmpty(t, lines)

	for _, line := range lines {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record), line)
		assert.Contains(t, record, "level")
	}

	assert.Contains(t, stderr, `"archive":"`+filepath.Join(env.home, "data", "pimvault")+`"`)
	assert.Contains(t, stderr, "no matching account")
}

func TestEnvironmentOverridesArchive(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.vars[config.EnvArchive] = "/srv/archive"

	_, _, stderr := env.run(t, "-v", "account", "list")
	assert.Contains(t, stderr, "archive=/srv/archive")
}

// lazyReader produces its content on first read, so a test can answer a
// prompt that was printed during the same run.
type lazyReader struct {
	produce func() string
	r       *strings.Reader
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if l.r == nil {
		l.r = strings.NewReader(l.produce())
	}

	return l.r.Read(p) //nolint:wrapcheck // test reader.
}

func TestLogin_GmailRegistersAccount(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/token":
			_, err := w.Write([]byte(`{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600}`))
			assert.NoError(t, err)
		case "/userinfo":
			_, err := w.Write([]byte(`{"email":"user@gmail.example.com"}`))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	require.NoError(t, os.MkdirAll(env.configDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(env.configDir, "config.toml"),
		[]byte("google_client_id = \"gid\"\ngoogle_client_secret = \"gsecret\"\n"), 0o600))

	var stderr bytes.Buffer

	stdin := &lazyReader{produce: func() string {
		match := regexp.MustCompile(`state=([0-9a-f]+)`).FindStringSubmatch(stderr.String())
		require.Len(t, match, 2, stderr.String())

		return "http://127.0.0.1:53682/?code=c&state=" + match[1] + "\n"
	}}

	var stdout bytes.Buffer

	code := Run(context.Background(), []string{"login", "gmail"}, &Runtime{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr, Home: env.home,
		Env: func(key string) (string, bool) { v, ok := env.vars[key]; return v, ok },
		LoginEndpoints: &login.Endpoints{
			GoogleAuth: server.URL + "/auth", GoogleToken: server.URL + "/token", GoogleUserinfo: server.URL + "/userinfo",
		},
	})
	require.Equal(t, ExitOK, code, stderr.String())
	assert.Equal(t, "signed in as gmail:user@gmail.example.com\n", stdout.String())
	assert.Contains(t, stderr.String(), "client_id=gid")

	_, listed, _ := env.run(t, "account", "list")
	assert.Equal(t, "gmail:user@gmail.example.com\n", listed)
}

func TestLogin_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown provider", args: []string{"login", "yahoo"}, want: "unknown provider"},
		{name: "no provider", args: []string{"login"}, want: "accepts 1 arg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, _, stderr := newTestEnv(t).run(t, tt.args...)
			assert.Equal(t, ExitError, code)
			assert.Contains(t, stderr, tt.want)
		})
	}
}

// redirectReader answers each paste-URL prompt with a redirect carrying the
// state of the latest sign-in URL printed.
type redirectReader struct {
	stderr *bytes.Buffer
	r      *strings.Reader
}

func (l *redirectReader) Read(p []byte) (int, error) {
	if l.r == nil || l.r.Len() == 0 {
		states := regexp.MustCompile(`state=([0-9a-f]+)`).FindAllStringSubmatch(l.stderr.String(), -1)
		if len(states) == 0 {
			return 0, io.EOF
		}

		l.r = strings.NewReader("http://localhost:53682/?code=c&state=" + states[len(states)-1][1] + "\n")
	}

	return l.r.Read(p) //nolint:wrapcheck // test reader.
}

func TestLogin_OutlookOneSignInAllowsIMAPAndRemoveForgetsBoth(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)

	var scopes []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/token":
			_, err := w.Write([]byte(`{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600}`))
			assert.NoError(t, err)
		case "/me":
			_, err := w.Write([]byte(`{"userPrincipalName":"user@outlook.example.com"}`))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stderr, stdout bytes.Buffer

	code := Run(context.Background(), []string{"login", "outlook"}, &Runtime{
		Stdin: &redirectReader{stderr: &stderr}, Stdout: &stdout, Stderr: &stderr, Home: env.home,
		Env: func(key string) (string, bool) { v, ok := env.vars[key]; return v, ok },
		LoginEndpoints: &login.Endpoints{
			MicrosoftAuth: server.URL + "/auth", MicrosoftToken: server.URL + "/token", GraphMe: server.URL + "/me",
		},
	})
	require.Equal(t, ExitOK, code, stderr.String())
	assert.Equal(t, "signed in as outlook:user@outlook.example.com\n", stdout.String())

	for _, m := range regexp.MustCompile(`scope=([^&\s]+)`).FindAllStringSubmatch(stderr.String(), -1) {
		scopes = append(scopes, m[1])
	}

	require.Len(t, scopes, 1, "one sign-in (D161)")
	assert.Contains(t, scopes[0], "IMAP.AccessAsUser.All")
	assert.FileExists(t, filepath.Join(env.configDir, "imap_outlook_user@outlook.example.com.json"))

	code, _, _ = env.run(t, "account", "remove", "outlook:user@outlook.example.com")
	require.Equal(t, ExitOK, code)
	assert.NoFileExists(t, filepath.Join(env.configDir, "imap_outlook_user@outlook.example.com.json"))
	assert.NoFileExists(t, filepath.Join(env.configDir, "token_outlook_user@outlook.example.com.json"))
}

func TestLogin_OutlookWithoutIMAPKeepsTheAccount(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/token" && r.FormValue("grant_type") == "refresh_token":
			w.WriteHeader(http.StatusBadRequest)
			_, err := w.Write([]byte(`{"error":"invalid_grant"}`))
			assert.NoError(t, err)
		case r.URL.Path == "/token":
			_, err := w.Write([]byte(`{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600}`))
			assert.NoError(t, err)
		case r.URL.Path == "/me":
			_, err := w.Write([]byte(`{"userPrincipalName":"user@outlook.example.com"}`))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stderr, stdout bytes.Buffer

	code := Run(context.Background(), []string{"login", "outlook"}, &Runtime{
		Stdin: &redirectReader{stderr: &stderr}, Stdout: &stdout, Stderr: &stderr, Home: env.home,
		Env: func(key string) (string, bool) { v, ok := env.vars[key]; return v, ok },
		LoginEndpoints: &login.Endpoints{
			MicrosoftAuth: server.URL + "/auth", MicrosoftToken: server.URL + "/token", GraphMe: server.URL + "/me",
		},
	})
	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr.String(), "restore and migrate of mail into it need login again")
	assert.FileExists(t, filepath.Join(env.configDir, "token_outlook_user@outlook.example.com.json"))
	assert.NoFileExists(t, filepath.Join(env.configDir, "imap_outlook_user@outlook.example.com.json"))
}

func TestRun_SecondRunIsRefusedWhileOneHoldsTheLock(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.addAccount(t, "gmail:user@gmail.example.com")

	unlock, err := state.Lock(filepath.Join(env.home, "state", "pimvault"))
	require.NoError(t, err)

	t.Cleanup(func() { assert.NoError(t, unlock()) })

	for _, args := range [][]string{
		{"backup"},
		{"verify"},
		{"restore", "gmail:user@gmail.example.com"},
		{"migrate", "x", "gmail:user@gmail.example.com"},
		{"account", "remove", "gmail:user@gmail.example.com"},
	} {
		code, _, stderr := env.run(t, args...)
		assert.Equal(t, ExitError, code, args)
		assert.Contains(t, stderr, "another pimvault run is using the state directory", args)
		assert.Contains(t, stderr, "process ", args)
	}

	// Listing accounts needs no lock; the account was not removed.
	code, stdout, _ := env.run(t, "account", "list")
	assert.Equal(t, ExitOK, code)
	assert.Equal(t, "gmail:user@gmail.example.com\n", stdout)
}

func TestRun_VersionPrintsTheBuildVersion(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	code := Run(context.Background(), []string{"--version"}, &Runtime{
		Stdout: &out, Stderr: &bytes.Buffer{}, Env: func(string) (string, bool) { return "", false },
		Home: t.TempDir(), Version: "v1.2.3",
	})
	assert.Equal(t, ExitOK, code)
	assert.Equal(t, "pimvault v1.2.3\n", out.String())
}
