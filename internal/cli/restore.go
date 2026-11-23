package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gcal"
	"github.com/tonimelisma/pimvault/internal/gmail"
	"github.com/tonimelisma/pimvault/internal/gpeople"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/gtasks"
	"github.com/tonimelisma/pimvault/internal/login"
	"github.com/tonimelisma/pimvault/internal/oauthflow"
	"github.com/tonimelisma/pimvault/internal/outlookimap"
	"github.com/tonimelisma/pimvault/internal/restore"
)

// errSkipped marks a run that finished but left items out (exit 2, D93).
var errSkipped = errors.New("items were skipped")

func (a *app) restoreCommand() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "restore <account>",
		Short: "Write an account's archive back into the account",
		Long: "Write every archived item the account does not have yet back into it.\n" +
			"Items the account already has are left alone; running it again resumes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.locked(func() error { return a.restore(cmd, args[0], dryRun) })
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "write nothing; list what would be written")

	return cmd
}

func (a *app) migrateCommand() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "migrate <from> <to>",
		Short: "Write one account's archive into another account",
		Long: "Write every item in the archive of <from> that <to> does not have yet into\n" +
			"<to>. <from> names an archived account, <to> a signed-in one; either may be\n" +
			"a unique part of the name. Running it again resumes.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.locked(func() error { return a.migrate(cmd, args[0], args[1], dryRun) })
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "write nothing; list what would be written")

	return cmd
}

// restore writes the archive of the account query names back into it.
func (a *app) restore(cmd *cobra.Command, query string, dryRun bool) error {
	registry := account.NewRegistry(a.paths.ConfigDir)

	target, err := a.matchAccount(registry, query)
	if err != nil {
		return err
	}

	return withLoginHint(target, a.restoreInto(cmd, registry, target, target, dryRun))
}

// migrate writes the archive of the account fromQuery names into the
// account toQuery names.
func (a *app) migrate(cmd *cobra.Command, fromQuery, toQuery string, dryRun bool) error {
	registry := account.NewRegistry(a.paths.ConfigDir)

	archived, err := archive.Accounts(a.paths.Archive)
	if err != nil {
		return err
	}

	from, err := account.Match(archived, fromQuery)
	if errors.Is(err, account.ErrNoMatch) {
		return fmt.Errorf("migrate: %w in the archive (run backup first)", err)
	}

	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	target, err := a.matchAccount(registry, toQuery)
	if err != nil {
		return err
	}

	if from == target {
		return errors.New("migrate: <from> and <to> are the same account; use restore")
	}

	return withLoginHint(target, a.restoreInto(cmd, registry, from, target, dryRun))
}

// matchAccount finds one signed-in account.
func (a *app) matchAccount(registry account.Registry, query string) (account.ID, error) {
	ids, err := a.selectAccounts(registry, []string{query})
	if err != nil {
		return account.ID{}, err
	}

	return ids[0], nil
}

// restoreInto writes the archive of from into target, one data type after
// another, and prints one summary line per data type (D155).
func (a *app) restoreInto(cmd *cobra.Command, registry account.Registry, from, target account.ID, dryRun bool) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	logger := a.logger.With(slog.String("account", target.String()))

	source := archive.AccountDir(a.paths.Archive, from)
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("read archive of %s: %w (run backup first)", from, err)
	}

	httpClient, err := a.accountClient(ctx, registry, target, logger)
	if err != nil {
		return err
	}

	job := restoreJob{
		target: target, source: source, migrate: from != target, dryRun: dryRun,
		registry: registry, http: httpClient, logger: logger, out: out,
	}

	var (
		lines   []string
		failed  int
		skipped int
		errs    []error
	)

	for _, name := range dataTypes() {
		line := fmt.Sprintf("%s %s: ", target, name)

		res, err := a.writer(&job, name)(ctx)
		failed += res.Failed
		skipped += res.Skipped

		if err != nil {
			errs = append(errs, fmt.Errorf("restore %s %s: %w", target, name, needsLogin(err)))
		}

		switch written := map[bool]string{false: "written", true: "to write"}[dryRun]; {
		case err != nil && res == restore.Result{}:
			lines = append(lines, line+"failed")
		default:
			summary := fmt.Sprintf("%d %s, %d already there, %d skipped, %d failed", res.Written, written, res.Present, res.Skipped, res.Failed)
			if err != nil {
				summary += ", then stopped"
			}

			lines = append(lines, line+summary)
		}
	}

	a.status.clear()

	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("print result: %w", err)
	}

	switch {
	case len(errs) > 0:
		return errors.Join(errs...)
	case failed > 0:
		return fmt.Errorf("restore %s: %d items failed", target, failed)
	case skipped > 0:
		return fmt.Errorf("restore %s: %d %w", target, skipped, errSkipped)
	default:
		return nil
	}
}

// restoreJob is one restore or migrate run into a target account.
type restoreJob struct {
	target   account.ID
	source   string // the account directory of the archive written from
	migrate  bool
	dryRun   bool
	registry account.Registry
	http     *http.Client
	logger   *slog.Logger
	out      io.Writer
}

