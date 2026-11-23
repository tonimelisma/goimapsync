package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/atomicfile"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/googleapi"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/httpretry"
	"github.com/tonimelisma/pimvault/internal/login"
	"github.com/tonimelisma/pimvault/internal/oauthflow"
	"github.com/tonimelisma/pimvault/internal/state"
)

const (
	// responseHeaderTimeout bounds the wait for a provider to start
	// answering; bodies such as large messages may take longer.
	responseHeaderTimeout = 2 * time.Minute
	// tokenTimeout bounds a token refresh request.
	tokenTimeout = time.Minute
)

func (a *app) backupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup [account...]",
		Short: "Back up accounts to the archive",
		Long: "Bring the archive in line with each account: download new items, move\n" +
			"items that moved, and move the files of items deleted in the account to\n" +
			"<archive>/.deleted/<account>/<date>/, where they are kept 30 days. When\n" +
			"more than a fifth of a folder of 50 or more items was deleted, its files\n" +
			"are kept and the run exits 2 until --allow-deletions lets them go. With\n" +
			"no arguments every signed-in account is backed up. --dry-run writes\n" +
			"nothing and prints how many items each data type would download (for\n" +
			"Gmail mail also how many bytes), move and remove.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.locked(func() error { return a.backup(cmd, args) })
		},
	}
	cmd.Flags().BoolVar(&a.backupOpts.AllowDeletions, "allow-deletions", false,
		"delete the files of items deleted in the account even when many of a folder went at once")
	cmd.Flags().BoolVar(&a.backupOpts.DryRun, "dry-run", false,
		"write nothing; count what a backup would download, move and remove")

	return cmd
}

// backup backs up the selected accounts at the same time: each account has
// its own provider quota (F80), archive directory, state file and sign-in,
// and within an account items still go one at a time (F83). Results are
// printed in the accounts' order, each as soon as it and those before it
// are done. A failed account is reported and the others continue (D80).
func (a *app) backup(cmd *cobra.Command, queries []string) error {
	registry := account.NewRegistry(a.paths.ConfigDir)

	ids, err := a.selectAccounts(registry, queries)
	if err != nil {
		return err
	}

	if !a.backupOpts.DryRun {
		a.cleanTemp()

		if err := archive.PruneKeptAside(a.paths.Archive, a.now()); err != nil {
			a.logger.Warn(err.Error())
		}
	}

	results := make([]accountResult, len(ids))

	var (
		mu       sync.Mutex
		next     int // the first account whose result is not printed yet
		printErr error
		wg       sync.WaitGroup
	)

	for i, id := range ids {
		wg.Go(func() {
			parts, err := a.backupAccount(cmd.Context(), registry, id)
			err = withLoginHint(id, err)

			a.status.drop(id.String())

			if err != nil {
				a.logger.Error(err.Error(), slog.String("account", id.String()))
			}

			mu.Lock()
			defer mu.Unlock()

			results[i] = accountResult{id: id, parts: parts, err: err, done: true}

			for ; next < len(results) && results[next].done; next++ {
				r := &results[next]

				err := a.status.finish(r.id.String(), func() error {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), r.line(a.backupOpts.DryRun))

					return err //nolint:wrapcheck // wrapped once below.
				})
				if err != nil && printErr == nil {
					printErr = fmt.Errorf("print result: %w", err)
				}
			}
		})
	}

	wg.Wait()

	if printErr != nil {
		return printErr
	}

	return tally(results, a.backupOpts.DryRun)
}

// tally is the run's error from the accounts' results (backupResult).
func tally(results []accountResult, dryRun bool) error {
	var failed, needLogin, withheld int

	for _, r := range results {
		for _, p := range r.parts {
			withheld += p.res.Withheld
		}

		if r.err != nil {
			failed++

			if errors.Is(r.err, account.ErrNeedsLogin) {
				needLogin++
			}
		}
	}

	if dryRun {
		withheld = 0 // nothing was kept: nothing was run
	}

	return backupResult(len(results), failed, needLogin, withheld)
}

// accountResult is one account's backup, kept until it can be printed in
// the accounts' order.
type accountResult struct {
	id    account.ID
	parts []part
	err   error
	done  bool
}

// line is the account's summary line (D144).
func (r *accountResult) line(dryRun bool) string {
	sections := make([]string, 0, len(r.parts))
	for _, p := range r.parts {
		sections = append(sections, p.summary(dryRun))
	}

	return fmt.Sprintf("%s: %s", r.id, strings.Join(sections, "; "))
}

