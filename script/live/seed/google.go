package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	gmailAPI    = "https://gmail.googleapis.com/gmail/v1/users/me"
	calendarAPI = "https://www.googleapis.com/calendar/v3"
	peopleAPI   = "https://people.googleapis.com/v1"
	tasksAPI    = "https://tasks.googleapis.com/tasks/v1"
)

func (s *seeder) seedGmail(ctx context.Context) error {
	return errors.Join(
		s.gmailMail(ctx),
		s.googleCalendar(ctx),
		s.googleContacts(ctx),
		s.googleTasks(ctx),
	)
}

// gmailLabels creates the seed labels (or finds them) and returns their IDs
// by userFolders index.
func (s *seeder) gmailLabels(ctx context.Context) ([]string, error) {
	var existing struct {
		Labels []struct{ ID, Name string } `json:"labels"`
	}

	if err := call(ctx, s.gmail, http.MethodGet, gmailAPI+"/labels", nil, &existing); err != nil {
		return nil, err
	}

	byName := map[string]string{}
	for _, l := range existing.Labels {
		byName[l.Name] = l.ID
	}

	names := append([]string{}, userFolders...)
	names[0] += "/Alpha" // a nested label; its parent is created too

	ids := make([]string, len(names))

	for i, name := range append([]string{userFolders[0]}, names...) {
		id, ok := byName[name]
		if !ok {
			var created struct{ ID string }
			if err := call(ctx, s.gmail, http.MethodPost, gmailAPI+"/labels", map[string]string{"name": name}, &created); err != nil {
				return nil, err
			}

			id = created.ID
		}

		if i > 0 {
			ids[i-1] = id
		}
	}

	return ids, nil
}

func (s *seeder) gmailMail(ctx context.Context) error {
	labels, err := s.gmailLabels(ctx)
	if err != nil {
		return s.step("gmail labels", 0, []error{err})
	}

	var (
		n    int
		errs []error
	)

	for _, t := range s.mail {
		if t.Side&sideGmail == 0 {
			continue
		}

		threadID := ""

		for _, m := range t.Messages {
			var ids []string

			switch {
			case m.FromAddr == s.gAddr:
				ids = append(ids, "SENT")
			case t.Folder < 0 && !t.Archived:
				ids = append(ids, "INBOX")
			}

			if t.Folder >= 0 {
				ids = append(ids, labels[t.Folder])
			}

			if m.Unread && m.FromAddr != s.gAddr {
				ids = append(ids, "UNREAD")
			}

			if m.Flag {
				ids = append(ids, "STARRED")
			}

			body := map[string]any{"raw": base64.URLEncoding.EncodeToString(m.mime()), "labelIds": ids}
			if threadID != "" {
				body["threadId"] = threadID
			}

			var inserted struct {
				ThreadID string `json:"threadId"`
			}

			if err := call(ctx, s.gmail, http.MethodPost, gmailAPI+"/messages?internalDateSource=dateHeader", body, &inserted); err != nil {
				errs = append(errs, err)

				continue
			}

			threadID = inserted.ThreadID
			n++
		}
	}

	return s.step("gmail messages", n, errs)
}

// googleTime is a Calendar API start or end.
func googleTime(t time.Time, zone string) map[string]string {
	return map[string]string{"dateTime": t.Format(time.RFC3339), "timeZone": zone}
}

func googleDate(t time.Time) map[string]string {
	return map[string]string{"date": t.Format(time.DateOnly)}
}

var zones = []string{"Europe/Helsinki", "America/New_York", "Asia/Tokyo", "UTC", "Australia/Adelaide"}

var eventTitles = []string{
	"Dentist", "Team sync", "1:1 with Aino", "Flight AY123 HEL→NRT", "Sauna evening", "Budget review",
	"Kirjasto: palautus", "歯医者", "Yoga", "Conference: GopherCon", "Call with Acme; contract", "Lunch",
}

