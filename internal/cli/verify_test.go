package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/account"
)

func TestVerify_MatchesThenDiffers(t *testing.T) {
	env, srv := backupEnv(t)
	env.addSignedIn(t, "gmail:user@gmail.example.com")
	srv.Add("m1", "Subject: one\r\n\r\nbody", "INBOX")
	srv.Add("m2", "Subject: two\r\n\r\nbody", "INBOX")

	code, _, stderr := env.run(t, "backup")
	require.Equal(t, ExitOK, code, stderr)

	code, stdout, stderr := env.run(t, "verify")
	require.Equal(t, ExitOK, code, stderr)
	// Mail: Inbox, Drafts, Sent, Archive; calendars: Calendar; tasks: Tasks;
	// Google contacts: Contacts.
	assert.Equal(t, "gmail:user@gmail.example.com: 7 checked, 0 different, 0 unreadable\n", stdout)

	inbox := filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com", "mail", "Inbox")
	require.NoError(t, os.Remove(filepath.Join(inbox, "m1.eml")))

	code, stdout, _ = env.run(t, "verify")
	assert.Equal(t, ExitError, code)
	assert.Equal(t, "gmail:user@gmail.example.com mail/Inbox: account 2, archive 1\n"+
		"gmail:user@gmail.example.com: 7 checked, 1 different, 0 unreadable\n", stdout)
	assert.NoFileExists(t, filepath.Join(inbox, "m1.eml"), "verify writes nothing")

	// A damaged file is listed and counted as unreadable (D190).
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "m2.eml"), nil, 0o600))

	code, stdout, _ = env.run(t, "verify")
	assert.Equal(t, ExitError, code)
	assert.Equal(t, "gmail:user@gmail.example.com mail/Inbox/m2.eml: unreadable: empty file\n"+
		"gmail:user@gmail.example.com mail/Inbox: account 2, archive 1\n"+
		"gmail:user@gmail.example.com: 7 checked, 1 different, 1 unreadable\n", stdout)
}

func TestVerify_FilesAreReadWhenTheAccountCannotBe(t *testing.T) {
	env := newTestEnv(t)
	env.addAccount(t, "gmail:user@gmail.example.com")

	id, err := account.Parse("gmail:user@gmail.example.com")
	require.NoError(t, err)
	// A token file that does not parse: the account needs a new sign-in.
	require.NoError(t, os.WriteFile(account.NewRegistry(env.configDir).TokenPath(id), []byte("{"), 0o600))

	inbox := filepath.Join(env.home, "data", "pimvault", "gmail_user@gmail.example.com", "mail", "Inbox")
	require.NoError(t, os.MkdirAll(inbox, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "m1.eml"), nil, 0o600))

	code, stdout, _ := env.run(t, "verify")
	assert.Equal(t, ExitNeedsLogin, code)
	assert.Equal(t, "gmail:user@gmail.example.com mail/Inbox/m1.eml: unreadable: empty file\n"+
		"gmail:user@gmail.example.com: 0 checked, 0 different, 5 unreadable\n", stdout)
}
