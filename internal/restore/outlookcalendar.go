package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/archive"
	"github.com/tonimelisma/pimvault/internal/backup"
	"github.com/tonimelisma/pimvault/internal/graph"
)

// Limits on expanding a series Outlook cannot represent (D63, D172): at
// most maxExpanded events, the latest ones, of at most maxIterations
// occurrences, and for a series without an end, none later than
// expandHorizon after the run.
const (
	maxExpanded   = 500
	maxIterations = 100000
	expandHorizon = 2 * 365 * day
)

// OutlookCalendar writes archived events into Outlook.com calendars.
// calendars/Calendar/ goes to the default calendar, other directories to
// calendars of the same name, created if missing (D152). A series is
// created with its recurrence, then canceled occurrences are deleted and
// changed ones updated; a recurrence Outlook cannot represent is expanded
// into single events (D63). An event is already there when the calendar has
// an event with its UID (D65) or one written from it by pimvault (D160).
// Owner is the account's email address.
type OutlookCalendar struct {
	Client   *graph.Client
	Source   archive.Store
	Owner    string
	Logger   *slog.Logger
	DryRun   bool
	Planned  Planned
	Progress backup.Progress
	// Now is the clock for D63's horizon; nil means time.Now.
	Now func() time.Time
}

// Run writes every archived event the account does not have yet.
func (o *OutlookCalendar) Run(ctx context.Context) (Result, error) {
	byFolder, err := itemsByFolder(o.Source)
	if err != nil {
		return Result{}, err
	}

	calendars, err := o.Client.Calendars(ctx)
	if err != nil {
		return Result{}, err
	}

	// Floating times are taken in the account's time zone (D171, D174). A
	// sign-in from before D174 cannot read it: they stay UTC.
	zone, err := o.Client.MailboxTimeZone(ctx)
	if err != nil {
		o.Logger.Warn("cannot read the account's time zone; floating times are written as UTC until you sign in again",
			slog.String("error", err.Error()))
	}

	zone = resolveZone(zone, "")

	defaultID, byName := "", map[string]string{}

	for _, c := range calendars {
		if c.IsDefaultCalendar {
			defaultID = c.ID
		} else if c.CanEdit {
			byName[c.Name] = c.ID
		}
	}

	var res Result

	folders := slices.Sorted(maps.Keys(byFolder))
	for i, folder := range folders {
		o.Progress.Report(i, len(folders))

		calendarID := defaultID
		if folder != archive.Calendar {
			calendarID = byName[listName(folder)]
		}

		if err := o.restoreCalendar(ctx, folder, byFolder[folder], calendarID, zone, &res); err != nil {
			o.Logger.Error("restore calendar", slog.String("calendar", folder), slog.String("error", err.Error()))
		}
	}

	o.Progress.Report(len(folders), len(folders))

	return res, nil
}

// restoreCalendar writes one calendar's events, creating the calendar when
// calendarID is empty. When the calendar fails as a whole, its events not
// yet tried count as failed.
func (o *OutlookCalendar) restoreCalendar(ctx context.Context, folder string, ids []string, calendarID, zone string, res *Result) error {
	uids := map[string]bool{}

	if calendarID != "" {
		existing, err := o.Client.ExistingEvents(ctx, calendarID)
		if err != nil {
			res.Failed += len(ids)

			return err
		}

		for _, e := range existing {
			uids[e.UID], uids[e.Source] = true, true
		}

		delete(uids, "")
	}

	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			res.Failed += len(ids) - i

			return fmt.Errorf("restore events: %w", err)
		}

		item := path.Join(folder, id)

		series, err := o.read(folder, id, zone)
		if err != nil {
			res.Failed++
			o.Logger.Error("restore event", slog.String("event", item), slog.String("error", err.Error()))

			continue
		}

		present := series.uid != "" && uids[series.uid]
		if present || o.DryRun {
			res.count(o.DryRun, o.Planned, item, present)

			continue
		}

		if calendarID == "" {
			if calendarID, err = o.Client.CreateCalendar(ctx, listName(folder)); err != nil {
				res.Failed += len(ids) - i

				return err
			}
		}

		if err := o.write(ctx, calendarID, series); err != nil {
			res.Failed++
			o.Logger.Error("restore event", slog.String("event", item), slog.String("error", err.Error()))

			continue
		}

		res.Written++
		uids[series.uid] = true
	}

	return nil
}

