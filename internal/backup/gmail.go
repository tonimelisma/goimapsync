package backup

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gmail"
	"github.com/tonimelisma/pimvault/internal/googleapi"
	"github.com/tonimelisma/pimvault/internal/state"
)

// GmailMail backs up one Gmail account's mail.
type GmailMail struct {
	Client *gmail.Client
	Store  archive.Store
	Logger *slog.Logger
	// Progress, if set, receives download progress.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// gmailRun is the working state of one Run.
type gmailRun struct {
	*GmailMail
	mailSync
}

// Run brings the mail archive in line with the account and returns the state
// to continue from next time. It continues from prev when prev still matches
// the account and the archive, and lists everything otherwise. Failed items
// are counted, logged and kept in the state for the next run. An error means
// the account could not be backed up at all; the counts so far are returned
// with it.
func (g *GmailMail) Run(ctx context.Context, prev *state.GmailMail) (Result, *state.GmailMail, error) {
	gone := func(err error) bool { return errors.Is(err, googleapi.ErrNotFound) }
	r := &gmailRun{GmailMail: g, mailSync: newMailSync(g.Store, g.Logger, g.Options, g.Progress,
		mailProvider{raw: g.Client.Raw, gone: gone, size: g.Client.Size})}

	next, err := r.run(ctx, prev)

	return r.res, next, err
}

func (r *gmailRun) run(ctx context.Context, prev *state.GmailMail) (*state.GmailMail, error) {
	if !r.Options.DryRun {
		if err := r.Store.CleanTemp(); err != nil {
			r.Logger.Warn(err.Error())
		}
	}

	labels, err := r.Client.UserLabels(ctx)
	if err != nil {
		return nil, err
	}

	have, err := r.Store.Scan()
	if err != nil {
		return nil, err
	}

	want, next, err := r.changes(ctx, prev, labels, have)
	if err != nil {
		return nil, err
	}

	if applyErr := r.apply(ctx, want, have, nil); applyErr != nil {
		return nil, applyErr
	}

	retry, err := r.finish()
	if err != nil {
		return nil, err
	}

	if r.Options.DryRun {
		r.res.Files = len(have)

		return nil, nil //nolint:nilnil // a dry run keeps the state as it was.
	}

	return &state.GmailMail{
		HistoryID: next,
		Labels:    labels,
		Archive:   r.Store.Root(),
		Files:     r.res.Files,
		// Withheld deletions are looked at again next time (D182).
		Retry: slices.Compact(slices.Sorted(slices.Values(append(retry, r.withheldIDs...)))),
	}, nil
}

// changes returns where messages belong (want: ID to folder, "" for gone)
// and the history ID to store afterwards. It reads Gmail's history when prev
// is usable and lists the whole mailbox otherwise; after a full listing,
// archive files of messages not listed are marked gone.
func (r *gmailRun) changes(
	ctx context.Context, prev *state.GmailMail, labels map[string]string, have map[string][]string,
) (want map[string]string, next string, err error) {
	reason := fullListingReason(prev, labels, r.Store.Root(), len(have))
	if reason == "" {
		changed, latest, historyErr := r.Client.History(ctx, prev.HistoryID)
		if historyErr == nil {
			ids := slices.Compact(slices.Sorted(slices.Values(append(changed, prev.Retry...))))

			return r.changedFolders(ctx, ids, labels), latest, nil
		}

		if !errors.Is(historyErr, gmail.ErrHistoryExpired) {
			return nil, "", historyErr
		}

		reason = "change history expired"
	}

	r.Logger.Info("listing all mail", slog.String("reason", reason))

	want, next, err = r.allFolders(ctx, labels)
	if err != nil {
		return nil, "", err
	}

	markUnlisted(want, have)

	return want, next, nil
}

// fullListingReason says why prev cannot be continued from, or "" if it can.
func fullListingReason(prev *state.GmailMail, labels map[string]string, root string, files int) string {
	if prev == nil || prev.HistoryID == "" {
		return reasonFirstBackup
	}

	if reason := driftReason(prev.Archive, prev.Files, root, files); reason != "" {
		return reason
	}

	if !maps.Equal(prev.Labels, labels) {
		return "labels changed"
	}

	return ""
}

// changedFolders looks up the current folder of each changed message.
func (r *gmailRun) changedFolders(ctx context.Context, ids []string, labels map[string]string) map[string]string {
	want := make(map[string]string, len(ids))

	for _, id := range ids {
		if !r.validID(id) {
			continue
		}

		labelIDs, err := r.Client.MessageLabels(ctx, id)
		if errors.Is(err, googleapi.ErrNotFound) {
			want[id] = ""
			continue
		}

		if err != nil {
			r.fail(id, err)
			continue
		}

		want[id] = folderOrGone(labelIDs, labels)
	}

	return want
}

// allFolders lists every message and derives its folder from the label
// lists it appears in. The history ID is read first, so changes made while
// listing are picked up by the next run.
func (r *gmailRun) allFolders(ctx context.Context, labels map[string]string) (map[string]string, string, error) {
	next, err := r.Client.HistoryID(ctx)
	if err != nil {
		return nil, "", err
	}

	folders, err := listFolders(ctx, r.Client, labels)
	if err != nil {
		return nil, "", err
	}

	want := make(map[string]string, len(folders))

	for id, folder := range folders {
		if r.validID(id) {
			want[id] = folder
		}
	}

	return want, next, nil
}

// listFolders lists every message and derives its folder ("" for one that
// is not backed up) from the label lists it appears in.
func listFolders(ctx context.Context, client *gmail.Client, labels map[string]string) (map[string]string, error) {
	all, err := client.MessageIDs(ctx, "")
	if err != nil {
		return nil, err
	}

	memberOf := make(map[string][]string)
	listed := append([]string{gmail.LabelInbox, gmail.LabelDraft, gmail.LabelSent}, slices.Sorted(maps.Keys(labels))...)

	for _, labelID := range listed {
		ids, err := client.MessageIDs(ctx, labelID)
		if err != nil {
			return nil, err
		}

		for _, id := range ids {
			memberOf[id] = append(memberOf[id], labelID)
		}
	}

	folders := make(map[string]string, len(all))
	for _, id := range all {
		folders[id] = folderOrGone(memberOf[id], labels)
	}

	return folders, nil
}

// folderOrGone is the archive folder for a message, or "" for one that is not
// backed up.
func folderOrGone(labelIDs []string, labels map[string]string) string {
	folder, ok := gmail.Folder(labelIDs, labels)
	if !ok {
		return ""
	}

	return folder
}