// localTime returns a random hour on a day offset from now, in zone.
func (s *seeder) localTime(days int, zone string) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}

	d := s.now.In(loc).AddDate(0, 0, days)

	return time.Date(d.Year(), d.Month(), d.Day(), 8+s.rng.IntN(10), 15*s.rng.IntN(4), 0, 0, loc)
}

// googleEvent is a random single event.
func (s *seeder) googleEvent(days int) map[string]any {
	zone := pick(s, zones)
	start := s.localTime(days, zone)
	e := map[string]any{
		"summary": pick(s, eventTitles) + " " + s.tag,
		"start":   googleTime(start, zone),
		"end":     googleTime(start.Add(time.Duration(30+30*s.rng.IntN(4))*time.Minute), zone),
	}

	if s.chance(0.5) {
		e["description"] = s.body()
	}

	if s.chance(0.4) {
		e["location"] = pick(s, people).City + ", " + pick(s, people).Country
	}

	if s.chance(0.3) {
		e["reminders"] = map[string]any{"useDefault": false, "overrides": []map[string]any{
			{"method": "popup", "minutes": 10}, {"method": "email", "minutes": 60 * 24},
		}}
	}

	if s.chance(0.2) {
		e["transparency"] = "transparent"
	}

	if s.chance(0.1) {
		e["visibility"] = "private"
	}

	return e
}

func (s *seeder) googleCalendar(ctx context.Context) error {
	var (
		n    int
		errs []error
	)

	add := func(calendar string, e map[string]any) string {
		var created struct{ ID string }

		err := call(ctx, s.gmail, http.MethodPost, calendarAPI+"/calendars/"+url.PathEscape(calendar)+"/events?sendUpdates=none", e, &created)
		if err != nil {
			errs = append(errs, err)

			return ""
		}

		n++

		return created.ID
	}

	for range 40 {
		add("primary", s.googleEvent(s.rng.IntN(240)-120))
	}

	for range 8 {
		start := s.now.AddDate(0, 0, s.rng.IntN(240)-120)
		add("primary", map[string]any{
			"summary": "Holiday " + s.tag, "start": googleDate(start), "end": googleDate(start.AddDate(0, 0, 1+s.rng.IntN(5))),
		})
	}

	attended := s.googleEvent(7)
	attended["summary"] = "Planning with Outlook tester " + s.tag
	attended["attendees"] = []map[string]string{{"email": s.oAddr}}
	add("primary", attended)

	s.googleSeries(ctx, add, &errs)

	var team struct{ ID string }
	if err := call(ctx, s.gmail, http.MethodPost, calendarAPI+"/calendars",
		map[string]string{"summary": "Seed Team " + s.tag, "timeZone": "Europe/Helsinki"}, &team); err != nil {
		errs = append(errs, err)
	} else {
		for range 10 {
			add(team.ID, s.googleEvent(s.rng.IntN(60)-30))
		}
	}

	return s.step("google calendar events", n, errs)
}

