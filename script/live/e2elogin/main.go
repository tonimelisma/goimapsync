// Command e2elogin signs a test account in and stores its tokens in Secret
// Manager (D158, D159), with the service account key in
// PIMVAULT_E2E_SERVICE_ACCOUNT:
//
//	go run ./script/live/e2elogin [-in FILE] [-account EMAIL] gmail|outlook
//
// It prints the sign-in URL, then waits for the address the browser was
// sent to. With -in it creates FILE as a named pipe and reads that address
// from it, so an agent can start it in the background and later write the
// address with `echo ADDRESS > FILE`; the same process keeps the sign-in's
// PKCE state. Without -in it reads the address from stdin.
//
// It refuses to store an account other than the one the secret already
// holds, so the owner's own account never becomes a test account. When the
// secret holds no token yet (first setup), -account names the test account
// to accept. It prints the provider only, never the account's email.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/e2e"
	"github.com/tonimelisma/pimvault/internal/login"
)

func main() {
	in := flag.String("in", "", "named pipe to create and read the redirect address from (default: stdin)")
	want := flag.String("account", "", "email of the test account to accept when the secret holds none yet")
	flag.Parse()

	if err := run(context.Background(), *in, *want, flag.Args()); err != nil {
		// Errors from the sign-in can name the account.
		fmt.Fprintln(os.Stderr, "e2elogin:", emailPattern.ReplaceAllString(err.Error(), "<account>"))
		os.Exit(1)
	}
}

// emailPattern finds email addresses in error messages.
var emailPattern = regexp.MustCompile(`[^\s"'<>:@]+@[^\s"'<>@]+`)

func run(ctx context.Context, in, want string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: e2elogin [-in FILE] [-account EMAIL] gmail|outlook")
	}

	provider, err := account.ParseProvider(args[0])
	if err != nil {
		return fmt.Errorf("parse provider: %w", err)
	}

	key := os.Getenv("PIMVAULT_E2E_SERVICE_ACCOUNT")
	if key == "" {
		return errors.New("PIMVAULT_E2E_SERVICE_ACCOUNT is not set")
	}

	store, err := e2e.ServiceAccount(ctx, []byte(key))
	if err != nil {
		return err
	}
	name := "e2e-" + string(provider) + "-token"

	expected, err := testAccount(ctx, store, name, provider, want)
	if err != nil {
		return err
	}

	readRedirect, cleanup, err := redirectReader(in)
	if err != nil {
		return err
	}
	defer cleanup()

	endpoints := login.DefaultEndpoints()

	res, err := login.Login(ctx, &login.Options{
		Provider:  provider,
		Endpoints: &endpoints,
		Prompts: login.Prompts{
			ShowSignInURL: func(url string) {
				fmt.Printf("Open this URL, sign in as the %s test account and allow access:\n\n%s\n\n"+
					"Then paste the 127.0.0.1 or localhost address the browser failed to load.\n", provider, url)
			},
			ReadRedirect: readRedirect,
		},
	})
	if err != nil {
		// A sign-in without its IMAP token is no use to the tests.
		return err
	}

	if res.ID != expected {
		return fmt.Errorf("the signed-in account is not the %s test account %s is for; nothing stored", provider, name)
	}

	if err := store.Add(ctx, name, &e2e.Secret{Account: expected.String(), Token: res.Token, IMAP: res.IMAP}); err != nil {
		return err
	}

	fmt.Printf("stored the %s test account's new tokens in %s\n", provider, name)

	return nil
}

// testAccount returns the account the secret is for: the one it holds, or
// on first setup, when it holds none, the one -account names.
func testAccount(ctx context.Context, store *e2e.Secrets, name string, provider account.Provider, want string) (account.ID, error) {
	current, err := store.Access(ctx, name)
	if err == nil {
		id, parseErr := account.Parse(current.Account)
		if parseErr != nil {
			return account.ID{}, fmt.Errorf("read secret %s: %w", name, parseErr)
		}

		if want != "" && !strings.EqualFold(want, id.Email) {
			return account.ID{}, fmt.Errorf("-account differs from the account %s holds", name)
		}

		return id, nil
	}

	if want == "" {
		return account.ID{}, fmt.Errorf("%w (on first setup, name the test account with -account)", err)
	}

	id, err := account.Parse(string(provider) + ":" + want)
	if err != nil {
		return account.ID{}, fmt.Errorf("parse -account: %w", err)
	}

	return id, nil
}

// redirectReader returns how the pasted address is read: from stdin, or from
// a named pipe it creates at path and removes in cleanup.
func redirectReader(path string) (read func() (string, error), cleanup func(), err error) {
	if path == "" {
		return readLine(os.Stdin), func() {}, nil
	}

	// A pipe left by a killed run, or a file written before the pipe
	// existed, holds nothing worth keeping.
	if fi, err := os.Lstat(path); err == nil && (fi.Mode().IsRegular() || fi.Mode()&os.ModeNamedPipe != 0) {
		if err := os.Remove(path); err != nil {
			return nil, nil, fmt.Errorf("remove stale %s: %w", path, err)
		}
	}

	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return nil, nil, fmt.Errorf("create named pipe %s: %w", path, err)
	}

	cleanup = func() { _ = os.Remove(path) } //nolint:errcheck // best effort; the pipe holds no data.

	// A stopped background sign-in leaves no pipe behind.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		cleanup()
		os.Exit(1)
	}()

	read = func() (string, error) {
		// Opening blocks until a writer opens the pipe.
		f, err := os.Open(path) //nolint:gosec // G304: the path is the operator's flag.
		if err != nil {
			return "", fmt.Errorf("open named pipe: %w", err)
		}
		defer f.Close() //nolint:errcheck // read-only.

		return readLine(f)()
	}

	return read, cleanup, nil
}

func readLine(r io.Reader) func() (string, error) {
	return func() (string, error) {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || line == "") {
			return "", fmt.Errorf("read redirect address: %w", err)
		}

		return strings.TrimSpace(line), nil
	}
}
