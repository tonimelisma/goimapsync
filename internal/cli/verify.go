package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/backup"
)

func (a *app) verifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "verify [account...]",
		Short: "Compare the archive with the accounts",
		Long: "Count each account's items per folder, calendar, address book and task\n" +
			"list and compare the counts with the archive, and read every archive file,\n" +
			"without writing anything. Prints the places whose counts differ, the files\n" +
			"that cannot be read or parsed, and a summary line per account. With no\n" +
			"arguments every signed-in account is verified.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.locked(func() error { return a.verify(cmd, args) })
		},
	}
}

// verifyTally is one account's verify result.
type verifyTally struct {
	checked, differ, unreadable int
}

// verify verifies the selected accounts one after another (D81, D154).
func (a *app) verify(cmd *cobra.Command, queries []string) error {
	registry := account.NewRegistry(a.paths.ConfigDir)

	ids, err := a.selectAccounts(registry, queries)
	if err != nil {
		return err
	}

	var differ, needLogin int

	out := cmd.OutOrStdout()

	for _, id := range ids {
		tally, err := a.verifyAccount(cmd.Context(), registry, id, out)
		err = withLoginHint(id, err)
		if err != nil {
			a.logger.Error(err.Error(), slog.String("account", id.String()))

			if errors.Is(err, account.ErrNeedsLogin) {
				needLogin++
			}
		}

		if tally.differ > 0 || tally.unreadable > 0 {
			differ++
		}

		line := fmt.Sprintf("%s: %d checked, %d different, %d unreadable", id, tally.checked, tally.differ, tally.unreadable)

		a.status.clear()
		if _, printErr := fmt.Fprintln(out, line); printErr != nil {
			return fmt.Errorf("print result: %w", printErr)
		}
	}

	switch {
	case needLogin > 0:
		return fmt.Errorf("verify: %d of %d accounts %w", needLogin, len(ids), account.ErrNeedsLogin)
	case differ > 0:
		return fmt.Errorf("verify: %d of %d accounts differ from the archive or could not be read", differ, len(ids))
	default:
		return nil
	}
}

// checkFiles reads every archive file of an account, prints a line for
// each that cannot be read or parsed and counts them as unreadable (D190).
func (a *app) checkFiles(ctx context.Context, id account.ID, out io.Writer) (verifyTally, error) {
	var tally verifyTally

	for _, part := range dataTypes() {
		bad, err := backup.CheckFiles(ctx, a.store(id, part))
		if err != nil {
			return tally, fmt.Errorf("verify %s %s: %w", id, part, err)
		}

		tally.unreadable += len(bad)

		for _, u := range bad {
			if _, err := fmt.Fprintf(out, "%s %s/%s: unreadable: %s\n", id, part, u.Path, u.Reason); err != nil {
				return tally, fmt.Errorf("print result: %w", err)
			}
		}
	}

	return tally, nil
}

// verifyAccount verifies each data type of an account and prints a line
// for each place whose counts differ. A data type that cannot be read at
// all counts as one place that could not be read.
func (a *app) verifyAccount(ctx context.Context, registry account.Registry, id account.ID, out io.Writer) (verifyTally, error) {
	logger := a.logger.With(slog.String("account", id.String()))
	types := len(dataTypes())

	// The archive's files are read whether or not the account can be
	// (D190).
	tally, err := a.checkFiles(ctx, id, out)
	if err != nil {
		return tally, err
	}

	httpClient, err := a.accountClient(ctx, registry, id, logger)
	if err != nil {
		tally.unreadable += types

		return tally, err
	}

	kinds, err := a.dataKinds(ctx, httpClient, id, logger)
	if err != nil {
		tally.unreadable += types

		return tally, err
	}

	var errs []error

	for _, k := range kinds {
		check, err := k.verify()
		if err != nil {
			tally.unreadable++
			errs = append(errs, fmt.Errorf("verify %s %s: %w", id, k.name, needsLogin(err)))

			continue
		}

		tally.checked += check.Checked
		tally.differ += len(check.Differences)
		tally.unreadable += check.Failed

		for _, d := range check.Differences {
			line := fmt.Sprintf("%s %s/%s: account %d, archive %d", id, k.name, d.Path, d.Account, d.Archive)
			if _, printErr := fmt.Fprintln(out, line); printErr != nil {
				return tally, fmt.Errorf("print result: %w", printErr)
			}
		}
	}

	return tally, errors.Join(errs...)
}
