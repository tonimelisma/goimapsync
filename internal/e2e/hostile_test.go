//go:build e2e

package e2e_test

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/cli"
)

// hostileAccount is the archived account the hostile messages come from. It
// is never signed in; only its archive exists.
const hostileAccount = "gmail:hostile@pimvault.invalid"

// hostileMail is mail providers keep but Go's net/mail refuses or that
// lacks what restore needs (F70), filed under names Gmail refuses or
// changes (F71), each by archive directory and ID. The content is fixed, so
// every run writes the same messages and later runs find them.
func hostileMail() map[string]map[string]string {
	body := "\r\n\r\nHostile seed message for pimvault's end-to-end test.\r\n"
	from := "From: Seed <seed@pimvault.invalid>\r\n"

	return map[string]map[string]string{
		archive.Inbox: {
			"h01noDate": "Received: from a by b; Tue, 03 Mar 2026 09:00:00 +0000\r\nMessage-ID: <h01@pimvault.invalid>\r\n" +
				from + "Subject: h01 no Date, dated by Received" + body,
			"h02noDateNoReceived": "Message-ID: <h02@pimvault.invalid>\r\n" + from + "Subject: h02 no Date at all" + body,
			"h03badDate":          "Date: someday soon\r\nMessage-ID: <h03@pimvault.invalid>\r\n" + from + "Subject: h03 bad Date" + body,
			"h04noMessageID":      "Date: Tue, 03 Mar 2026 10:00:00 +0000\r\n" + from + "Subject: h04 no Message-ID" + body,
			"h05emptyMessageID":   "Date: Tue, 03 Mar 2026 10:05:00 +0000\r\nMessage-ID:\r\n" + from + "Subject: h05 empty Message-ID" + body,
			"h06invalidMessageID": "Date: Tue, 03 Mar 2026 10:10:00 +0000\r\nMessage-ID: <Share-1;rcid_2>\r\n" + from +
				"Subject: h06 invalid Message-ID" + body,
			"h07noHeader": "Hostile seed message h07 without any header.\r\n",
			"h08bareLF": strings.ReplaceAll("Date: Tue, 03 Mar 2026 10:20:00 +0000\r\nMessage-ID: <h08@pimvault.invalid>\r\n"+
				from+"Subject: h08 bare LF"+body, "\r\n", "\n"),
			"h09latin1": "Date: Tue, 03 Mar 2026 10:25:00 +0000\r\nMessage-ID: <h09@pimvault.invalid>\r\n" +
				"From: J\xfcrgen <seed@pimvault.invalid>\r\nSubject: h09 8-bit header \xe4\xf6" + body,
			"h10badFrom": "Date: Tue, 03 Mar 2026 10:30:00 +0000\r\nMessage-ID: <h10@pimvault.invalid>\r\nFrom: not an address\r\n" +
				"Subject: h10 From is not an address" + body,
			"h11longLine": "Date: Tue, 03 Mar 2026 10:35:00 +0000\r\nMessage-ID: <h11@pimvault.invalid>\r\n" + from +
				"Subject: h11 a folded\r\n subject and a long line\r\n\r\n" + strings.Repeat("x", 20000) + "\r\n",
			"h12twoFolders": "Date: Tue, 03 Mar 2026 10:40:00 +0000\r\nMessage-ID: <h12@pimvault.invalid>\r\n" + from +
				"Subject: h12 in two folders" + body,
			// Valid msg-ids with "=" and "?", as in spam that put an
			// encoded-word in its Message-ID (seen 2026-11-01). Gmail
			// indexes them decoded (F84); h20's decodes to punctuation.
			"h17encodedWordMessageID": "Date: Tue, 03 Mar 2026 11:05:00 +0000\r\n" +
				"Message-ID: <=?utf-8?b?aDE3?=-h17@pimvault.invalid>\r\n" + from + "Subject: h17 encoded-word Message-ID" + body,
			"h20encodedPunctuation": "Date: Tue, 03 Mar 2026 11:20:00 +0000\r\n" +
				"Message-ID: <=?utf-8?b?NCgmXiVeJigjQCQlKg==?=-h20@pimvault.invalid>\r\n" + from +
				"Subject: h20 encoded-word Message-ID decoding to punctuation" + body,
			"h18questionMessageID": "Date: Tue, 03 Mar 2026 11:10:00 +0000\r\nMessage-ID: <h18?q@pimvault.invalid>\r\n" + from +
				"Subject: h18 question mark Message-ID" + body,
			"h19equalsMessageID": "Date: Tue, 03 Mar 2026 11:15:00 +0000\r\nMessage-ID: <h19=e@pimvault.invalid>\r\n" + from +
				"Subject: h19 equals sign Message-ID" + body,
		},
		archive.FolderPath([]string{"Chats"}, archive.MailFolders()): {
			"h13refusedLabel": "Date: Tue, 03 Mar 2026 10:45:00 +0000\r\nMessage-ID: <h13@pimvault.invalid>\r\n" + from +
				"Subject: h13 label Gmail refuses" + body,
		},
		archive.FolderPath([]string{"  Hostile   spaced  "}, archive.MailFolders()): {
			"h14spacedLabel": "Date: Tue, 03 Mar 2026 10:50:00 +0000\r\nMessage-ID: <h14@pimvault.invalid>\r\n" + from +
				"Subject: h14 label Gmail trims" + body,
			"h12twoFolders": "Date: Tue, 03 Mar 2026 10:40:00 +0000\r\nMessage-ID: <h12@pimvault.invalid>\r\n" + from +
				"Subject: h12 in two folders" + body,
		},
		archive.FolderPath([]string{"Hostile", "Deep", "Nested", "Path 🎉"}, archive.MailFolders()): {
			"h15deep": "Date: Tue, 03 Mar 2026 10:55:00 +0000\r\nMessage-ID: <h15@pimvault.invalid>\r\n" + from +
				"Subject: h15 deep emoji label" + body,
		},
		// 230 characters, 452 bytes: a cut directory (D188), and a label
		// name over Gmail's limits (F71, F81).
		archive.FolderPath([]string{"Hostile " + strings.Repeat("ö", 222)}, archive.MailFolders()): {
			"h16longLabel": "Date: Tue, 03 Mar 2026 11:00:00 +0000\r\nMessage-ID: <h16@pimvault.invalid>\r\n" + from +
				"Subject: h16 long label" + body,
		},
	}
}