// backupResult is a backup run's error: the failed accounts, those that
// need a new sign-in and the deletions withheld (D80, D182), or nil.
func backupResult(accounts, failed, needLogin, withheld int) error {
	kept := ""
	if withheld > 0 {
		kept = fmt.Sprintf("kept the files of %d items deleted in the account, as more than a fifth of their "+
			"folders went at once; check the accounts, then run pimvault backup --allow-deletions", withheld)
	}

	switch {
	case failed == 0 && withheld > 0:
		return fmt.Errorf("back up: %s: %w", kept, errSkipped)
	case failed == 0:
		return nil
	case withheld > 0:
		kept = "; " + kept
	}

	if needLogin > 0 {
		return fmt.Errorf("back up: %d of %d accounts had failures%s, %d %w",
			failed, accounts, kept, needLogin, account.ErrNeedsLogin)
	}

	return fmt.Errorf("back up: %d of %d accounts had failures%s", failed, accounts, kept)
}

// withLoginHint adds the command that signs an account in again to an
// error that needs it (D80).
func withLoginHint(id account.ID, err error) error {
	if errors.Is(err, account.ErrNeedsLogin) {
		return fmt.Errorf("%w; run pimvault login %s and sign in as %s", err, id.Provider, id.Email)
	}

	return err
}

// configTempAge is how old a temp file in the config directory must be to
// be removed: login writes there without the run lock, and a token write
// takes far less.
const configTempAge = time.Hour

// cleanTemp removes temp files an interrupted run left in the state and
// config directories (D58); the archive's are removed per account.
func (a *app) cleanTemp() {
	for _, d := range []struct {
		path   string
		minAge time.Duration
	}{{a.paths.State, 0}, {a.paths.ConfigDir, configTempAge}} {
		if err := atomicfile.CleanDir(d.path, d.minAge); err != nil {
			a.logger.Warn(err.Error())
		}
	}
}

// selectAccounts returns the accounts the queries name, or all accounts.
func (a *app) selectAccounts(registry account.Registry, queries []string) ([]account.ID, error) {
	all, badNames, err := registry.IDs()
	if err != nil {
		return nil, err
	}

	if badNames != nil {
		a.logger.Warn(badNames.Error())
	}

	if len(all) == 0 {
		return nil, errors.New("no accounts; sign in with pimvault login <gmail|outlook>")
	}

	if len(queries) == 0 {
		return all, nil
	}

	var ids []account.ID

	seen := make(map[account.ID]bool)

	for _, q := range queries {
		id, err := account.Match(all, q)
		if err != nil {
			return nil, err
		}

		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	return ids, nil
}

// Data type names in the summary line.
const (
	partMail      = "mail"
	partCalendars = "calendars"
	partContacts  = "contacts"
	partTasks     = "tasks"
)

// dataTypes lists the data types backed up, in summary order.
func dataTypes() []string {
	return []string{partMail, partCalendars, partContacts, partTasks}
}

// part is the result of backing up one data type of an account.
type part struct {
	name string
	res  backup.Result
	err  error
}

// summary is the part's section of the account's summary line (D144).
func (p *part) summary(dryRun bool) string {
	if dryRun {
		return p.planned()
	}

	if p.err != nil {
		return fmt.Sprintf("%s failed, %d downloaded, %d moved, %d deleted, %d failed",
			p.name, p.res.Downloaded, p.res.Moved, p.res.Deleted, p.res.Failed)
	}

	return fmt.Sprintf("%s %d files, %d downloaded, %d moved, %d deleted, %d failed",
		p.name, p.res.Files, p.res.Downloaded, p.res.Moved, p.res.Deleted, p.res.Failed)
}

// planned is a dry run's section of the summary line (D183, D189).
func (p *part) planned() string {
	if p.err != nil {
		return p.name + " failed"
	}

	download := fmt.Sprintf("%d to download", p.res.Downloaded)

	switch {
	case p.name != partMail:
	case p.res.SizeKnown:
		download += fmt.Sprintf(" (%d bytes)", p.res.Bytes)
	default:
		download += " (size unknown)"
	}

	line := fmt.Sprintf("%s %s, %d to move, %d to remove", p.name, download, p.res.Moved, p.res.Deleted)
	if p.res.Withheld > 0 {
		line += fmt.Sprintf(", %d kept by the deletion guard", p.res.Withheld)
	}

	if p.res.Failed > 0 {
		line += fmt.Sprintf(", %d failed", p.res.Failed)
	}

	return line
}

// allFailed reports every data type as failed when nothing could run
// (D144).
func allFailed(err error) []part {
	parts := make([]part, 0, len(dataTypes()))
	for _, name := range dataTypes() {
		parts = append(parts, part{name: name, err: err})
	}

	return parts
}

// backupAccount backs up each data type of an account. A data type that
// fails is reported and the others continue. The returned error covers the
// whole account: its sign-in, its state, or any failed data type or item.
func (a *app) backupAccount(ctx context.Context, registry account.Registry, id account.ID) ([]part, error) {
	logger := a.logger.With(slog.String("account", id.String()))

	httpClient, err := a.accountClient(ctx, registry, id, logger)
	if err != nil {
		return allFailed(err), err
	}

	states := state.NewStore(a.paths.State)

	st, err := states.Load(id)
	if err != nil {
		logger.Warn(err.Error() + "; listing everything")
	}

	kinds, err := a.dataKinds(ctx, httpClient, id, logger)
	if err != nil {
		return allFailed(err), err
	}

	parts := make([]part, 0, len(kinds))

	for _, k := range kinds {
		res, err := k.run(&st)
		parts = append(parts, part{name: k.name, res: res, err: err})
	}

	var errs []error

	for _, p := range parts {
		if p.err != nil {
			errs = append(errs, fmt.Errorf("back up %s %s: %w", id, p.name, needsLogin(p.err)))
		} else if p.res.Failed > 0 {
			errs = append(errs, fmt.Errorf("back up %s %s: %d items failed", id, p.name, p.res.Failed))
		}
	}

	if a.backupOpts.DryRun {
		return parts, errors.Join(errs...)
	}

	if err := states.Save(id, &st); err != nil {
		errs = append(errs, fmt.Errorf("back up %s: %w", id, err))
	}

	return parts, errors.Join(errs...)
}

// accountClient returns an HTTP client authorized as the account, retrying
// throttled requests, including Google's rate-limit errors.
func (a *app) accountClient(ctx context.Context, registry account.Registry, id account.ID, logger *slog.Logger) (*http.Client, error) {
	var retryable func(*http.Response) bool
	if id.Provider == account.Gmail {
		retryable = googleapi.RateLimited
	}

	return a.apiClient(ctx, registry, id, logger, retryable)
}

// keep stores a new state when there is one.
func keep[T any](dst **T, next *T) {
	if next != nil {
		*dst = next
	}
}

// apiClient returns an HTTP client that authorizes requests with the
// account's token, saves refreshed tokens and retries throttled requests.
func (a *app) apiClient(
	ctx context.Context, registry account.Registry, id account.ID, logger *slog.Logger, retryable func(*http.Response) bool,
) (*http.Client, error) {
	tok, err := registry.Token(id)
	if err != nil {
		return nil, err
	}

	endpoints := a.endpoints()

	cfg, err := login.OAuthConfig(id.Provider, a.clients(), &endpoints)
	if err != nil {
		return nil, err
	}

	save := func(t *oauth2.Token) error { return registry.Save(id, t) }

	transport, err := plainTransport()
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: authorized(tokenSource(ctx, cfg, tok, save, logger), transport, retryable, logger),
	}, nil
}