// writer returns the run that writes one data type of an archive into the
// target.
func (a *app) writer(job *restoreJob, name string) func(context.Context) (restore.Result, error) {
	planned := func(path, action string) {
		a.status.clear()

		verb := map[string]string{restore.ActionWrite: "would write", restore.ActionPresent: "already there"}[action]
		_, _ = fmt.Fprintf(job.out, "%s %s/%s\n", verb, name, path) //nolint:errcheck // the summary line reports a broken stdout.
	}

	store := archive.NewStore(filepath.Join(job.source, name), fileExt(name))
	progress := a.progress(job.target, name)

	if job.target.Provider == account.Gmail {
		return a.googleWriter(job, name, store, progress, planned)
	}

	return a.outlookWriter(job, name, store, progress, planned)
}

// googleWriter returns the run that writes one data type into a Google
// account.
func (a *app) googleWriter(
	job *restoreJob, name string, store archive.Store, progress backup.Progress, planned restore.Planned,
) func(context.Context) (restore.Result, error) {
	logger, dryRun := job.logger, job.dryRun

	switch name {
	case partMail:
		return (&restore.GmailMail{
			Client: gmail.New(job.http, cmp.Or(a.rt.APIs.Gmail, gmail.DefaultBaseURL)),
			Source: store, Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	case partCalendars:
		return (&restore.GoogleCalendar{
			Client: gcal.New(job.http, cmp.Or(a.rt.APIs.Calendar, gcal.DefaultBaseURL)), Source: store, Owner: job.target.Email,
			Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	case partContacts:
		return (&restore.GoogleContacts{
			Client: gpeople.New(job.http, &http.Client{}, cmp.Or(a.rt.APIs.People, gpeople.DefaultBaseURL)), Source: store, Migrate: job.migrate,
			Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	default:
		return (&restore.GoogleTasks{
			Client: gtasks.New(job.http, cmp.Or(a.rt.APIs.Tasks, gtasks.DefaultBaseURL)),
			Source: store, Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	}
}

// outlookWriter returns the run that writes one data type into an
// Outlook.com account.
func (a *app) outlookWriter(
	job *restoreJob, name string, store archive.Store, progress backup.Progress, planned restore.Planned,
) func(context.Context) (restore.Result, error) {
	logger, dryRun := job.logger, job.dryRun
	client := a.graphClient(job.http)

	switch name {
	case partMail:
		return (&restore.OutlookMail{
			Graph: client, Open: a.openIMAP(job), Source: store,
			Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	case partCalendars:
		return (&restore.OutlookCalendar{
			Client: client, Source: store, Owner: job.target.Email,
			Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	case partContacts:
		return (&restore.OutlookContacts{
			Client: client, Source: store, Migrate: job.migrate,
			Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	default:
		return (&restore.OutlookTasks{
			Client: client, Source: store, Logger: logger, DryRun: dryRun, Planned: planned, Progress: progress,
		}).Run
	}
}

func (a *app) graphClient(httpClient *http.Client) *graph.Client {
	return graph.New(httpClient, cmp.Or(a.rt.APIs.Graph, graph.DefaultBaseURL))
}

// openIMAP returns how an Outlook.com account's mailbox is opened over IMAP,
// with its IMAP token (O138), refreshed and saved as needed.
func (a *app) openIMAP(job *restoreJob) func(context.Context) (*outlookimap.Mailbox, error) {
	return func(ctx context.Context) (*outlookimap.Mailbox, error) {
		tok, err := job.registry.IMAPToken(job.target)
		if err != nil {
			return nil, err
		}

		if !tok.Valid() {
			// The refresh names the IMAP scope, or Microsoft would answer
			// with a Graph token.
			endpoints := a.endpoints()
			refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: tokenTimeout})

			if tok, err = oauthflow.Refresh(refreshCtx, login.IMAPConfig(a.clients(), &endpoints), tok.RefreshToken); err != nil {
				return nil, fmt.Errorf("refresh imap token of %s: %w", job.target, err)
			}

			if saveErr := job.registry.SaveIMAP(job.target, tok); saveErr != nil {
				job.logger.Warn("save refreshed imap token", slog.String("error", saveErr.Error()))
			}
		}

		dial := a.rt.DialIMAP
		if dial == nil {
			dial = func(ctx context.Context) (net.Conn, error) {
				return outlookimap.DialTLS(ctx, outlookimap.DefaultAddress)
			}
		}

		conn, err := dial(ctx)
		if err != nil {
			return nil, err
		}

		mailbox, err := outlookimap.Open(ctx, conn, &outlookimap.XOAuth2{Username: job.target.Email, AccessToken: tok.AccessToken})
		if errors.Is(err, outlookimap.ErrSignIn) {
			return nil, fmt.Errorf("account %s: mail writing: %w: %w", job.target, account.ErrNeedsLogin, err)
		}

		if errors.Is(err, outlookimap.ErrIMAPOff) {
			return nil, fmt.Errorf("account %s: mail writing: turn on \"Let devices and apps use IMAP\" in Outlook.com "+
				"(Settings > Mail > Forwarding and IMAP), then run again: %w", job.target, err)
		}

		return mailbox, err
	}
}