func (o *OutlookCalendar) read(folder, id, zone string) (*eventSeries, error) {
	raw, err := o.Source.Read(folder, id)
	if err != nil {
		return nil, err
	}

	return readSeries(raw, zone)
}

// seriesWrite is the events written for one series so far, deleted again
// when a later step fails, so the next run writes the series whole.
type seriesWrite struct {
	*OutlookCalendar

	calendarID string
	uid        string
	created    []string
}

func (o *OutlookCalendar) write(ctx context.Context, calendarID string, s *eventSeries) error {
	w := &seriesWrite{OutlookCalendar: o, calendarID: calendarID, uid: s.uid}

	if err := w.series(ctx, s); err != nil {
		for _, id := range w.created {
			err = errors.Join(err, gone(o.Client.DeleteEvent(ctx, id)))
		}

		return err
	}

	return nil
}

func (w *seriesWrite) series(ctx context.Context, s *eventSeries) error {
	if s.master == nil {
		for _, comp := range s.exceptions {
			if err := w.create(ctx, comp, nil); err != nil {
				return err
			}
		}

		return nil
	}

	rules, rdates := s.master.Props[ical.PropRecurrenceRule], s.master.Props[ical.PropRecurrenceDates]
	if len(rules) == 0 && len(rdates) == 0 {
		return w.create(ctx, s.master, nil)
	}

	start, err := instant(s.master.Props.Get(ical.PropDateTimeStart))
	if err != nil {
		return err
	}

	if len(rules) == 1 && len(rdates) == 0 {
		if rec, ok := graphRecurrence(rules[0].Value, start); ok {
			return w.recurring(ctx, s, rec)
		}
	}

	return w.expanded(ctx, s)
}

// create creates one event from a VEVENT, at start when not nil.
func (w *seriesWrite) create(ctx context.Context, comp *ical.Component, at *time.Time) error {
	e, err := outlookEvent(comp, w.Owner)
	if err != nil {
		return err
	}

	if at != nil {
		moveTo(e, *at)
	}

	e.SetSource(w.uid)

	id, err := w.Client.CreateEvent(ctx, w.calendarID, e)
	if err != nil {
		return err
	}

	w.created = append(w.created, id)

	return nil
}

// recurring creates a series, deletes its canceled occurrences and updates
// its changed ones.
func (w *seriesWrite) recurring(ctx context.Context, s *eventSeries, rec *graph.Recurrence) error {
	e, err := outlookEvent(s.master, w.Owner)
	if err != nil {
		return err
	}

	rec.Range.RecurrenceTimeZone = e.Start.TimeZone
	e.Recurrence = rec
	e.SetSource(w.uid)

	masterID, err := w.Client.CreateEvent(ctx, w.calendarID, e)
	if err != nil {
		return err
	}

	w.created = append(w.created, masterID)

	for _, t := range exdates(s.master) {
		occ, err := w.occurrence(ctx, masterID, t)
		if err != nil {
			return err
		}

		if occ != "" {
			if err := gone(w.Client.DeleteEvent(ctx, occ)); err != nil {
				return err
			}
		}
	}

	for _, comp := range s.exceptions {
		if err := w.exception(ctx, masterID, comp); err != nil {
			return err
		}
	}

	return nil
}

// exception updates the occurrence a changed instance replaces, or creates
// it as a single event when the series has no such occurrence.
func (w *seriesWrite) exception(ctx context.Context, masterID string, comp *ical.Component) error {
	original, err := instant(comp.Props.Get(ical.PropRecurrenceID))
	if err != nil {
		return err
	}

	occ, err := w.occurrence(ctx, masterID, original)
	if err != nil {
		return err
	}

	if occ == "" {
		return w.create(ctx, comp, nil)
	}

	e, err := outlookEvent(comp, w.Owner)
	if err != nil {
		return err
	}

	// The occurrence keeps the series' guests: changing them on an
	// occurrence of a draft meeting is not needed and could notify them.
	e.Attendees, e.IsDraft = nil, false

	return w.Client.UpdateEvent(ctx, occ, e)
}

// occurrence returns the ID of the series' occurrence originally starting
// at t, or "" when there is none.
func (w *seriesWrite) occurrence(ctx context.Context, masterID string, t time.Time) (string, error) {
	occurrences, err := w.Client.Occurrences(ctx, masterID, t.Add(-day), t.Add(day))
	if err != nil {
		return "", err
	}

	for _, occ := range occurrences {
		if start, err := time.Parse(time.RFC3339, strings.TrimSuffix(occ.OriginalStart, "Z")+"Z"); err == nil && start.Equal(t) {
			return occ.ID, nil
		}
	}

	return "", nil
}

