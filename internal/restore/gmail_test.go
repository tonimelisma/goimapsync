package restore_test

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gmail"
	"github.com/tonimelisma/pimvault/internal/gmail/gmailtest"
	"github.com/tonimelisma/pimvault/internal/restore"
)

func eml(messageID, date, from, subject string) []byte {
	h := ""
	if messageID != "" {
		h += "Message-ID: " + messageID + "\r\n"
	}

	return []byte(h + "Date: " + date + "\r\nFrom: " + from + "\r\nSubject: " + subject + "\r\n\r\nBody.\r\n")
}

type gmailFixture struct {
	srv    *gmailtest.Server
	source archive.Store
	run    *restore.GmailMail
}

func newGmailFixture(t *testing.T) *gmailFixture {
	t.Helper()

	srv := gmailtest.New(t)
	source := archive.NewStore(filepath.Join(t.TempDir(), "mail"), ".eml")

	return &gmailFixture{srv: srv, source: source, run: &restore.GmailMail{
		Client: gmail.New(http.DefaultClient, srv.URL),
		Source: source,
		Logger: slog.New(slog.DiscardHandler),
	}}
}

func (f *gmailFixture) put(t *testing.T, folder, id string, raw []byte) {
	t.Helper()
	require.NoError(t, f.source.Write(folder, id, raw))
}

// byLabels maps each message's subject line to its labels.
func (f *gmailFixture) byLabels() map[string][]string {
	out := make(map[string][]string)

	for _, m := range f.srv.Messages() {
		msgSubject := ""

		for _, line := range strings.Split(m.Raw, "\r\n") {
			if v, ok := strings.CutPrefix(line, "Subject: "); ok {
				msgSubject = v
			}
		}

		out[msgSubject] = m.Labels
	}

	return out
}

const date = "Mon, 02 Mar 2026 10:00:00 +0200"

// Validates: PV-152, PV-153, PV-163, PV-165, PV-167, PV-171
func TestGmailMail_RestoreMapsFoldersAndSkipsCopies(t *testing.T) {
	f := newGmailFixture(t)
	// The account already has a; the archive's copy writes the date and
	// sender differently but means the same.
	f.srv.Add("existing", string(eml("<a@x>", date, "Ann <ann@example.com>", "a")), "INBOX")

	f.put(t, archive.Inbox, "a", eml("<a@x>", "Mon, 2 Mar 2026 08:00:00 +0000", "ANN@example.com", "a"))
	f.put(t, archive.Sent, "b", eml("<b@x>", date, "me@example.com", "b"))
	f.put(t, archive.FolderPath([]string{"Work", "Projects"}, archive.MailFolders()), "c", eml("<c@x>", date, "x@example.com", "c"))
	f.put(t, archive.Archive, "d", eml("<d@x>", date, "x@example.com", "d"))
	f.put(t, archive.Drafts, "e", eml("<e@x>", date, "me@example.com", "e"))
	f.put(t, archive.Inbox, "f", eml("", date, "x@example.com", "f"))
	f.put(t, archive.Inbox, "g", eml("<a@x>", "Tue, 03 Mar 2026 10:00:00 +0200", "ann@example.com", "g")) // same ID, other date

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, restore.Result{Written: 6, Present: 1}, res)
	assert.Equal(t, map[string]string{"Label_1": "Work/Projects"}, f.srv.Labels())
	assert.Equal(t, map[string][]string{
		"a": {"INBOX"}, "b": {"SENT"}, "c": {"Label_1"}, "d": nil, "e": {"DRAFT"}, "f": {"INBOX"}, "g": {"INBOX"},
	}, f.byLabels(), "restored mail is read: no UNREAD label")

	// Running again writes nothing: the message without a Message-ID was
	// given one derived from its content (D184), and is found by it.
	res, err = f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 7}, res)
}