// hostileEvents are a weekly series with changed and cancelled occurrences,
// a floating time (D171) and an all-day event, by ID. Fixed content, like
// hostileMail.
func hostileEvents() map[string]string {
	var series strings.Builder

	series.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//pimvault//hostile//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:hostile-series@pimvault.invalid\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20260105T080000Z\r\nDTEND:20260105T090000Z\r\nSUMMARY:Hostile weekly series\r\n" +
		"RRULE:FREQ=WEEKLY;COUNT=40\r\n" +
		"EXDATE:20260119T080000Z,20260126T080000Z,20260302T080000Z\r\nEND:VEVENT\r\n")

	// Ten changed occurrences: moved an hour and renamed.
	for week := 5; week < 15; week++ {
		day := time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC).AddDate(0, 0, 7*week)
		series.WriteString("BEGIN:VEVENT\r\nUID:hostile-series@pimvault.invalid\r\nDTSTAMP:20260101T000000Z\r\n" +
			"RECURRENCE-ID:" + day.Format("20060102T150405Z") + "\r\n" +
			"DTSTART:" + day.Add(time.Hour).Format("20060102T150405Z") + "\r\n" +
			"DTEND:" + day.Add(2*time.Hour).Format("20060102T150405Z") + "\r\n" +
			"SUMMARY:Hostile weekly series (moved " + strconv.Itoa(week) + ")\r\nEND:VEVENT\r\n")
	}

	series.WriteString("END:VCALENDAR\r\n")

	return map[string]string{
		"hostileSeries": series.String(),
		"hostileFloating": "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//pimvault//hostile//EN\r\nBEGIN:VEVENT\r\n" +
			"UID:hostile-floating@pimvault.invalid\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260210T120000\r\n" +
			"DTEND:20260210T130000\r\nSUMMARY:Hostile floating time\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		"hostileAllDay": "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//pimvault//hostile//EN\r\nBEGIN:VEVENT\r\n" +
			"UID:hostile-allday@pimvault.invalid\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;VALUE=DATE:20260214\r\n" +
			"DTEND;VALUE=DATE:20260216\r\nSUMMARY:Hostile two-day event\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
	}
}

// summaryLine finds a restore or migrate summary's counts for a data type.
func summaryLine(kind string) *regexp.Regexp {
	return regexp.MustCompile(kind + `: (\d+) (?:written|to write), (\d+) already there, (\d+) skipped, (\d+) failed`)
}

// TestLive_HostileMailConverges migrates hostile mail from an archive into
// the Gmail test account twice: the first run writes what the account does
// not have, the second finds all of it (D184, D185). Outlook.com is not
// covered yet.
func TestLive_HostileMailConverges(t *testing.T) {
	h := signIn(t, store(t))

	sourceID, err := account.Parse(hostileAccount)
	require.NoError(t, err)

	source := archive.AccountDir(filepath.Join(h.home, "data", "pimvault"), sourceID)
	mailStore := archive.NewStore(filepath.Join(source, "mail"), ".eml")

	messages := 0

	for folder, items := range hostileMail() {
		for id, raw := range items {
			require.NoError(t, mailStore.Write(folder, id, []byte(raw)))
		}

		messages += len(items)
	}

	unique := messages - 1 // h12 is in two folders

	cals := archive.NewStore(filepath.Join(source, "calendars"), ".ics")
	for id, ics := range hostileEvents() {
		require.NoError(t, cals.Write(archive.FolderPath([]string{"Hostile series"}, []string{archive.Calendar}), id, []byte(ics)))
	}

	events := len(hostileEvents())

	for _, target := range h.ids {
		if target.Provider == account.Outlook {
			code, out := h.run(t, "migrate", "--dry-run", hostileAccount, target.String())
			require.Equal(t, cli.ExitOK, code, out)

			continue
		}

		code, out := h.run(t, "migrate", hostileAccount, target.String())
		require.Equal(t, cli.ExitOK, code, out)

		mail := counts(t, "mail", out)
		assert.Equal(t, unique, mail[0]+mail[1], "every message written or already there: %s", out)
		assert.Zero(t, mail[3], out)

		cal := counts(t, "calendars", out)
		assert.Equal(t, events, cal[0]+cal[1], "every event written or already there: %s", out)
		assert.Zero(t, cal[3], out)

		code, out = h.run(t, "migrate", hostileAccount, target.String())
		require.Equal(t, cli.ExitOK, code, out)
		assert.Equal(t, [4]int{0, unique, 0, 0}, counts(t, "mail", out), "the second run finds every message: %s", out)
		assert.Equal(t, [4]int{0, events, 0, 0}, counts(t, "calendars", out), "the second run finds every event: %s", out)
	}
}

// counts reads a data type's written, already there, skipped and failed
// counts from a restore or migrate summary.
func counts(t *testing.T, kind, out string) [4]int {
	t.Helper()

	m := summaryLine(kind).FindStringSubmatch(out)
	require.NotNil(t, m, out)

	var counts [4]int

	for i := range counts {
		n, err := strconv.Atoi(m[i+1])
		require.NoError(t, err)

		counts[i] = n
	}

	return counts
}
