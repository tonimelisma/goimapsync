package restore_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/graph/graphtest"
	"github.com/tonimelisma/pimvault/internal/outlookimap"
	"github.com/tonimelisma/pimvault/internal/outlookimap/outlookimaptest"
	"github.com/tonimelisma/pimvault/internal/restore"
)

// Validates: PV-152, PV-163, PV-165, PV-171
func TestOutlookMail_Restore(t *testing.T) {
	srv := graphtest.New(t)
	srv.Add("m-existing", "f-inbox", string(eml("<a@x>", date, "Ann <ann@example.com>", "a")))
	// Without a Date, the Message-ID and From decide (and Graph has no
	// sent time to compare).
	undated := "Message-ID: <h@x>\r\nFrom: x@example.com\r\nSubject: h\r\n\r\nBody.\r\n"
	srv.Add("m-undated", "f-inbox", undated)
	// A copy in Deleted Items does not count: backup leaves that folder out.
	srv.Add("m-trashed", "f-deleted", string(eml("<i@x>", date, "x@example.com", "i")))
	// Graph cannot find this Message-ID; the date window does.
	srv.Add("m-share", "f-sent", string(eml("<Share-1;rcid_2>", date, "me@outlook.example.com", "j")))

	mailbox := outlookimaptest.New(t, "me@outlook.example.com", "imap-token")
	mailbox.MaxSize = 1000
	mailbox.DropAt = "e" // signed in again, then written

	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")
	put := func(folder, id string, raw []byte) { require.NoError(t, source.Write(folder, id, raw)) }

	put(archive.Inbox, "a", eml("<a@x>", "Mon, 2 Mar 2026 08:00:00 +0000", "ann@example.com", "a")) // the account has it
	put(archive.Inbox, "b", eml("<b@x>", date, "x@example.com", "b"))
	put(archive.Sent, "c", eml("<c@x>", date, "me@outlook.example.com", "c"))
	put(archive.Drafts, "d", eml("<d@x>", date, "me@outlook.example.com", "d"))
	put(archive.Archive, "e", eml("<e@x>", date, "x@example.com", "e"))
	put(archive.Subfolder(archive.Sent, "Project"), "k", eml("<k@x>", date, "me@outlook.example.com", "k"))
	put(archive.FolderPath([]string{"Work", "Projects"}, archive.MailFolders()), "f", eml("", date, "x@example.com", "f"))
	put(archive.Inbox, "h", []byte(undated))
	put(archive.Inbox, "i", eml("<i@x>", date, "x@example.com", "i"))
	put(archive.Sent, "j", eml("<Share-1;rcid_2>", date, "Me <me@outlook.example.com>", "j"))
	put(archive.Inbox, "g", append(eml("<g@x>", date, "x@example.com", "g"), strings.Repeat("x", 4000)...))

	opened := 0
	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, srv.URL),
		Open: func(ctx context.Context) (*outlookimap.Mailbox, error) {
			opened++

			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", mailbox.Address)
			if err != nil {
				return nil, fmt.Errorf("dial: %w", err)
			}

			return outlookimap.Open(ctx, conn, &outlookimap.XOAuth2{Username: "me@outlook.example.com", AccessToken: "imap-token"})
		},
		Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	// A dry run needs no IMAP sign-in.
	run.DryRun, run.Planned = true, func(string, string) {}
	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 8, Present: 3}, res)
	assert.Zero(t, opened)

	run.DryRun = false
	res, err = run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 7, Present: 3, Skipped: 1}, res)
	assert.Equal(t, 2, opened, "signed in again after the connection dropped")

	byFolder := map[string][]imap.Flag{}
	for _, m := range mailbox.Appended() {
		subject := m.Raw[strings.Index(m.Raw, "Subject: ")+len("Subject: "):][:1]
		byFolder[m.Folder+" "+subject] = m.Flags
		assert.Equal(t, "2026-03-02T08:00:00Z", m.Time.UTC().Format("2006-01-02T15:04:05Z"))
	}

	assert.Equal(t, map[string][]imap.Flag{
		"INBOX b":         {imap.FlagSeen},
		"INBOX i":         {imap.FlagSeen},
		"Sent c":          {imap.FlagSeen},
		"Drafts d":        {imap.FlagSeen, imap.FlagDraft},
		"Archive e":       {imap.FlagSeen},
		"Sent/Project k":  {imap.FlagSeen},
		"Work/Projects f": {imap.FlagSeen},
	}, byFolder)
}

