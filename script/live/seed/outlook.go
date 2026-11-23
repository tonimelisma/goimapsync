package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const graphAPI = "https://graph.microsoft.com/v1.0/me"

func (s *seeder) seedOutlook(ctx context.Context) error {
	return errors.Join(
		s.outlookMail(ctx),
		s.outlookCalendar(ctx),
		s.outlookContacts(ctx),
		s.outlookTasks(ctx),
	)
}

// graphFolder finds or creates a mail folder by name under parent ("" for
// the top level) and returns its ID.
func (s *seeder) graphFolder(ctx context.Context, parent, name string) (string, error) {
	target := graphAPI + "/mailFolders"
	if parent != "" {
		target += "/" + url.PathEscape(parent) + "/childFolders"
	}

	var existing struct {
		Value []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"value"`
	}

	if err := call(ctx, s.outlook, http.MethodGet, target+"?$top=250", nil, &existing); err != nil {
		return "", err
	}

	for _, f := range existing.Value {
		if f.DisplayName == name {
			return f.ID, nil
		}
	}

	var created struct {
		ID string `json:"id"`
	}

	if err := call(ctx, s.outlook, http.MethodPost, target, map[string]string{"displayName": name}, &created); err != nil {
		return "", err
	}

	return created.ID, nil
}

// outlookFolders returns folder IDs by userFolders index; the first is a
// child folder "Alpha" of "Seed Projects", like the nested Gmail label.
func (s *seeder) outlookFolders(ctx context.Context) ([]string, error) {
	ids := make([]string, len(userFolders))

	for i, name := range userFolders {
		id, err := s.graphFolder(ctx, "", name)
		if err != nil {
			return nil, err
		}

		if i == 0 {
			if id, err = s.graphFolder(ctx, id, "Alpha"); err != nil {
				return nil, err
			}
		}

		ids[i] = id
	}

	return ids, nil
}

func recipient(name, addr string) map[string]any {
	return map[string]any{"emailAddress": map[string]string{"name": name, "address": addr}}
}

// graphMessage is a message as Graph creates it. PR_MESSAGE_FLAGS (0x0E07)
// set at creation makes it a received or sent message instead of a draft;
// PR_MESSAGE_DELIVERY_TIME (0x0E06) and PR_CLIENT_SUBMIT_TIME (0x0039) set
// its dates.
func (s *seeder) graphMessage(m *message) map[string]any {
	flags := "1" // read
	if m.Unread {
		flags = "0"
	}

	body := map[string]string{"contentType": "text", "content": m.Text}
	if m.HTML {
		html := htmlBody(m.Text)

		for _, a := range m.Attachments {
			if a.ContentID != "" {
				html = strings.Replace(html, "</body>", "<img src=\"cid:"+a.ContentID+"\"></body>", 1)
			}
		}

		body = map[string]string{"contentType": "html", "content": html}
	}

	msg := map[string]any{
		"subject":           m.Subject,
		"body":              body,
		"from":              recipient(m.From.Name(), m.FromAddr),
		"toRecipients":      []any{recipient(m.To.Name(), m.ToAddr)},
		"internetMessageId": m.ID,
		"isRead":            !m.Unread,
		"singleValueExtendedProperties": []map[string]string{
			{"id": "Integer 0x0E07", "value": flags},
			{"id": "SystemTime 0x0E06", "value": m.Date.UTC().Format(time.RFC3339)},
			{"id": "SystemTime 0x0039", "value": m.Date.UTC().Add(-time.Minute).Format(time.RFC3339)},
		},
	}

	if len(m.Cc) > 0 {
		msg["ccRecipients"] = []any{recipient(m.Cc[0].Name(), m.Cc[0].Email())}
	}

	if m.Flag {
		msg["flag"] = map[string]string{"flagStatus": "flagged"}
	}

	var atts []map[string]any

	for _, a := range m.Attachments {
		att := map[string]any{
			"@odata.type": "#microsoft.graph.fileAttachment", "name": a.Name, "contentType": a.ContentType,
			"contentBytes": base64.StdEncoding.EncodeToString(a.Data),
		}
		if a.ContentID != "" {
			att["isInline"] = true
			att["contentId"] = a.ContentID
		}

		atts = append(atts, att)
	}

	if len(atts) > 0 {
		msg["attachments"] = atts
	}

	return msg
}

func (s *seeder) outlookMail(ctx context.Context) error {
	folders, err := s.outlookFolders(ctx)
	if err != nil {
		return s.step("outlook folders", 0, []error{err})
	}

	var (
		n    int
		errs []error
	)

	for _, t := range s.mail {
		if t.Side&sideOutlook == 0 {
			continue
		}

		for _, m := range t.Messages {
			folder := "inbox"

			switch {
			case m.FromAddr == s.oAddr:
				folder = "sentitems"
			case t.Folder >= 0:
				folder = folders[t.Folder]
			case t.Archived:
				folder = "archive"
			}

			if err := call(ctx, s.outlook, http.MethodPost, graphAPI+"/mailFolders/"+url.PathEscape(folder)+"/messages", s.graphMessage(m), nil); err != nil {
				errs = append(errs, err)

				continue
			}

			n++
		}
	}

	return s.step("outlook messages", n, errs)
}

// windowsZones maps the seed time zones to Outlook's Windows names.
var windowsZones = map[string]string{
	"Europe/Helsinki":    "FLE Standard Time",
	"America/New_York":   "Eastern Standard Time",
	"Asia/Tokyo":         "Tokyo Standard Time",
	"UTC":                "UTC",
	"Australia/Adelaide": "Cen. Australia Standard Time",
}

func graphTime(t time.Time, zone string) map[string]string {
	return map[string]string{"dateTime": t.Format("2006-01-02T15:04:05"), "timeZone": windowsZones[zone]}
}

// graphEvent is a random single event.
func (s *seeder) graphEvent(days int) map[string]any {
	zone := pick(s, zones)
	start := s.localTime(days, zone)
	e := map[string]any{
		"subject": pick(s, eventTitles) + " " + s.tag,
		"start":   graphTime(start, zone),
		"end":     graphTime(start.Add(time.Duration(30+30*s.rng.IntN(4))*time.Minute), zone),
	}

	if s.chance(0.5) {
		e["body"] = map[string]string{"contentType": "text", "content": s.body()}
	}

	if s.chance(0.4) {
		e["location"] = map[string]string{"displayName": pick(s, people).City}
	}

	if s.chance(0.3) {
		e["isReminderOn"] = true
		e["reminderMinutesBeforeStart"] = pick(s, []int{5, 15, 60, 1440})
	} else {
		e["isReminderOn"] = false
	}

	if s.chance(0.2) {
		e["showAs"] = "free"
	}

	if s.chance(0.1) {
		e["sensitivity"] = "private"
	}

	if s.chance(0.2) {
		e["categories"] = []string{pick(s, []string{"Red category", "Blue category", "Green category"})}
	}

	return e
}

func (s *seeder) outlookCalendar(ctx context.Context) error {
	var (
		n    int
		errs []error
	)

	add := func(target string, e map[string]any) string {
		var created struct {
			ID string `json:"id"`
		}

		if err := call(ctx, s.outlook, http.MethodPost, target, e, &created); err != nil {
			errs = append(errs, err)

			return ""
		}

		n++

		return created.ID
	}

	events := graphAPI + "/events"

	for range 40 {
		add(events, s.graphEvent(s.rng.IntN(240)-120))
	}

	for range 8 {
		start := s.now.AddDate(0, 0, s.rng.IntN(240)-120).Truncate(24 * time.Hour)
		add(events, map[string]any{
			"subject": "Holiday " + s.tag, "isAllDay": true, "isReminderOn": false,
			"start": graphTime(start, "UTC"), "end": graphTime(start.AddDate(0, 0, 1+s.rng.IntN(5)), "UTC"),
		})
	}

	// Graph sends an invitation for this one; it goes to the Gmail test
	// account.
	attended := s.graphEvent(8)
	attended["subject"] = "Review with Gmail tester " + s.tag
	attended["attendees"] = []map[string]any{{"emailAddress": map[string]string{"address": s.gAddr}, "type": "required"}}
	add(events, attended)

	s.outlookSeries(ctx, add, &errs)

	var team struct {
		ID string `json:"id"`
	}

	if err := call(ctx, s.outlook, http.MethodPost, graphAPI+"/calendars", map[string]string{"name": "Seed Team " + s.tag}, &team); err != nil {
		errs = append(errs, err)
	} else {
		for range 10 {
			add(graphAPI+"/calendars/"+url.PathEscape(team.ID)+"/events", s.graphEvent(s.rng.IntN(60)-30))
		}
	}

	return s.step("outlook calendar events", n, errs)
}

// outlookSeries adds recurring events, then moves one instance and cancels
// another of the weekly one.
func (s *seeder) outlookSeries(ctx context.Context, add func(string, map[string]any) string, errs *[]error) {
	events := graphAPI + "/events"
	series := func(subject string, start time.Time, zone string, pattern map[string]any, rng map[string]any) map[string]any {
		rng["startDate"] = start.Format(time.DateOnly)
		rng["recurrenceTimeZone"] = windowsZones[zone]

		return map[string]any{
			"subject": subject + " " + s.tag, "start": graphTime(start, zone), "end": graphTime(start.Add(30*time.Minute), zone),
			"isReminderOn": false, "recurrence": map[string]any{"pattern": pattern, "range": rng},
		}
	}

	weeklyStart := s.localTime(-21, "Europe/Helsinki")
	weekday := strings.ToLower(weeklyStart.Weekday().String())

	add(events, series("Daily check", s.localTime(-3, "America/New_York"), "America/New_York",
		map[string]any{"type": "daily", "interval": 1}, map[string]any{"type": "numbered", "numberOfOccurrences": 14}))
	add(events, series("Monthly review", s.localTime(-40, "Asia/Tokyo"), "Asia/Tokyo",
		map[string]any{"type": "relativeMonthly", "interval": 1, "daysOfWeek": []string{"tuesday"}, "index": "second"},
		map[string]any{"type": "endDate", "endDate": s.now.AddDate(1, 0, 0).Format(time.DateOnly)}))

	id := add(events, series("Weekly sync", weeklyStart, "Europe/Helsinki",
		map[string]any{"type": "weekly", "interval": 1, "daysOfWeek": []string{weekday}, "firstDayOfWeek": "monday"},
		map[string]any{"type": "numbered", "numberOfOccurrences": 10}))
	if id == "" {
		return
	}

	var instances struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}

	window := "?startDateTime=" + weeklyStart.AddDate(0, 0, -1).UTC().Format(time.RFC3339) +
		"&endDateTime=" + weeklyStart.AddDate(0, 0, 80).UTC().Format(time.RFC3339) + "&$select=id&$orderby=start/dateTime"
	if err := call(ctx, s.outlook, http.MethodGet, events+"/"+url.PathEscape(id)+"/instances"+window, nil, &instances); err != nil {
		*errs = append(*errs, err)

		return
	}

	if len(instances.Value) < 4 {
		*errs = append(*errs, fmt.Errorf("weekly series has %d instances", len(instances.Value)))

		return
	}

	moved := weeklyStart.AddDate(0, 0, 7).Add(26 * time.Hour)
	patch := map[string]any{
		"subject": "Weekly sync (moved) " + s.tag,
		"start":   graphTime(moved, "Europe/Helsinki"), "end": graphTime(moved.Add(time.Hour), "Europe/Helsinki"),
	}

	if err := call(ctx, s.outlook, http.MethodPatch, events+"/"+url.PathEscape(instances.Value[1].ID), patch, nil); err != nil {
		*errs = append(*errs, err)
	}

	if err := call(ctx, s.outlook, http.MethodDelete, events+"/"+url.PathEscape(instances.Value[3].ID), nil, nil); err != nil {
		*errs = append(*errs, err)
	}
}

func (s *seeder) outlookContacts(ctx context.Context) error {
	var folder struct {
		ID string `json:"id"`
	}

	var errs []error

	if err := call(ctx, s.outlook, http.MethodPost, graphAPI+"/contactFolders",
		map[string]string{"displayName": "Seed Friends " + s.tag}, &folder); err != nil {
		errs = append(errs, err)
	}

	n := 0

	for i := range 40 {
		p := people[i%len(people)]
		c := map[string]any{
			"givenName": p.Given, "surname": p.Family + fmt.Sprintf(" %s-%d", s.tag, i),
			"displayName": p.Name() + fmt.Sprintf(" %s-%d", s.tag, i),
		}

		if i%7 != 3 {
			c["emailAddresses"] = []map[string]string{{"address": fmt.Sprintf("%d.%s", i, p.Email()), "name": p.Name()}}
		}

		if s.chance(0.7) {
			c["mobilePhone"] = fmt.Sprintf("+358 50 %03d %04d", s.rng.IntN(1000), s.rng.IntN(10000))
		}

		if s.chance(0.3) {
			c["businessPhones"] = []string{fmt.Sprintf("+1 512 555 %04d", s.rng.IntN(10000))}
		}

		if p.Company != "" {
			c["companyName"] = p.Company
			c["jobTitle"] = p.Title
		}

		if s.chance(0.5) {
			c["homeAddress"] = map[string]string{"street": fmt.Sprintf("Katu %d", 1+s.rng.IntN(99)), "city": p.City, "countryOrRegion": p.Country}
		}

		if s.chance(0.4) {
			c["birthday"] = time.Date(1950+s.rng.IntN(55), time.Month(1+s.rng.IntN(12)), 1+s.rng.IntN(28), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		}

		if s.chance(0.3) {
			c["personalNotes"] = s.body()
		}

		if s.chance(0.3) {
			c["categories"] = []string{pick(s, []string{"Seed Friends", "Seed Work", "Perhe 👪"})}
		}

		target := graphAPI + "/contacts"
		if folder.ID != "" && i%3 == 0 {
			target = graphAPI + "/contactFolders/" + url.PathEscape(folder.ID) + "/contacts"
		}

		var created struct {
			ID string `json:"id"`
		}

		if err := call(ctx, s.outlook, http.MethodPost, target, c, &created); err != nil {
			errs = append(errs, err)

			continue
		}

		n++

		if i%8 == 0 {
			photo := rawBody{data: jpegImage(240, i), contentType: "image/jpeg"}
			if err := call(ctx, s.outlook, http.MethodPut, graphAPI+"/contacts/"+url.PathEscape(created.ID)+"/photo/$value", photo, nil); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return s.step("outlook contacts", n, errs)
}

func (s *seeder) outlookTasks(ctx context.Context) error {
	var (
		n    int
		errs []error
	)

	for _, name := range []string{"Seed errands " + s.tag, "Seed work " + s.tag, "Ostoslista " + s.tag} {
		var list struct {
			ID string `json:"id"`
		}

		if err := call(ctx, s.outlook, http.MethodPost, graphAPI+"/todo/lists", map[string]string{"displayName": name}, &list); err != nil {
			errs = append(errs, err)

			continue
		}

		base := graphAPI + "/todo/lists/" + url.PathEscape(list.ID) + "/tasks"

		for i := range 12 {
			task := map[string]any{"title": pick(s, eventTitles) + fmt.Sprintf(" #%d", i), "importance": pick(s, []string{"low", "normal", "high"})}
			if s.chance(0.5) {
				task["body"] = map[string]string{"contentType": "text", "content": s.body()}
			}

			if s.chance(0.6) {
				due := s.now.AddDate(0, 0, s.rng.IntN(60)-20).Truncate(24 * time.Hour)
				task["dueDateTime"] = graphTime(due, "UTC")
			}

			if s.chance(0.3) {
				task["status"] = "completed"
			}

			if i == 5 {
				task["dueDateTime"] = graphTime(s.now.Truncate(24*time.Hour), "UTC")
				task["recurrence"] = map[string]any{
					"pattern": map[string]any{"type": "weekly", "interval": 1, "daysOfWeek": []string{"friday"}},
					"range":   map[string]any{"type": "noEnd", "startDate": s.now.Format(time.DateOnly)},
				}
			}

			var created struct {
				ID string `json:"id"`
			}

			if err := call(ctx, s.outlook, http.MethodPost, base, task, &created); err != nil {
				errs = append(errs, err)

				continue
			}

			n++

			if i%4 == 0 {
				for j := range 3 {
					item := map[string]any{"displayName": fmt.Sprintf("Step %d", j+1), "isChecked": j == 0}
					if err := call(ctx, s.outlook, http.MethodPost, base+"/"+url.PathEscape(created.ID)+"/checklistItems", item, nil); err != nil {
						errs = append(errs, err)
					}
				}
			}
		}
	}

	return s.step("outlook tasks", n, errs)
}
