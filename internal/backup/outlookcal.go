package backup

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/graph"
	"github.com/tonimelisma/pimvault/internal/state"
)

// OutlookCalendars backs up one Outlook.com account's calendars: one
// directory per calendar, one .ics file per event series (D29, D34, D43).
// Graph's change tracking does not follow series (F6), so every calendar is
// listed in full on every run (D53); files are only rewritten when their
// content changed.
type OutlookCalendars struct {
	Client *graph.Client
	Store  archive.Store
	Logger *slog.Logger
	// Owner is the account's email address.
	Owner string
	// Progress, if set, receives progress per collection.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// Run brings the calendars archive in line with the account.
func (o *OutlookCalendars) Run(ctx context.Context, prev *state.Collections) (Result, *state.Collections, error) {
	return runCollections(ctx, &outlookCalSource{o}, o.Store, o.Logger, o.Progress, o.Options, prev)
}

type outlookCalSource struct {
	*OutlookCalendars
}

// collections returns the default calendar and the other calendars the
// account owns and can write to: not calendars shared with it (D20) or
// subscribed to (D21), and not the fixed calendars Outlook generates, such
// as Birthdays (D148), which cannot be removed. The owner is the default
// calendar's owner, which may be an alias of the sign-in address.
func (s *outlookCalSource) collections(ctx context.Context) ([]collection, error) {
	cals, err := s.Client.Calendars(ctx)
	if err != nil {
		return nil, err
	}

	owner := s.Owner

	for i := range cals {
		if cals[i].IsDefaultCalendar && cals[i].Owner.Address != "" {
			owner = cals[i].Owner.Address
		}
	}

	paths := make(map[string]string, len(cals))

	for i := range cals {
		c := &cals[i]

		switch {
		case c.IsDefaultCalendar:
			paths[c.ID] = archive.Calendar
		case !c.CanEdit || !c.IsRemovable || !strings.EqualFold(c.Owner.Address, owner):
			continue
		default:
			paths[c.ID] = archive.FolderPath([]string{c.Name}, []string{archive.Calendar})
		}
	}

	colls := make([]collection, 0, len(paths))
	for id, path := range archive.Unique(paths) {
		colls = append(colls, collection{ID: id, Path: path})
	}

	return colls, nil
}

func (s *outlookCalSource) full(ctx context.Context, c collection) (listing, error) {
	events, err := s.Client.Events(ctx, c.ID)
	if err != nil {
		return listing{}, err
	}

	l := listing{items: make(map[string][]byte, len(events))}

	for i := range events {
		e := &events[i]

		data, err := s.render(ctx, e)
		if err != nil {
			l.fail(e.ID)
			s.Logger.Error("back up event", slog.String("calendar", c.Path), slog.String("event", e.ID), slog.String("error", err.Error()))

			continue
		}

		l.items[e.ID] = data
	}

	return l, nil
}

// changes is never called: collections have no token, so each run lists
// them in full.
func (s *outlookCalSource) changes(ctx context.Context, c collection, _ string) (listing, error) {
	return s.full(ctx, c)
}

// render reads a series' exceptions and the attachments it needs and
// renders the event's file.
func (s *outlookCalSource) render(ctx context.Context, e *graph.Event) ([]byte, error) {
	var (
		exceptions []graph.Event
		canceled   []string
		err        error
	)

	if e.Type == "seriesMaster" {
		exceptions, canceled, err = s.Client.Series(ctx, e.ID)
		if err != nil {
			return nil, err
		}
	}

	attachments := make(map[string][]graph.Attachment)

	events := append([]graph.Event{*e}, exceptions...)

	for i := range events {
		if !events[i].HasAttachments {
			continue
		}

		list, err := s.Client.Attachments(ctx, events[i].ID)
		if err != nil {
			return nil, err
		}

		attachments[events[i].ID] = list
	}

	return graph.EncodeSeries(e, exceptions, canceled, attachments)
}