func TestOutlookMail_SignInFailureStopsTheRun(t *testing.T) {
	mailbox := outlookimaptest.New(t, "me@outlook.example.com", "imap-token")
	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")

	for _, id := range []string{"a", "b"} {
		require.NoError(t, source.Write(archive.Inbox, id, eml("<"+id+"@x>", date, "x@example.com", id)))
	}

	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, graphtest.New(t).URL),
		Open: func(ctx context.Context) (*outlookimap.Mailbox, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", mailbox.Address)
			if err != nil {
				return nil, fmt.Errorf("dial: %w", err)
			}

			return outlookimap.Open(ctx, conn, &outlookimap.XOAuth2{Username: "me@outlook.example.com", AccessToken: "wrong"})
		},
		Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.ErrorContains(t, err, "imap sign-in")
	assert.Equal(t, restore.Result{Failed: 2}, res)
}

func TestOutlookMail_NothingToWriteNeedsNoIMAP(t *testing.T) {
	graphSrv := graphtest.New(t)
	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")

	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, graphSrv.URL),
		Open: func(context.Context) (*outlookimap.Mailbox, error) {
			t.Error("opened IMAP with nothing to write")

			return nil, errors.New("unexpected sign-in")
		},
		Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{}, res)
}

func TestOutlookMail_FailedSignInAfterADropStopsTheRun(t *testing.T) {
	mailbox := outlookimaptest.New(t, "me@outlook.example.com", "imap-token")
	mailbox.DropAt = "a"

	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, source.Write(archive.Inbox, id, eml("<"+id+"@x>", date, "x@example.com", id)))
	}

	opened := 0
	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, graphtest.New(t).URL),
		Open: func(ctx context.Context) (*outlookimap.Mailbox, error) {
			opened++
			if opened > 1 {
				return nil, errors.New("token revoked")
			}

			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", mailbox.Address)
			if err != nil {
				return nil, fmt.Errorf("dial: %w", err)
			}

			return outlookimap.Open(ctx, conn, &outlookimap.XOAuth2{Username: "me@outlook.example.com", AccessToken: "imap-token"})
		},
		Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.ErrorContains(t, err, "token revoked")
	assert.Equal(t, restore.Result{Failed: 3}, res)
	assert.Equal(t, 2, opened, "no sign-in attempt per remaining message")
}

// openMailbox signs in to a fake mailbox.
func openMailbox(mailbox *outlookimaptest.Server) func(ctx context.Context) (*outlookimap.Mailbox, error) {
	return func(ctx context.Context) (*outlookimap.Mailbox, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", mailbox.Address)
		if err != nil {
			return nil, fmt.Errorf("dial: %w", err)
		}

		return outlookimap.Open(ctx, conn, &outlookimap.XOAuth2{Username: "me@outlook.example.com", AccessToken: "imap-token"})
	}
}

// Validates: PV-164, PV-172
func TestOutlookMail_MessageOverOutlookLimitIsSkipped(t *testing.T) {
	// Outlook.com answers "BAD Command Argument Error. 11" just above its
	// limit (F74).
	mailbox := outlookimaptest.New(t, "me@outlook.example.com", "imap-token")
	mailbox.MaxSize = 13_803_520

	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")
	require.NoError(t, source.Write(archive.Inbox, "a", append(eml("<a@x>", date, "x@example.com", "a"), strings.Repeat("x", 13_900_000)...)))
	require.NoError(t, source.Write(archive.Inbox, "b", eml("<b@x>", date, "x@example.com", "b")))

	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, graphtest.New(t).URL), Open: openMailbox(mailbox),
		Source: source, Logger: slog.New(slog.DiscardHandler),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1, Skipped: 1}, res)
	require.Len(t, mailbox.Appended(), 1)
	assert.Contains(t, mailbox.Appended()[0].Raw, "<b@x>")
}