// authorized retries outside the authorization, so every attempt asks the
// token source again: a retry that waited past the access token's expiry is
// sent with a refreshed token rather than the expired one, which the provider
// answers with 401. A refused refresh is not retried.
func authorized(
	source oauth2.TokenSource, base http.RoundTripper, retryable func(*http.Response) bool, logger *slog.Logger,
) *httpretry.Transport {
	return &httpretry.Transport{
		Base:      &oauth2.Transport{Source: source, Base: base},
		Retryable: retryable,
		Final:     refusedRefresh,
		Logger:    logger,
	}
}

// refusedRefresh reports whether the token endpoint answered a refresh with
// a refusal, such as invalid_grant for a revoked sign-in, which no retry
// fixes. Its timeouts, throttling and server errors are retried.
func refusedRefresh(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) || retrieveErr.Response == nil {
		return false
	}

	code := retrieveErr.Response.StatusCode

	return code < http.StatusInternalServerError && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests
}

// tokenSource refreshes a saved token when needed, with a bound on each
// refresh, and saves refreshed tokens.
func tokenSource(
	ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token, save func(*oauth2.Token) error, logger *slog.Logger,
) oauth2.TokenSource {
	refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: tokenTimeout})

	return oauthflow.TokenSource(refreshCtx, cfg, tok, save, logger)
}

// plainTransport is the default transport with a bound on the wait for
// response headers.
func plainTransport() (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("configure http: default transport has an unexpected type")
	}

	transport := base.Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout

	return transport, nil
}

// needsLogin marks errors from a revoked or expired sign-in: a refused token
// refresh, or a provider rejecting the access token.
func needsLogin(err error) error {
	var (
		retrieveErr *oauth2.RetrieveError
		gmailErr    *googleapi.APIError
		graphErr    *graph.APIError
	)

	if (errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant") ||
		(errors.As(err, &gmailErr) && gmailErr.Status == http.StatusUnauthorized) ||
		(errors.As(err, &graphErr) && graphErr.Status == http.StatusUnauthorized) {
		return fmt.Errorf("%w: %w", account.ErrNeedsLogin, err)
	}

	return err
}
