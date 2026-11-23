package restore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/gcal"
)

// primaryCalendar is the Calendar API's name for the account's own
// calendar.
const primaryCalendar = "primary"

// GoogleCalendar writes archived events into Google Calendar as private
// copies, without notifying anyone (D62). calendars/Calendar/ goes to the
// primary calendar, other directories to calendars of the same name,
// created if missing (D152). An event (a series with its changed
// instances) is already there when the calendar has an event with its UID
// (D65). Owner is the account's email address.
type GoogleCalendar struct {
	Client   *gcal.Client
	Source   archive.Store
	Owner    string
	Logger   *slog.Logger
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
}

// Run writes every archived event the account does not have yet.
func (g *GoogleCalendar) Run(ctx context.Context) (Result, error) {
	byFolder, err := itemsByFolder(g.Source)
	if err != nil {
		return Result{}, err
	}

	calendars, err := g.Client.Calendars(ctx)
	if err != nil {
		return Result{}, err
	}

	byName := map[string]string{} // calendars other than the primary one
	zones := map[string]string{}  // calendar ID → time zone, for floating times (D171)

	for _, c := range calendars {
		if c.Primary {
			zones[primaryCalendar] = resolveZone(c.TimeZone, "")
		} else {
			byName[c.Summary] = c.ID
			zones[c.ID] = resolveZone(c.TimeZone, "")
		}
	}

	var res Result

	folders := slices.Sorted(maps.Keys(byFolder))
	for i, folder := range folders {
		g.Progress.Report(i, len(folders))

		calendarID := primaryCalendar
		if folder != archive.Calendar {
			calendarID = byName[listName(folder)]
		}

		zone := cmp.Or(zones[calendarID], zones[primaryCalendar])
		if err := g.restoreCalendar(ctx, folder, byFolder[folder], calendarID, zone, &res); err != nil {
			g.Logger.Error("restore calendar", slog.String("calendar", folder), slog.String("error", err.Error()))
		}
	}

	g.Progress.Report(len(folders), len(folders))

	return res, nil
}

// restoreCalendar writes one calendar's events, creating the calendar when
// calendarID is empty; floating times are taken in zone. When the calendar
// fails as a whole, its events not yet tried count as failed.
func (g *GoogleCalendar) restoreCalendar(ctx context.Context, folder string, ids []string, calendarID, zone string, res *Result) error {
	uids := map[string]bool{}

	if calendarID != "" {
		var err error
		if uids, err = g.Client.UIDs(ctx, calendarID); err != nil {
			res.Failed += len(ids)

			return err
		}
	}

	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			res.Failed += len(ids) - i

			return fmt.Errorf("restore events: %w", err)
		}

		item := path.Join(folder, id)

		series, err := g.read(folder, id, zone)
		if err != nil {
			res.Failed++
			g.Logger.Error("restore event", slog.String("event", item), slog.String("error", err.Error()))

			continue
		}

		present := uids[series.uid]
		if present || g.DryRun {
			res.count(g.DryRun, g.Planned, item, present)

			continue
		}

		if calendarID == "" {
			if calendarID, err = g.Client.CreateCalendar(ctx, listName(folder)); err != nil {
				res.Failed += len(ids) - i

				return err
			}
		}

		if err := g.write(ctx, calendarID, series); err != nil {
			res.Failed++
			g.Logger.Error("restore event", slog.String("event", item), slog.String("error", err.Error()))

			continue
		}

		res.Written++
		uids[series.uid] = true
	}

	return nil
}

func (g *GoogleCalendar) read(folder, id, zone string) (*eventSeries, error) {
	raw, err := g.Source.Read(folder, id)
	if err != nil {
		return nil, err
	}

	return readSeries(raw, zone)
}

// write imports a series: the event, then its changed instances. When one
// fails, what was imported is deleted again, so the next run writes the
// series whole.
func (g *GoogleCalendar) write(ctx context.Context, calendarID string, s *eventSeries) error {
	var imported []string

	err := g.importSeries(ctx, calendarID, s, &imported)
	if err == nil {
		return nil
	}

	if s.master != nil && len(imported) > 0 {
		imported = imported[:1] // deleting the series deletes its instances
	}

	for _, id := range imported {
		err = errors.Join(err, gone(g.Client.DeleteEvent(ctx, calendarID, id)))
	}

	return err
}

func (g *GoogleCalendar) importSeries(ctx context.Context, calendarID string, s *eventSeries, imported *[]string) error {
	comps := s.exceptions
	if s.master != nil {
		comps = append([]*ical.Component{s.master}, s.exceptions...)
	}

	for _, comp := range comps {
		e, err := googleEvent(comp, g.Owner)
		if err != nil {
			return err
		}

		id, err := g.Client.ImportEvent(ctx, calendarID, e)
		if err != nil {
			return err
		}

		*imported = append(*imported, id)
	}

	return nil
}