// Validates: PV-111
func TestOutlookMail_ThrottlingIsWaitedOut(t *testing.T) {
	mailbox := outlookimaptest.New(t, "me@outlook.example.com", "imap-token")
	mailbox.Throttle(2)

	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")
	require.NoError(t, source.Write(archive.Inbox, "a", eml("<a@x>", date, "x@example.com", "a")))

	var waits []time.Duration

	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, graphtest.New(t).URL), Open: openMailbox(mailbox),
		Source: source, Logger: slog.New(slog.DiscardHandler),
		Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)

			return nil
		},
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 1}, res)
	assert.Equal(t, []time.Duration{299961 * time.Millisecond, 299961 * time.Millisecond}, waits, "the server's suggested wait")
	assert.Len(t, mailbox.Appended(), 1)
}

func TestOutlookMail_LastingThrottlingStopsTheRun(t *testing.T) {
	mailbox := outlookimaptest.New(t, "me@outlook.example.com", "imap-token")
	mailbox.Throttle(100)

	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, source.Write(archive.Inbox, id, eml("<"+id+"@x>", date, "x@example.com", id)))
	}

	waited := 0
	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, graphtest.New(t).URL), Open: openMailbox(mailbox),
		Source: source, Logger: slog.New(slog.DiscardHandler),
		Sleep: func(context.Context, time.Duration) error {
			waited++

			return nil
		},
	}

	res, err := run.Run(t.Context())
	require.ErrorContains(t, err, "still throttled")
	assert.Equal(t, restore.Result{Failed: 3}, res)
	assert.Equal(t, 5, waited, "one message's waits, not every message's")
	assert.Empty(t, mailbox.Appended())
}

// Validates: PV-169
func TestOutlookMail_OutlookFolderNamesAreRenamed(t *testing.T) {
	srv := graphtest.New(t)
	srv.AddFolder("f-snoozed", "Snoozed", "")
	srv.AddFolder("f-receipts", "Receipts", "")

	mailbox := outlookimaptest.New(t, "me@outlook.example.com", "imap-token")
	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")

	dirs := map[string]string{
		"a": "Deleted Items",     // well-known, and reserved over IMAP
		"b": "outbox",            // well-known, whatever the case
		"c": "Notes",             // hidden from Graph (F73)
		"d": "Calendar",          // reserved over IMAP (F73)
		"e": "Calendar/Sub",      // the rename keeps what is inside
		"f": "Snoozed",           // Outlook's, but a user folder in Graph
		"g": "receipts",          // the user's own
		"h": "Deleted Items",     // renamed again without a second warning
		"j": "Projects",          // new: created by the first message
		"k": "Projects",          // and the same folder for the next
		"l": "Projects/Calendar", // a reserved name inside is the server's call
	}

	for id, dir := range dirs {
		require.NoError(t, source.Write(archive.FolderPath(strings.Split(dir, "/"), archive.MailFolders()), id,
			eml("<"+id+"@x>", date, "x@example.com", id)))
	}

	var logs strings.Builder

	run := &restore.OutlookMail{
		Graph: graph.New(http.DefaultClient, srv.URL), Open: openMailbox(mailbox),
		Source: source, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}

	res, err := run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 11}, res)

	got := map[string]string{}
	for _, m := range mailbox.Appended() {
		got[m.Raw[strings.Index(m.Raw, "Subject: ")+len("Subject: "):][:1]] = m.Folder
	}

	assert.Equal(t, map[string]string{
		"a": "Deleted Items (restored)",
		"b": "outbox (restored)",
		"c": "Notes (restored)",
		"d": "Calendar (restored)",
		"e": "Calendar (restored)/Sub",
		"f": "Snoozed",
		"g": "receipts",
		"h": "Deleted Items (restored)",
		"j": "Projects",
		"k": "Projects",
		"l": "Projects/Calendar",
	}, got)
	assert.Equal(t, 4, strings.Count(logs.String(), "keeps this folder name"), "one warning per renamed folder")
}
