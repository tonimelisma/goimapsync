package backup

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/gcal"
	"github.com/tonimelisma/pimvault/internal/googleapi"
	"github.com/tonimelisma/pimvault/internal/state"
)

// GoogleCalendars backs up one Google account's calendars: one directory
// per owned calendar, one .ics file per event series (D29, D34, D43).
type GoogleCalendars struct {
	Client *gcal.Client
	Store  archive.Store
	Logger *slog.Logger
	// Owner is the account's email address, used in email reminders.
	Owner string
	// Progress, if set, receives progress per collection.
	Progress Progress
	// Options are the run's flags.
	Options Options
}

// Run brings the calendars archive in line with the account, continuing
// from each calendar's sync token where it can (D146).
func (g *GoogleCalendars) Run(ctx context.Context, prev *state.Collections) (Result, *state.Collections, error) {
	src := &gcalSource{GoogleCalendars: g, calendars: make(map[string]*gcal.Calendar)}

	return runCollections(ctx, src, g.Store, g.Logger, g.Progress, g.Options, prev)
}

type gcalSource struct {
	*GoogleCalendars

	calendars map[string]*gcal.Calendar
}

func (s *gcalSource) collections(ctx context.Context) ([]collection, error) {
	cals, err := s.Client.Calendars(ctx)
	if err != nil {
		return nil, err
	}

	paths := make(map[string]string, len(cals))

	for i := range cals {
		c := &cals[i]
		if !c.Primary && !strings.EqualFold(c.DataOwner, s.Owner) {
			continue // shared with the account by its owner (D20)
		}

		s.calendars[c.ID] = c

		if c.Primary {
			paths[c.ID] = archive.Calendar
		} else {
			paths[c.ID] = archive.FolderPath([]string{c.Summary}, []string{archive.Calendar})
		}
	}

	var colls []collection
	for id, path := range archive.Unique(paths) {
		colls = append(colls, collection{ID: id, Path: path})
	}

	return colls, nil
}

func (s *gcalSource) full(ctx context.Context, c collection) (listing, error) {
	events, token, err := s.Client.Events(ctx, c.ID, "")
	if err != nil {
		return listing{}, err
	}

	groups := make(map[string][]gcal.Event)
	for i := range events {
		groups[events[i].SeriesID()] = append(groups[events[i].SeriesID()], events[i])
	}

	l := listing{items: make(map[string][]byte, len(groups)), token: token}

	for id, series := range groups {
		data, err := gcal.Encode(series, s.calendars[c.ID], s.Owner)
		if err != nil {
			l.fail(id)
			s.logFailure(c, id, err)

			continue
		}

		if data != nil {
			l.items[id] = data
		}
	}

	return l, nil
}

func (s *gcalSource) changes(ctx context.Context, c collection, token string) (listing, error) {
	events, next, err := s.Client.Events(ctx, c.ID, token)
	if errors.Is(err, gcal.ErrSyncExpired) {
		return listing{}, errTokenExpired
	}

	if err != nil {
		return listing{}, err
	}

	// The iCalendar UID of each changed series, where a change carried it.
	uids := make(map[string]string)
	for i := range events {
		if id := events[i].SeriesID(); uids[id] == "" {
			uids[id] = events[i].ICalUID
		}
	}

	l := listing{items: make(map[string][]byte, len(uids)), token: next}

	for id, uid := range uids {
		data, err := s.series(ctx, c, id, uid)
		if err != nil {
			l.fail(id)
			s.logFailure(c, id, err)

			continue
		}

		l.items[id] = data // nil when the series is gone
	}

	return l, nil
}

// series renders a whole event series again after one of its events
// changed: the recurring event and all its instances are read by UID.
func (s *gcalSource) series(ctx context.Context, c collection, id, uid string) ([]byte, error) {
	if uid == "" {
		// Deleted events often come without their UID; the series' own
		// event has it unless the whole series is gone.
		e, err := s.Client.Event(ctx, c.ID, id)
		if gone(err) {
			return nil, nil
		}

		if err != nil {
			return nil, err
		}

		if e.ICalUID == "" {
			return nil, nil
		}

		uid = e.ICalUID
	}

	events, err := s.Client.Series(ctx, c.ID, uid)
	if err != nil {
		return nil, err
	}

	var series []gcal.Event

	for i := range events {
		if events[i].SeriesID() == id {
			series = append(series, events[i])
		}
	}

	return gcal.Encode(series, s.calendars[c.ID], s.Owner)
}

func (s *gcalSource) logFailure(c collection, id string, err error) {
	s.Logger.Error("back up event", slog.String("calendar", c.Path), slog.String("event", id), slog.String("error", err.Error()))
}

// gone reports whether an event lookup failed because the event no longer
// exists: 404 Not Found or 410 Gone.
func gone(err error) bool {
	var apiErr *googleapi.APIError

	return errors.Is(err, googleapi.ErrNotFound) || (errors.As(err, &apiErr) && apiErr.Status == http.StatusGone)
}