// googleSeries adds recurring events, then moves one instance and cancels
// another of the weekly one.
func (s *seeder) googleSeries(ctx context.Context, add func(string, map[string]any) string, errs *[]error) {
	weeklyStart := s.localTime(-21, "Europe/Helsinki")
	weekly := map[string]any{
		"summary":    "Weekly standup " + s.tag,
		"start":      googleTime(weeklyStart, "Europe/Helsinki"),
		"end":        googleTime(weeklyStart.Add(15*time.Minute), "Europe/Helsinki"),
		"recurrence": []string{"RRULE:FREQ=WEEKLY;COUNT=10"},
	}
	daily := s.googleEvent(-3)
	daily["summary"] = "Daily pill " + s.tag
	daily["recurrence"] = []string{"RRULE:FREQ=DAILY;COUNT=14"}
	monthly := s.googleEvent(-40)
	monthly["summary"] = "Book club " + s.tag
	monthly["recurrence"] = []string{"RRULE:FREQ=MONTHLY;BYDAY=2TU;UNTIL=" + s.now.AddDate(1, 0, 0).Format("20060102") + "T000000Z"}
	yearly := map[string]any{
		"summary": "Anniversary " + s.tag, "start": googleDate(s.now.AddDate(0, 0, -100)), "end": googleDate(s.now.AddDate(0, 0, -99)),
		"recurrence": []string{"RRULE:FREQ=YEARLY"},
	}

	for _, e := range []map[string]any{daily, monthly, yearly} {
		add("primary", e)
	}

	id := add("primary", weekly)
	if id == "" {
		return
	}

	var instances struct {
		Items []struct {
			ID    string            `json:"id"`
			Start map[string]string `json:"start"`
		} `json:"items"`
	}

	base := calendarAPI + "/calendars/primary/events/"
	if err := call(ctx, s.gmail, http.MethodGet, base+url.PathEscape(id)+"/instances?maxResults=10", nil, &instances); err != nil {
		*errs = append(*errs, err)

		return
	}

	if len(instances.Items) < 4 {
		*errs = append(*errs, fmt.Errorf("weekly series has %d instances", len(instances.Items)))

		return
	}

	moved, err := time.Parse(time.RFC3339, instances.Items[1].Start["dateTime"])
	if err != nil {
		*errs = append(*errs, err)

		return
	}

	moved = moved.Add(26 * time.Hour)
	patch := map[string]any{
		"summary": "Weekly standup (moved) " + s.tag,
		"start":   googleTime(moved, "Europe/Helsinki"), "end": googleTime(moved.Add(30*time.Minute), "Europe/Helsinki"),
	}

	if err := call(ctx, s.gmail, http.MethodPatch, base+url.PathEscape(instances.Items[1].ID)+"?sendUpdates=none", patch, nil); err != nil {
		*errs = append(*errs, err)
	}

	if err := call(ctx, s.gmail, http.MethodDelete, base+url.PathEscape(instances.Items[3].ID)+"?sendUpdates=none", nil, nil); err != nil {
		*errs = append(*errs, err)
	}
}

// googleGroups creates the seed contact labels and returns their resource
// names.
func (s *seeder) googleGroups(ctx context.Context) ([]string, error) {
	names := []string{"Seed Friends", "Seed Work", "Perhe 👪"}
	out := make([]string, 0, len(names))

	var existing struct {
		ContactGroups []struct{ ResourceName, Name string } `json:"contactGroups"`
	}

	if err := call(ctx, s.gmail, http.MethodGet, peopleAPI+"/contactGroups?pageSize=1000", nil, &existing); err != nil {
		return nil, err
	}

	for _, name := range names {
		found := ""

		for _, g := range existing.ContactGroups {
			if g.Name == name {
				found = g.ResourceName
			}
		}

		if found == "" {
			var created struct{ ResourceName string }
			if err := call(ctx, s.gmail, http.MethodPost, peopleAPI+"/contactGroups",
				map[string]any{"contactGroup": map[string]string{"name": name}}, &created); err != nil {
				return nil, err
			}

			found = created.ResourceName
		}

		out = append(out, found)
	}

	return out, nil
}

