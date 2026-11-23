package backup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tonimelisma/pimvault/internal/backup"
)

func TestPlan(t *testing.T) {
	want := map[string]string{
		"new":        "Inbox",
		"same":       "Inbox",
		"moved":      "Archive",
		"gone":       "",
		"gone-twice": "",
		"dup-kept":   "Sent",
		"dup-moved":  "Work",
		"never":      "",
	}
	have := map[string][]string{
		"same":       {"Inbox"},
		"moved":      {"Inbox"},
		"gone":       {"Sent"},
		"gone-twice": {"A", "B"},
		"dup-kept":   {"Inbox", "Sent"},
		"dup-moved":  {"A", "B"},
		"untouched":  {"Inbox"},
	}

	assert.Equal(t, []backup.Action{
		{Kind: backup.DropCopy, ID: "dup-kept", From: "Inbox"},
		{Kind: backup.Move, ID: "dup-moved", From: "A", To: "Work"},
		{Kind: backup.DropCopy, ID: "dup-moved", From: "B"},
		{Kind: backup.Delete, ID: "gone", From: "Sent"},
		{Kind: backup.Delete, ID: "gone-twice", From: "A"},
		{Kind: backup.Delete, ID: "gone-twice", From: "B"},
		{Kind: backup.Move, ID: "moved", From: "Inbox", To: "Archive"},
		{Kind: backup.Download, ID: "new", To: "Inbox"},
	}, backup.Plan(want, have, nil))
}

func TestPlan_Empty(t *testing.T) {
	assert.Empty(t, backup.Plan(nil, map[string][]string{"x": {"Inbox"}}, nil))
}

func TestPlan_Refresh(t *testing.T) {
	want := map[string]string{"draft": "Drafts", "moved": "Drafts", "new": "Drafts", "same": "Inbox"}
	have := map[string][]string{"draft": {"Drafts"}, "moved": {"Inbox", "Old"}, "same": {"Inbox"}}
	refresh := map[string]bool{"draft": true, "moved": true, "new": true}

	assert.Equal(t, []backup.Action{
		{Kind: backup.Download, ID: "draft", To: "Drafts"},
		{Kind: backup.Download, ID: "moved", To: "Drafts"},
		{Kind: backup.DropCopy, ID: "moved", From: "Inbox"},
		{Kind: backup.DropCopy, ID: "moved", From: "Old"},
		{Kind: backup.Download, ID: "new", To: "Drafts"},
	}, backup.Plan(want, have, refresh))
}
