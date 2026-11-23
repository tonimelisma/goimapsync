package gmail

import (
	"slices"
	"strings"

	"github.com/tonimelisma/pimvault/internal/archive"
)

// Folder returns the archive folder (a relative directory path) of a message
// with the given label IDs, following D31/D32: Inbox > Drafts > Sent > user
// labels > Archive. Among several user labels the one whose name sorts first
// wins. A nested label "a/b" is the folder a/b (D142). ok is false for
// messages in Spam or Trash, which are not backed up (D17).
func Folder(labelIDs []string, userLabels map[string]string) (folder string, ok bool) {
	has := func(id string) bool { return slices.Contains(labelIDs, id) }

	switch {
	case has(LabelSpam) || has(LabelTrash):
		return "", false
	case has(LabelInbox):
		return archive.Inbox, true
	case has(LabelDraft):
		return archive.Drafts, true
	case has(LabelSent):
		return archive.Sent, true
	}

	var names []string

	for _, id := range labelIDs {
		if name, isUser := userLabels[id]; isUser {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		return archive.Archive, true
	}

	return archive.FolderPath(strings.Split(slices.Min(names), "/"), archive.MailFolders()), true
}