func (s *seeder) googleContacts(ctx context.Context) error {
	groups, err := s.googleGroups(ctx)
	if err != nil {
		return s.step("google contacts", 0, []error{err})
	}

	contacts := make([]map[string]any, 0, 60)

	for i := range 60 {
		p := people[i%len(people)]
		c := map[string]any{
			"names": []map[string]string{{"givenName": p.Given, "familyName": p.Family + fmt.Sprintf(" %s-%d", s.tag, i)}},
		}

		if i%7 != 3 {
			c["emailAddresses"] = []map[string]string{{"value": fmt.Sprintf("%d.%s", i, p.Email()), "type": pick(s, []string{"home", "work", "other"})}}
		}

		if s.chance(0.7) {
			c["phoneNumbers"] = []map[string]string{
				{"value": fmt.Sprintf("+358 40 %03d %04d", s.rng.IntN(1000), s.rng.IntN(10000)), "type": pick(s, []string{"mobile", "home", "work", "workFax"})},
			}
		}

		if p.Company != "" {
			c["organizations"] = []map[string]string{{"name": p.Company, "title": p.Title}}
		}

		if s.chance(0.5) {
			c["addresses"] = []map[string]string{{"streetAddress": fmt.Sprintf("Katu %d\nC 12", 1+s.rng.IntN(99)), "city": p.City, "country": p.Country, "type": "home"}}
		}

		if s.chance(0.4) {
			date := map[string]int{"month": 1 + s.rng.IntN(12), "day": 1 + s.rng.IntN(28)}
			if s.chance(0.5) {
				date["year"] = 1950 + s.rng.IntN(55)
			}

			c["birthdays"] = []map[string]any{{"date": date}}
		}

		if s.chance(0.3) {
			c["biographies"] = []map[string]string{{"value": s.body()}}
		}

		if s.chance(0.2) {
			c["urls"] = []map[string]string{{"value": "https://example.com/" + asciiOnly(p.Given), "type": "homePage"}}
		}

		if s.chance(0.15) {
			c["relations"] = []map[string]string{{"person": pick(s, people).Name(), "type": pick(s, []string{"spouse", "child", "friend", "manager"})}}
		}

		if s.chance(0.6) {
			c["memberships"] = []map[string]any{{"contactGroupMembership": map[string]string{"contactGroupResourceName": pick(s, groups)}}}
		}

		contacts = append(contacts, map[string]any{"contactPerson": c})
	}

	var created struct {
		CreatedPeople []struct {
			Person struct{ ResourceName string } `json:"person"`
		} `json:"createdPeople"`
	}

	if err := call(ctx, s.gmail, http.MethodPost, peopleAPI+"/people:batchCreateContacts",
		map[string]any{"contacts": contacts, "readMask": "names"}, &created); err != nil {
		return s.step("google contacts", 0, []error{err})
	}

	var errs []error

	for i, c := range created.CreatedPeople {
		if i%10 != 0 {
			continue
		}

		photo := map[string]string{"photoBytes": base64.StdEncoding.EncodeToString(jpegImage(300, i))}
		if err := call(ctx, s.gmail, http.MethodPatch, peopleAPI+"/"+c.Person.ResourceName+":updateContactPhoto", photo, nil); err != nil {
			errs = append(errs, err)
		}
	}

	return s.step("google contacts", len(created.CreatedPeople), errs)
}

func (s *seeder) googleTasks(ctx context.Context) error {
	var (
		n    int
		errs []error
	)

	for _, title := range []string{"Seed errands " + s.tag, "Seed work " + s.tag, "Ostoslista " + s.tag} {
		var list struct{ ID string }
		if err := call(ctx, s.gmail, http.MethodPost, tasksAPI+"/users/@me/lists", map[string]string{"title": title}, &list); err != nil {
			errs = append(errs, err)

			continue
		}

		base := tasksAPI + "/lists/" + url.PathEscape(list.ID) + "/tasks"

		for i := range 12 {
			task := map[string]any{"title": pick(s, eventTitles) + fmt.Sprintf(" #%d", i)}
			if s.chance(0.5) {
				task["notes"] = s.body()
			}

			if s.chance(0.6) {
				task["due"] = s.now.AddDate(0, 0, s.rng.IntN(60)-20).Format("2006-01-02") + "T00:00:00.000Z"
			}

			if s.chance(0.3) {
				task["status"] = "completed"
			}

			var created struct{ ID string }
			if err := call(ctx, s.gmail, http.MethodPost, base, task, &created); err != nil {
				errs = append(errs, err)

				continue
			}

			n++

			if i%4 == 0 {
				for j := range 2 {
					sub := map[string]any{"title": fmt.Sprintf("Subtask %d of %d", j+1, i)}
					if err := call(ctx, s.gmail, http.MethodPost, base+"?parent="+url.QueryEscape(created.ID), sub, nil); err != nil {
						errs = append(errs, err)
					} else {
						n++
					}
				}
			}
		}
	}

	return s.step("google tasks", n, errs)
}
