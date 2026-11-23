package main

import (
	"fmt"
	"time"
)

// Sides of a thread: which test mailboxes hold it.
const (
	sideGmail   = 1
	sideOutlook = 2
	sideBoth    = sideGmail | sideOutlook
)

// thread is a conversation. Messages alternate between the two parties.
type thread struct {
	Side     int
	Messages []*message
	// Folder is the user label or folder index the thread goes in instead
	// of the inbox, or -1.
	Folder int
	// Archived threads are in no folder: Gmail's All Mail only, Outlook's
	// Archive folder.
	Archived bool
}

var (
	gmailSelf   = person{Given: "Gmail", Family: "Tester"}
	outlookSelf = person{Given: "Outlook", Family: "Tester"}
)

// userFolders are the labels and folders created in both accounts. A slash
// nests in Gmail; in Outlook the first one gets a child folder instead.
var userFolders = []string{"Seed Projects", "Receipts", "Ünïcödé 名前", "Travel 2026"}

// threads plans n conversations, starting up to 200 days ago.
func (s *seeder) threads(n int) []thread {
	out := make([]thread, 0, n)

	for i := range n {
		side := pick(s, []int{sideBoth, sideBoth, sideGmail, sideOutlook})

		var a, b person

		var aAddr, bAddr string

		other := pick(s, people)

		switch side {
		case sideBoth:
			a, aAddr, b, bAddr = gmailSelf, s.gAddr, outlookSelf, s.oAddr
		case sideGmail:
			a, aAddr, b, bAddr = other, other.Email(), gmailSelf, s.gAddr
		default:
			a, aAddr, b, bAddr = other, other.Email(), outlookSelf, s.oAddr
		}

		subject := s.subject()
		date := s.now.Add(-time.Duration(s.rng.IntN(200*24)) * time.Hour)
		count := 1 + s.rng.IntN(6)

		t := thread{Side: side, Folder: -1}
		switch {
		case s.chance(0.4):
			t.Folder = s.rng.IntN(len(userFolders))
		case s.chance(0.2):
			t.Archived = true
		}

		var refs []string

		for j := range count {
			m := &message{
				From: a, FromAddr: aAddr, To: b, ToAddr: bAddr,
				Subject: subject,
				Text:    s.body(),
				HTML:    s.chance(0.5),
				Date:    date,
				ID:      fmt.Sprintf("<seed-%s-%d-%d@pimvault.example>", s.tag, i, j),
				Unread:  s.chance(0.3),
				Flag:    s.chance(0.1),
			}

			if j > 0 {
				m.Subject = "Re: " + subject
				m.InReplyTo = refs[len(refs)-1]
				m.References = append([]string(nil), refs...)
			}

			if s.chance(0.15) {
				m.Cc = []person{pick(s, people)}
			}

			m.Attachments = s.attachments(m.HTML)
			refs = append(refs, m.ID)
			t.Messages = append(t.Messages, m)

			a, aAddr, b, bAddr = b, bAddr, a, aAddr
			date = date.Add(time.Duration(10+s.rng.IntN(48*60)) * time.Minute)
			if latest := s.now.Add(-time.Duration(count-j) * time.Minute); date.After(latest) {
				date = latest
			}
		}

		out = append(out, t)
	}

	return out
}
