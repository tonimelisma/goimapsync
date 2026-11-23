package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tonimelisma/pimvault/internal/account"
)

func (a *app) accountCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "List or remove signed-in accounts",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List signed-in accounts",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.accountList(cmd) },
	}, &cobra.Command{
		Use:   "remove <account>",
		Short: "Remove an account's sign-in; its archive and sync state are kept",
		Args:  cobra.ExactArgs(1),
		// Locked: a running backup that refreshes the token would save
		// it back (D186).
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.locked(func() error { return a.accountRemove(cmd, args[0]) })
		},
	})

	return cmd
}

// accountList prints every account with a valid token file, one per line.
// Broken token files are reported after the valid accounts are printed.
func (a *app) accountList(cmd *cobra.Command) error {
	ids, err := account.NewRegistry(a.paths.ConfigDir).List()

	for _, id := range ids {
		if _, printErr := fmt.Fprintln(cmd.OutOrStdout(), id); printErr != nil {
			return fmt.Errorf("print accounts: %w", printErr)
		}
	}

	return err
}

// accountRemove deletes the token file of the account the query matches.
func (a *app) accountRemove(cmd *cobra.Command, query string) error {
	registry := account.NewRegistry(a.paths.ConfigDir)

	ids, badNames, err := registry.IDs()
	if err != nil {
		return err
	}

	if badNames != nil {
		a.logger.Warn(badNames.Error())
	}

	id, err := account.Match(ids, query)
	if err != nil {
		return err
	}

	if err := registry.Remove(id); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", id); err != nil {
		return fmt.Errorf("print result: %w", err)
	}

	return nil
}
