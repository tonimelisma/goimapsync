package backup_test

import (
	"context"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/graph"
)

// Validates: PV-120
func TestVerify_GmailMail(t *testing.T) {
	f := newFixture(t)
	f.srv.AddLabel("Label_1", "Work")
	f.srv.Add("m1", "Subject: one\r\n\r\nbody", "INBOX")
	f.srv.Add("m2", "Subject: two\r\n\r\nbody", "INBOX")
	f.srv.Add("m3", "Subject: three\r\n\r\nbody", "Label_1")
	f.srv.Add("m4", "Subject: spam\r\n\r\nbody", "SPAM")
	f.backup(t)

	check, err := f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Checked: 5}, check, "Inbox, Drafts, Sent, Archive and Work")

	// The account gains a message and the archive loses one; a stray
	// directory appears in the archive.
	f.srv.Add("m5", "Subject: five\r\n\r\nbody", "Label_1")
	require.NoError(t, os.Remove(filepath.Join(f.root, "Inbox", "m1.eml")))
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Old"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "Old", "m9.eml"), []byte("Subject: x\r\n\r\nbody"), 0o600))

	check, err = f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Checked: 6, Differences: []backup.Difference{
		{Path: "Inbox", Account: 2, Archive: 1},
		{Path: "Old", Account: 0, Archive: 1},
		{Path: "Work", Account: 2, Archive: 1},
	}}, check)
	assert.Equal(t, []string{"Inbox/m2.eml", "Old/m9.eml", "Work/m3.eml"}, f.files(t), "verify writes nothing")
}

// Validates: PV-120
func TestVerify_OutlookMail(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("m1", "f-inbox", "Subject: one\r\n\r\nbody")
	f.srv.Add("m2", "f-sent", "Subject: two\r\n\r\nbody")
	f.backup(t)

	check, err := f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Checked: 4}, check, "Inbox, Drafts, Sent and Archive")

	f.srv.FailFolder("f-sent", http.StatusInternalServerError)
	f.srv.Add("m3", "f-inbox", "Subject: three\r\n\r\nbody")

	check, err = f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Checked: 3, Failed: 1, Differences: []backup.Difference{
		{Path: "Inbox", Account: 2, Archive: 1},
	}}, check, "a folder that cannot be read is not compared")
}

// Validates: PV-120
func TestVerify_Collections(t *testing.T) {
	f := newOutlookContactsFixture(t)
	f.srv.AddContactFolder("cf-friends", "Friends", graph.DefaultContactFolder)
	f.srv.PutContact(graph.DefaultContactFolder, outlookContact("c-ann", "Ann"))
	f.srv.PutContact("cf-friends", outlookContact("c-bob", "Bob"))
	f.backup(t)

	check, err := f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Checked: 2}, check)

	require.NoError(t, os.Remove(filepath.Join(f.root, "Friends", "c-bob.vcf")))

	check, err = f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Checked: 2, Differences: []backup.Difference{{Path: "Friends", Account: 1, Archive: 0}}}, check)
}

func TestVerify_GoogleContactWithFailedPhotoIsUncounted(t *testing.T) {
	f := newGpeopleFixture(t)
	f.srv.Put(contact(t, "c1", "Ann", photo(f.srv.SetPhoto("ann", []byte("\xff\xd8 ann")), false)))
	f.backup(t)

	f.srv.FailPhotos = http.StatusInternalServerError

	check, err := f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Failed: 1}, check)
}

func TestVerify_OutlookMailRetriesExpiredListingAndSkipsUnusableIDs(t *testing.T) {
	f := newOutlookFixture(t)
	f.srv.Add("m1", "f-inbox", "Subject: one\r\n\r\nbody")
	f.srv.Add("m2", "f-inbox", "Subject: two\r\n\r\nbody")
	f.srv.Add("bad/id", "f-inbox", "Subject: cannot be a file name\r\n\r\nbody")
	f.backup(t)

	f.srv.PageSize = 1
	f.srv.ExpireListings()

	check, err := f.run.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, backup.Check{Checked: 4}, check, "the expired listing is read again; the unusable ID is left out")
}

// Validates: PV-121
func TestCheckFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(store archive.Store, folder, id, content string) {
		t.Helper()
		require.NoError(t, store.Write(folder, id, []byte(content)))
	}

	mail := archive.NewStore(filepath.Join(dir, "mail"), ".eml")
	long := strings.Repeat("ö", 200) // a cut directory name (D188)
	write(mail, "Inbox", "ok", "Subject: x\r\n\r\nbody")
	write(mail, "Inbox", "lf", "From: a@b\nSubject: x\n")
	write(mail, "Inbox", "empty", "")
	write(mail, "Inbox", "text", "just text\r\n")
	write(mail, long, "deep", "Subject: x\r\n\r\n")

	event := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20260101T000000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cals := archive.NewStore(filepath.Join(dir, "calendars"), ".ics")
	write(cals, "Calendar", "ok", event)
	write(cals, "Calendar", "noevent", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nEND:VCALENDAR\r\n")
	write(cals, "Calendar", "cut", event[:40])
	write(cals, "Calendar", "twice", event+event[:30])

	card := "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Ann\r\nEND:VCARD\r\n"
	cards := archive.NewStore(filepath.Join(dir, "contacts"), ".vcf")
	write(cards, "Contacts", "ok", card)
	write(cards, "Contacts", "cut", "BEGIN:VCARD\r\nFN:Ann\r\n")
	write(cards, "Contacts", "empty", "")

	check := func(store archive.Store) map[string]string {
		bad, err := backup.CheckFiles(t.Context(), store)
		require.NoError(t, err)

		got := map[string]string{}
		for _, u := range bad {
			got[u.Path] = u.Reason
		}

		return got
	}

	assert.Equal(t, map[string]string{"Inbox/empty.eml": "empty file", "Inbox/text.eml": "no message header"}, check(mail))

	gotCals := check(cals)
	assert.Equal(t, []string{"Calendar/cut.ics", "Calendar/noevent.ics", "Calendar/twice.ics"}, slices.Sorted(maps.Keys(gotCals)))
	assert.Equal(t, "parse iCalendar: no event or task", gotCals["Calendar/noevent.ics"])
	assert.Equal(t, "parse iCalendar: more than one VCALENDAR", gotCals["Calendar/twice.ics"])
	assert.Equal(t, "parse iCalendar: cut short", gotCals["Calendar/cut.ics"])

	gotCards := check(cards)
	assert.Equal(t, []string{"Contacts/cut.vcf", "Contacts/empty.vcf"}, slices.Sorted(maps.Keys(gotCards)))
	assert.Equal(t, "empty file", gotCards["Contacts/empty.vcf"])
	assert.Contains(t, gotCards["Contacts/cut.vcf"], "parse vCard")
}

func TestCheckFiles_StopsWhenCanceled(t *testing.T) {
	store := archive.NewStore(t.TempDir(), ".eml")
	require.NoError(t, store.Write("Inbox", "m1", []byte("Subject: x\r\n\r\n")))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := backup.CheckFiles(ctx, store)
	require.ErrorIs(t, err, context.Canceled)
}