// expanded writes a series Outlook cannot represent as single events, one
// per occurrence, with changed instances in place of theirs (D63).
func (w *seriesWrite) expanded(ctx context.Context, s *eventSeries) error {
	set, err := s.master.RecurrenceSet(time.UTC)
	if err != nil {
		return fmt.Errorf("expand event %s: %w", s.uid, err)
	}

	changed, err := changedInstances(s)
	if err != nil {
		return err
	}

	horizon := w.horizon(s.master)

	occurrences, capped := expandOccurrences(set.Iterator(), horizon)
	if capped {
		w.Logger.Warn("series too long to expand whole; some events left out", slog.String("event", s.uid), slog.Int("events", len(occurrences)))
	}

	for _, t := range occurrences {
		if comp, isChanged := changed[t.UTC()]; isChanged {
			delete(changed, t.UTC())

			if err := w.create(ctx, comp, nil); err != nil {
				return err
			}

			continue
		}

		if err := w.create(ctx, s.master, &t); err != nil {
			return err
		}
	}

	for moment, comp := range changed {
		// A changed instance of an occurrence the cap left out stays out.
		if capped && len(occurrences) > 0 && moment.Before(occurrences[0].UTC()) {
			continue
		}

		if err := w.create(ctx, comp, nil); err != nil {
			return err
		}
	}

	return nil
}

// expandOccurrences returns a series' occurrences up to horizon (zero for
// none), at most maxExpanded of them: the latest ones. capped reports that
// occurrences were left out.
func expandOccurrences(next func() (time.Time, bool), horizon time.Time) (out []time.Time, capped bool) {
	for range maxIterations {
		t, ok := next()
		if !ok || !horizon.IsZero() && t.After(horizon) {
			return out, capped
		}

		out = append(out, t)
		if len(out) > maxExpanded {
			out = out[1:]
			capped = true
		}
	}

	if t, more := next(); more && (horizon.IsZero() || !t.After(horizon)) {
		capped = true
	}

	return out, capped
}

// changedInstances maps a series' changed instances by the moment they
// replace, in UTC.
func changedInstances(s *eventSeries) (map[time.Time]*ical.Component, error) {
	changed := map[time.Time]*ical.Component{}

	for _, comp := range s.exceptions {
		t, err := instant(comp.Props.Get(ical.PropRecurrenceID))
		if err != nil {
			return nil, err
		}

		changed[t.UTC()] = comp
	}

	return changed, nil
}

// horizon is the latest occurrence expanded for a series without an end
// (D63), or zero for one with an end.
func (w *seriesWrite) horizon(master *ical.Component) time.Time {
	rule := propValue(master.Props, ical.PropRecurrenceRule)
	if strings.Contains(rule, "COUNT=") || strings.Contains(rule, "UNTIL=") {
		return time.Time{}
	}

	now := time.Now
	if w.Now != nil {
		now = w.Now
	}

	return now().Add(expandHorizon)
}

// moveTo moves an event to start at t, keeping its length and time zone.
func moveTo(e *graph.NewEvent, t time.Time) {
	start, err := time.Parse(graphDateTime, e.Start.DateTime)
	end, endErr := time.Parse(graphDateTime, e.End.DateTime)

	if err != nil || endErr != nil {
		return
	}

	loc, err := time.LoadLocation(e.Start.TimeZone)
	if err != nil {
		loc = time.UTC
	}

	local := t.In(loc)
	if e.IsAllDay {
		local = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}

	e.Start = &graph.DateTimeZone{DateTime: local.Format(graphDateTime), TimeZone: e.Start.TimeZone}
	e.End = &graph.DateTimeZone{DateTime: local.Add(end.Sub(start)).Format(graphDateTime), TimeZone: e.End.TimeZone}
}

// exdates returns the moments a series' EXDATEs cancel.
func exdates(master *ical.Component) []time.Time {
	var out []time.Time

	for _, p := range master.Props[ical.PropExceptionDates] {
		for v := range strings.SplitSeq(p.Value, ",") {
			one := p
			one.Value = v

			if t, err := instant(&one); err == nil {
				out = append(out, t)
			}
		}
	}

	return out
}