// Validates: PV-154
func TestGmailMail_RepairsMissingDateAndMessageIDInTheCopy(t *testing.T) {
	f := newGmailFixture(t)

	received := "Received: from a.example by b.example; Tue, 03 Mar 2026 09:00:00 +0000\r\n" +
		"Received: from c.example by a.example; Tue, 03 Mar 2026 08:59:00 +0000\r\n"
	noDate := []byte(received + "Message-ID: <nodate@x>\r\nFrom: x@example.com\r\nSubject: nodate\r\n\r\nBody.\r\n")
	badDate := []byte("Message-ID: <baddate@x>\r\nDate: someday\r\nFrom: x@example.com\r\nSubject: baddate\r\n\r\nBody.\r\n")
	noID := []byte("Date: " + date + "\nFrom: x@example.com\nSubject: noid\n\nBody.\n")
	noHeader := []byte("Just text.\r\n")

	f.put(t, archive.Inbox, "a", noDate)
	f.put(t, archive.Inbox, "b", badDate)
	f.put(t, archive.Inbox, "c", noID)
	f.put(t, archive.Inbox, "d", noHeader)

	modified := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"b", "d"} {
		require.NoError(t, os.Chtimes(filepath.Join(f.source.Root(), archive.Inbox, id+".eml"), modified, modified))
	}

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 4}, res)

	// The newest Received field dates the message.
	assert.Equal(t, "Date: Tue, 03 Mar 2026 09:00:00 +0000\r\n"+string(noDate), findRaw(t, f, "nodate"))
	// An unparseable Date is replaced by the file's time.
	assert.Equal(t, "Date: Wed, 01 Apr 2026 12:00:00 +0000\r\nMessage-ID: <baddate@x>\r\nFrom: x@example.com\r\nSubject: baddate\r\n\r\nBody.\r\n",
		findRaw(t, f, "baddate"))
	// A derived Message-ID, in the message's own line endings.
	assert.Regexp(t, `^Message-ID: <[0-9a-f]{32}@pimvault\.invalid>\n`+regexp.QuoteMeta(string(noID))+`$`, findRaw(t, f, "noid"))
	// A message without a header gets one, then the original as its body.
	assert.Regexp(t, `^Date: Wed, 01 Apr 2026 12:00:00 \+0000\r\nMessage-ID: <[0-9a-f]{32}@pimvault\.invalid>\r\n\r\nJust text\.\r\n$`,
		findRaw(t, f, "Just text"))

	// The archive keeps the messages as they were.
	raw, err := f.source.Read(archive.Inbox, "c")
	require.NoError(t, err)
	assert.Equal(t, noID, raw)

	// The copies are found again, even when the files were written again
	// since: a date from the file is not part of the key.
	rewritten := modified.Add(24 * time.Hour)
	for _, id := range []string{"b", "d"} {
		require.NoError(t, os.Chtimes(filepath.Join(f.source.Root(), archive.Inbox, id+".eml"), rewritten, rewritten))
	}

	res, err = f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 4}, res)
}

// findRaw returns the one message in the account that holds text.
func findRaw(t *testing.T, f *gmailFixture, text string) string {
	t.Helper()

	var found []string

	for _, m := range f.srv.Messages() {
		if strings.Contains(m.Raw, text) {
			found = append(found, m.Raw)
		}
	}

	require.Len(t, found, 1, text)

	return found[0]
}

// Validates: PV-162
func TestGmailMail_DryRunWritesNothing(t *testing.T) {
	f := newGmailFixture(t)
	f.srv.Add("existing", string(eml("<a@x>", date, "ann@example.com", "a")), "INBOX")
	f.put(t, archive.Inbox, "a", eml("<a@x>", date, "ann@example.com", "a"))
	f.put(t, "Work", "b", eml("<b@x>", date, "x@example.com", "b"))

	var planned []string

	f.run.DryRun = true
	f.run.Planned = func(path, action string) { planned = append(planned, action+" "+path) }

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, restore.Result{Written: 1, Present: 1}, res)
	assert.Equal(t, []string{"present Inbox/a", "write Work/b"}, planned)
	assert.Len(t, f.srv.Messages(), 1)
	assert.Empty(t, f.srv.Labels())
}

// Validates: PV-167
func TestGmailMail_MessageInSeveralFoldersIsWrittenOnce(t *testing.T) {
	f := newGmailFixture(t)
	f.put(t, archive.Inbox, "a", eml("<a@x>", date, "x@example.com", "a"))
	f.put(t, "Work", "a", eml("<a@x>", date, "x@example.com", "a"))
	// Another archived copy of b, under another ID: written once.
	f.put(t, archive.Inbox, "b1", eml("<b@x>", date, "x@example.com", "b"))
	f.put(t, archive.Archive, "b2", eml("<b@x>", date, "x@example.com", "b"))
	// A labeled draft stays a draft with its label (D165, F63).
	f.put(t, archive.Drafts, "c", eml("<c@x>", date, "me@example.com", "c"))
	f.put(t, "Work", "c", eml("<c@x>", date, "me@example.com", "c"))

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, restore.Result{Written: 3, Present: 1}, res)
	assert.Equal(t, map[string][]string{"a": {"INBOX", "Label_1"}, "b": {"INBOX"}, "c": {"DRAFT", "Label_1"}}, f.byLabels())
}

