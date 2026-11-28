// Package backup brings the archive in line with an account: it finds out
// where each item should be, compares that with the archive, and downloads,
// moves and deletes files to match (D11, D35, D44).
package backup

import (
	"maps"
	"slices"
)

// Kind is what an action does to one item file.
type Kind int

// Action kinds. Delete is for an item deleted in the account; its file is
// kept aside (D181). DropCopy removes an extra file of an item the archive
// keeps in another folder.
const (
	Download Kind = iota + 1
	Move
	Delete
	DropCopy
)

// Action is one change to the archive. Download uses To, Delete and
// DropCopy use From, Move uses both. Folders are relative paths inside the
// store.
type Action struct {
	Kind Kind
	ID   string
	From string
	To   string
}

// Plan compares where items should be (want: ID to folder, "" for an item
// that no longer exists) with the folders the archive holds them in (have).
// Items in refresh changed content and are downloaded again even when their
// file is in place. Items missing from want are left alone. Actions are
// ordered by ID.
func Plan(want map[string]string, have map[string][]string, refresh map[string]bool) []Action {
	var actions []Action

	for _, id := range slices.Sorted(maps.Keys(want)) {
		folder, held := want[id], have[id]

		switch {
		case folder == "":
			for _, f := range held {
				actions = append(actions, Action{Kind: Delete, ID: id, From: f})
			}
		case len(held) == 0:
			actions = append(actions, Action{Kind: Download, ID: id, To: folder})
		case refresh[id]:
			actions = append(actions, Action{Kind: Download, ID: id, To: folder})
			for _, f := range held {
				if f != folder {
					actions = append(actions, Action{Kind: DropCopy, ID: id, From: f})
				}
			}
		case slices.Contains(held, folder):
			for _, f := range held {
				if f != folder {
					actions = append(actions, Action{Kind: DropCopy, ID: id, From: f})
				}
			}
		default:
			actions = append(actions, Action{Kind: Move, ID: id, From: held[0], To: folder})
			for _, f := range held[1:] {
				actions = append(actions, Action{Kind: DropCopy, ID: id, From: f})
			}
		}
	}

	return actions
}
