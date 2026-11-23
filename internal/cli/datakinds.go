package cli

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/tonimelisma/pimvault/internal/account"
	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gcal"
	"github.com/tonimelisma/pimvault/internal/gmail"
	"github.com/tonimelisma/pimvault/internal/gpeople"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/gtasks"
	"github.com/tonimelisma/pimvault/internal/httpretry"
	"github.com/tonimelisma/pimvault/internal/state"
)

// dataKind is one data type of an account: run backs it up and records its
// new state in the account's state; verify compares it with the archive
// without writing.
type dataKind struct {
	name   string
	run    func(st *state.Account) (backup.Result, error)
	verify func() (backup.Check, error)
}

// runner is what every data type's backup offers.
type runner[S any] interface {
	Run(ctx context.Context, prev *S) (backup.Result, *S, error)
	Verify(ctx context.Context) (backup.Check, error)
}

// kind wraps a data type's backup; slot picks its part of the state.
func kind[S any](ctx context.Context, name string, r runner[S], slot func(st *state.Account) **S) dataKind {
	return dataKind{
		name: name,
		run: func(st *state.Account) (backup.Result, error) {
			res, next, err := r.Run(ctx, *slot(st))
			keep(slot(st), next)

			return res, err
		},
		verify: func() (backup.Check, error) { return r.Verify(ctx) },
	}
}

// dataKinds returns an account's data types in summary order.
func (a *app) dataKinds(ctx context.Context, httpClient *http.Client, id account.ID, logger *slog.Logger) ([]dataKind, error) {
	store := func(part string) archive.Store { return a.store(id, part) }
	opts := a.backupOpts

	mail := func(st *state.Account) **state.GmailMail { return &st.GmailMail }
	outlookMail := func(st *state.Account) **state.OutlookMail { return &st.OutlookMail }
	calendars := func(st *state.Account) **state.Collections { return &st.Calendars }
	contacts := func(st *state.Account) **state.Collections { return &st.Contacts }
	tasks := func(st *state.Account) **state.Collections { return &st.Tasks }

	if id.Provider == account.Outlook {
		client := graph.New(httpClient, cmp.Or(a.rt.APIs.Graph, graph.DefaultBaseURL))

		return []dataKind{
			kind(ctx, partMail, &backup.OutlookMail{
				Client: client, Store: store(partMail), Logger: logger,
				Progress: a.progress(id, partMail),
				Options:  opts,
			}, outlookMail),
			kind(ctx, partCalendars, &backup.OutlookCalendars{
				Client: client, Store: store(partCalendars), Logger: logger, Owner: id.Email,
				Progress: a.progress(id, partCalendars),
				Options:  opts,
			}, calendars),
			kind(ctx, partContacts, &backup.OutlookContacts{
				Client: client, Store: store(partContacts), Logger: logger,
				Progress: a.progress(id, partContacts),
				Options:  opts,
			}, contacts),
			kind(ctx, partTasks, &backup.OutlookTasks{
				Client: client, Store: store(partTasks), Logger: logger,
				Progress: a.progress(id, partTasks),
				Options:  opts,
			}, tasks),
		}, nil
	}

	transport, err := plainTransport()
	if err != nil {
		return nil, err
	}

	// Photo URLs need no credentials, so photos get a client without them.
	photos := &http.Client{Transport: &httpretry.Transport{Base: transport, Logger: logger}}

	return []dataKind{
		kind(ctx, partMail, &backup.GmailMail{
			Client: gmail.New(httpClient, cmp.Or(a.rt.APIs.Gmail, gmail.DefaultBaseURL)), Store: store(partMail), Logger: logger,
			Progress: a.progress(id, partMail),
			Options:  opts,
		}, mail),
		kind(ctx, partCalendars, &backup.GoogleCalendars{
			Client: gcal.New(httpClient, cmp.Or(a.rt.APIs.Calendar, gcal.DefaultBaseURL)),
			Store:  store(partCalendars), Logger: logger, Owner: id.Email,
			Progress: a.progress(id, partCalendars),
			Options:  opts,
		}, calendars),
		kind(ctx, partContacts, &backup.GoogleContacts{
			Client: gpeople.New(httpClient, photos, cmp.Or(a.rt.APIs.People, gpeople.DefaultBaseURL)),
			Store:  store(partContacts), Logger: logger,
			Progress: a.progress(id, partContacts),
			Options:  opts,
		}, contacts),
		kind(ctx, partTasks, &backup.GoogleTasks{
			Client: gtasks.New(httpClient, cmp.Or(a.rt.APIs.Tasks, gtasks.DefaultBaseURL)), Store: store(partTasks), Logger: logger,
			Progress: a.progress(id, partTasks),
			Options:  opts,
		}, tasks),
	}, nil
}

// store returns one data type's store of an account, keeping deleted
// items aside under today's date (D181).
func (a *app) store(id account.ID, part string) archive.Store {
	aside := archive.KeptAside(a.paths.Archive, id, a.now())

	return archive.NewStore(filepath.Join(archive.AccountDir(a.paths.Archive, id), part), fileExt(part)).
		KeepingAside(filepath.Join(aside, part))
}

// progress returns the progress reporter for one data type of an account,
// or nil with --quiet.
func (a *app) progress(id account.ID, part string) backup.Progress {
	if a.quiet {
		return nil
	}

	unit := map[string]string{
		partMail:      "messages",
		partCalendars: "calendars",
		partContacts:  "address books",
		partTasks:     "task lists",
	}[part]

	return func(done, total int) { a.status.progress(id.String(), part, unit, done, total) }
}

// fileExt is the file extension of a data type's items (D7).
func fileExt(part string) string {
	switch part {
	case partMail:
		return ".eml"
	case partContacts:
		return ".vcf"
	default:
		return ".ics" // calendars and tasks
	}
}
