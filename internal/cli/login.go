package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/login"
)

func (a *app) loginCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "login <gmail|outlook>",
		Short: "Sign in to an account and register it",
		Long: "Sign in to a Gmail or Outlook.com account. Sign-in prints a URL to open on any\n" +
			"device; afterwards you paste the address the browser was sent to. One\n" +
			"sign-in grants everything backup, restore and migrate need.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return a.login(cmd, args[0]) },
	}
}

func (a *app) login(cmd *cobra.Command, providerName string) error {
	provider, err := account.ParseProvider(providerName)
	if err != nil {
		return err
	}

	endpoints := a.endpoints()
	stderr := cmd.ErrOrStderr()
	stdin := bufio.NewReader(a.stdin())

	res, err := login.Login(cmd.Context(), &login.Options{
		Provider:  provider,
		Clients:   a.clients(),
		Endpoints: &endpoints,
		Prompts: login.Prompts{
			ShowSignInURL: func(url string) {
				const instructions = "Open this URL in a browser on any device, sign in and allow access:\n\n%s\n\n" +
					"The browser then fails to load a 127.0.0.1 or localhost page. Copy the full\n" +
					"address from its address bar and paste it here:\n"
				_, _ = fmt.Fprintf(stderr, instructions, url) //nolint:errcheck // best-effort prompt.
			},
			ReadRedirect: func() (string, error) {
				line, readErr := stdin.ReadString('\n')
				if readErr != nil && (!errors.Is(readErr, io.EOF) || line == "") {
					return "", readErr //nolint:wrapcheck // oauthflow adds context.
				}

				return strings.TrimSpace(line), nil
			},
		},
	})
	if res == nil {
		return err
	}

	// Without its IMAP token the account still backs up, so it is saved.
	registry := account.NewRegistry(a.paths.ConfigDir)
	if saveErr := registry.Save(res.ID, res.Token); saveErr != nil {
		return saveErr
	}

	if res.IMAP != nil {
		if saveErr := registry.SaveIMAP(res.ID, res.IMAP); saveErr != nil {
			return saveErr
		}
	}

	if err != nil {
		return fmt.Errorf("%w: backup works; restore and migrate of mail into it need login again", err)
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "signed in as %s\n", res.ID); err != nil {
		return fmt.Errorf("print result: %w", err)
	}

	return nil
}

func (a *app) stdin() io.Reader {
	if a.rt.Stdin == nil {
		return strings.NewReader("")
	}

	return a.rt.Stdin
}

func (a *app) endpoints() login.Endpoints {
	if a.rt.LoginEndpoints != nil {
		return *a.rt.LoginEndpoints
	}

	return login.DefaultEndpoints()
}

func (a *app) clients() login.Clients {
	return login.Clients{
		GoogleID:     a.file.GoogleClientID,
		GoogleSecret: a.file.GoogleClientSecret,
		MicrosoftID:  a.file.MicrosoftClientID,
	}
}
