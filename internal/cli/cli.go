// Package cli defines pimvault's command line: global flags, logging setup,
// directory resolution and the commands. Commands print results to stdout and
// diagnostics to stderr.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/spf13/cobra"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/config"
	"github.com/tonimelisma/pimvault/internal/login"
	"github.com/tonimelisma/pimvault/internal/state"
)

// logFormatText is the default log format.
const logFormatText = "text"

// Exit codes (D93).
const (
	ExitOK         = 0
	ExitError      = 1
	ExitSkipped    = 2
	ExitNeedsLogin = 3
)

// Runtime is the process environment the CLI runs in. Tests supply their own.
type Runtime struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Env    config.Env
	Home   string
	// LoginEndpoints overrides the provider URLs; nil means the real ones.
	LoginEndpoints *login.Endpoints
	// APIs overrides the provider API roots; empty fields mean the real
	// ones.
	APIs APIs
	// StderrWidth returns the width of stderr in columns when stderr is a
	// terminal; progress is then a status line redrawn in place instead of
	// periodic log lines (D77). Nil means stderr is not a terminal.
	StderrWidth func() int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// DialIMAP connects to Outlook.com's IMAP server; nil means TLS to
	// outlookimap.DefaultAddress.
	DialIMAP func(ctx context.Context) (net.Conn, error)
	// Version is what --version prints (D187).
	Version string
}

// APIs are the provider API roots.
type APIs struct {
	Gmail    string
	Calendar string
	People   string
	Tasks    string
	Graph    string
}

// app holds per-invocation state shared by the commands.
type app struct {
	rt        *Runtime
	flags     config.Flags
	logFormat string
	verbose   bool
	quiet     bool
	// backupOpts are backup's flags.
	backupOpts backup.Options
	logger     *slog.Logger
	status     *statusLine
	file       config.File
	paths      config.Paths
}

// Run executes one command line and returns the process exit code.
func Run(ctx context.Context, args []string, rt *Runtime) int {
	a := &app{rt: rt}
	root := a.rootCommand() //nolint:contextcheck // commands take ctx from cobra via ExecuteContext.
	root.SetArgs(args)

	if err := root.ExecuteContext(ctx); err != nil {
		a.reportError(err)

		if errors.Is(err, account.ErrNeedsLogin) {
			return ExitNeedsLogin
		}

		if errors.Is(err, errSkipped) {
			return ExitSkipped
		}

		return ExitError
	}

	return ExitOK
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:               "pimvault",
		Short:             "Back up Gmail and Outlook.com mail, calendars, contacts and tasks to local files",
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return a.setup() },
		Version:           a.rt.Version,
	}
	root.SetVersionTemplate("pimvault {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(a.rt.Stdout)
	root.SetErr(a.rt.Stderr)

	flags := root.PersistentFlags()
	flags.StringVar(&a.flags.Archive, "archive", "", "archive directory (env "+config.EnvArchive+")")
	flags.StringVar(&a.flags.Config, "config", "", "config file (env "+config.EnvConfig+")")
	flags.StringVar(&a.flags.State, "state", "", "state directory (env "+config.EnvState+")")
	flags.StringVar(&a.logFormat, "log-format", logFormatText, "log format: text or json")
	flags.BoolVarP(&a.verbose, "verbose", "v", false, "log debug details")
	flags.BoolVarP(&a.quiet, "quiet", "q", false, "log only warnings and errors")

	root.AddCommand(a.loginCommand(), a.accountCommand(), a.backupCommand(), a.verifyCommand(), a.restoreCommand(), a.migrateCommand())

	return root
}

// setup validates global flags, creates the logger and resolves directories.
func (a *app) setup() error {
	handler, err := a.logHandler()
	if err != nil {
		return err
	}

	a.logger = slog.New(handler)
	a.status.logger = a.logger

	configFile, err := config.ConfigFilePath(a.flags, a.rt.Env, a.rt.Home)
	if err != nil {
		return err
	}

	file, err := config.Load(configFile)
	if err != nil {
		return err
	}

	paths, err := config.Resolve(configFile, &file, a.flags, a.rt.Env, a.rt.Home)
	if err != nil {
		return err
	}

	a.file = file
	a.paths = paths
	a.logger.Debug("resolved directories",
		slog.String("config", paths.ConfigFile),
		slog.String("archive", paths.Archive),
		slog.String("state", paths.State))

	return nil
}

func (a *app) logHandler() (slog.Handler, error) {
	if a.verbose && a.quiet {
		return nil, errors.New("parse flags: --verbose and --quiet cannot be combined")
	}

	level := slog.LevelInfo

	switch {
	case a.verbose:
		level = slog.LevelDebug
	case a.quiet:
		level = slog.LevelWarn
	}

	opts := &slog.HandlerOptions{Level: level}

	// JSON logs stay one object per line (D78), so they get no status line.
	width := a.rt.StderrWidth
	if a.logFormat != logFormatText {
		width = nil
	}

	a.status = newStatusLine(a.rt.Stderr, width, a.now)

	switch a.logFormat {
	case logFormatText:
		return slog.NewTextHandler(a.status, opts), nil
	case "json":
		return slog.NewJSONHandler(a.status, opts), nil
	default:
		return nil, fmt.Errorf("parse flags: --log-format %q: want text or json", a.logFormat)
	}
}

// locked runs a command holding the state directory's run lock, so runs
// using the same state directory never overlap (D186).
func (a *app) locked(run func() error) (err error) {
	unlock, err := state.Lock(a.paths.State)
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, unlock()) }()

	return run()
}

// now returns the time on the runtime's clock.
func (a *app) now() time.Time {
	if a.rt.Now != nil {
		return a.rt.Now()
	}

	return time.Now()
}

// reportError writes a failed command's error to stderr, through the logger
// when setup got far enough to create one.
func (a *app) reportError(err error) {
	if a.logger != nil {
		a.logger.Error(err.Error())

		return
	}

	_, _ = fmt.Fprintln(a.rt.Stderr, "pimvault: "+err.Error()) //nolint:errcheck // stderr is the last place to report to.
}