// Validates: PV-155
func TestGmailMail_FindsCopiesGmailRewroteOrDecoded(t *testing.T) {
	f := newGmailFixture(t)
	// Gmail shows display names decoded; the archive has them in a charset
	// Go does not decode.
	f.srv.Add("existing", string(eml("<a@x>", date, `"Łukasz Wójcik" <l@example.com>`, "a")), "INBOX")
	f.put(t, archive.Inbox, "a", eml("<a@x>", date, "=?iso-8859-2?Q?=A3ukasz_W=F3jcik?= <l@example.com>", "a"))
	// Gmail replaces this Message-ID on insert (F52).
	f.put(t, archive.Inbox, "b", eml("<Share-1;rcid_2>", date, "OneDrive <no-reply@example.com>", "b"))
	// Same sender and date as b, but another message, written before b.
	f.put(t, archive.Inbox, "a2", eml("<c@x>", date, "no-reply@example.com", "c"))

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 2, Present: 1}, res)

	res, err = f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

// Gmail's Message-ID search cannot find a Message-ID holding an
// encoded-word (F84); such copies are found by date and sender and matched
// on the Message-ID, so a second run writes nothing.
// Validates: PV-156
func TestGmailMail_FindsEncodedWordMessageID(t *testing.T) {
	f := newGmailFixture(t)
	f.put(t, archive.Inbox, "a", eml("<=?utf-8?b?aDE3?=-a@x>", date, "x@example.com", "a"))
	// Same sender, date and subject, another Message-ID: not a copy.
	f.put(t, archive.Inbox, "b", eml("<=?utf-8?b?aDE4?=-a@x>", date, "x@example.com", "a"))
	// An invalid Message-ID with an encoded-word: Gmail replaces it (F52),
	// so it is still found by its subject (D167).
	f.put(t, archive.Inbox, "c", eml("<=?utf-8?q?a;b?=@x>", date, "x@example.com", "c"))

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 3}, res)

	res, err = f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

// Validates: PV-166, PV-168
func TestGmailMail_LabelNamesMatchAnyCaseAndSystemNamesAreGmails(t *testing.T) {
	f := newGmailFixture(t)
	f.srv.AddLabel("Label_9", "work")
	f.put(t, "Work", "a", eml("<a@x>", date, "x@example.com", "a"))
	f.put(t, "Important", "b", eml("<b@x>", date, "x@example.com", "b"))
	f.put(t, "spam", "c", eml("<c@x>", date, "x@example.com", "c"))

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, restore.Result{Written: 3}, res)
	assert.Equal(t, map[string]string{"Label_9": "work"}, f.srv.Labels(), "no label created")
	assert.Equal(t, map[string][]string{"a": {"Label_9"}, "b": {"IMPORTANT"}, "c": {"SPAM"}}, f.byLabels())

	// The copy in Spam is found again.
	res, err = f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Present: 3}, res)
}

// Validates: PV-169
func TestGmailMail_RefusedLabelNamesAreRenamed(t *testing.T) {
	f := newGmailFixture(t)
	f.srv.AddLabel("Label_9", "Work Stuff")

	var logs strings.Builder

	f.run.Logger = slog.New(slog.NewTextHandler(&logs, nil))

	long := strings.Repeat("ö", 230) // 460 bytes: a cut directory name (D188), over Gmail's 255 bytes (F81)
	for id, folder := range map[string]string{
		"a": "Chats",                  // Gmail's own (F71)
		"b": "all mail",               // whatever the case
		"c": "Chats",                  // the same rename, one warning
		"d": " Work  Stuff",           // Gmail's whitespace trimming finds the label
		"e": long,                     // over 225 characters
		"f": "Spam",                   // a system label, as before (D162)
		"g": strings.Repeat("€", 100), // 100 characters but 300 bytes: Gmail answers 500 (F81)
		"h": strings.Repeat("€", 100), // the same rename, one warning
	} {
		f.put(t, archive.FolderPath([]string{folder}, archive.MailFolders()), id, eml("<"+id+"@x>", date, "x@example.com", id))
	}

	res, err := f.run.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, restore.Result{Written: 8}, res)

	labels := f.srv.Labels()
	byName := map[string]string{}

	for id, name := range labels {
		byName[name] = id
	}

	got := f.byLabels()
	assert.Equal(t, []string{byName["Chats (restored)"]}, got["a"])
	assert.Equal(t, []string{byName["all mail (restored)"]}, got["b"])
	assert.Equal(t, []string{byName["Chats (restored)"]}, got["c"])
	assert.Equal(t, []string{"Label_9"}, got["d"])
	assert.Equal(t, []string{byName[strings.Repeat("ö", 122)+" (restored)"]}, got["e"])
	assert.Equal(t, []string{"SPAM"}, got["f"])
	assert.Equal(t, []string{byName[strings.Repeat("€", 81)+" (restored)"]}, got["g"])
	assert.Equal(t, got["g"], got["h"])
	assert.Len(t, labels, 5, "no label for the trimmed name")
	assert.Equal(t, 4, strings.Count(logs.String(), "refuses this label name"))
}
