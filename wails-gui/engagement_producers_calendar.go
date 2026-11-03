package main

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
)

// Moments the calendar produces. meeting_prep is active and runs on its own
// minute loop; calendar_conflict is passive and runs after every sync. A
// meeting's follow-ups announce nothing: they become commitments.
const (
	// engagementMeetingPrepLead is how long before an event meeting_prep fires.
	engagementMeetingPrepLead   = 15 * time.Minute
	engagementMeetingPrepTick   = time.Minute
	engagementMeetingPrepStakes = 0.6
	engagementConflictStakes    = 0.4
	engagementConflictHorizon   = 48 * time.Hour
	engagementPrepPeopleLimit   = 3
)

// meetingPrepMoment builds the meeting_prep moment for an event. It is pure
// so the copy and the keys can be tested.
func meetingPrepMoment(ev calendar.Event, now time.Time, loc *time.Location) engagement.Moment {
	minutes := int(ev.Start.Sub(now).Round(time.Minute) / time.Minute)
	if minutes < 0 {
		minutes = 0
	}
	title := engagementTitle(ev.Title)
	private := title == "" || proactiveVoiceRestrictedText(title)
	if private {
		title = ""
	}
	// Render trims each value, so the connector words sit inside the values
	// and the template separates them with spaces; a meeting-like event always
	// has a place (its link) or people, so the sentence never ends bare.
	place := ""
	if p := calendar.Place(ev); p != "" && !private {
		place = "on " + p
		if ev.InPerson() {
			place = "at " + p
		}
	}
	people := ""
	names := ""
	if !private {
		names = calendar.AttendeeNames(ev, engagementPrepPeopleLimit)
	}
	if names != "" {
		people = "with " + names
	}
	params := map[string]string{
		"title":            title,
		"minutes":          strconv.Itoa(minutes),
		"place":            place,
		"people":           people,
		"time":             calendar.TimeRange(ev, loc),
		"event_title":      title,
		"event_start":      ev.Start.Format(time.RFC3339),
		"event_end":        ev.End.Format(time.RFC3339),
		"event_place":      "",
		"event_people":     names,
		"event_private":    strconv.FormatBool(private),
		"event_minutes":    strconv.Itoa(minutes),
		"event_conference": "",
	}
	if !private {
		params["event_place"] = strings.TrimSpace(calendar.Place(ev))
		params["event_conference"] = ev.ConferenceHost
	}
	return engagement.Moment{
		Kind:      engagement.KindMeetingPrep,
		SubjectID: ev.ID,
		DedupeKey: engagement.DedupeKey(engagement.KindMeetingPrep, ev.ID),
		CreatedAt: now,
		DueAt:     ev.Start,
		ExpiresAt: ev.Start,
		Stakes:    engagementMeetingPrepStakes,
		Params:    params,
	}
}

// upcomingMeetingsToPrepare picks the meeting-like events whose start is
// within the lead time and still ahead of now.
func upcomingMeetingsToPrepare(events []calendar.Event, now time.Time) []calendar.Event {
	var out []calendar.Event
	for _, ev := range events {
		if ev.AllDay || !ev.Busy() || !ev.IsMeetingLike() {
			continue
		}
		if ev.Start.Before(now) || ev.Start.After(now.Add(engagementMeetingPrepLead)) {
			continue
		}
		out = append(out, ev)
	}
	calendar.SortByStart(out)
	return out
}

// conflictMoments builds the passive calendar_conflict moments for a day
// shape: one per overlapping pair (keyed by both ids, so a pair is announced
// once) and one per invite still waiting within the horizon.
func conflictMoments(shape calendar.DayShape, now time.Time, loc *time.Location) []engagement.Moment {
	var out []engagement.Moment
	for _, c := range shape.Conflicts {
		if !c.Second.End.After(now) {
			continue
		}
		ids := []string{c.First.ID, c.Second.ID}
		sort.Strings(ids)
		first := calendarConflictEventParams(c.First, loc)
		second := calendarConflictEventParams(c.Second, loc)
		summary := "Two calendar events overlap."
		if first["event_private"] == "true" && second["event_private"] != "true" {
			summary = "A private event overlaps another event."
		} else if second["event_private"] == "true" && first["event_private"] != "true" {
			summary = "An event overlaps a private event."
		}
		params := map[string]string{
			"summary":          summary,
			"event_type":       "overlap",
			"event_overlap_at": c.Second.Start.Format(time.RFC3339),
		}
		for key, value := range first {
			params["event_first_"+strings.TrimPrefix(key, "event_")] = value
		}
		for key, value := range second {
			params["event_second_"+strings.TrimPrefix(key, "event_")] = value
		}
		out = append(out, engagement.Moment{
			Kind:      engagement.KindCalendarConflict,
			SubjectID: c.First.ID,
			DedupeKey: engagement.DedupeKey(engagement.KindCalendarConflict, "overlap", ids[0], ids[1]),
			CreatedAt: now,
			DueAt:     c.Second.Start,
			ExpiresAt: c.Second.Start,
			Stakes:    engagementConflictStakes,
			Params:    params,
		})
	}
	for _, ev := range shape.PendingInvites {
		if ev.Start.After(now.Add(engagementConflictHorizon)) {
			continue
		}
		event := calendarConflictEventParams(ev, loc)
		summary := "You have not answered a calendar invite."
		if event["event_private"] == "true" {
			summary = "You have not answered a private calendar invite."
		}
		params := map[string]string{
			"summary":    summary,
			"event_type": "invite",
		}
		for key, value := range event {
			params[key] = value
		}
		out = append(out, engagement.Moment{
			Kind:      engagement.KindCalendarConflict,
			SubjectID: ev.ID,
			DedupeKey: engagement.DedupeKey(engagement.KindCalendarConflict, "invite", ev.ID),
			CreatedAt: now,
			DueAt:     ev.Start,
			ExpiresAt: ev.Start,
			Stakes:    engagementConflictStakes,
			Params:    params,
		})
	}
	return out
}

func calendarConflictEventParams(ev calendar.Event, loc *time.Location) map[string]string {
	title := engagementTitle(ev.Title)
	private := title == "" || proactiveVoiceRestrictedText(title)
	if private {
		title = ""
	}
	params := map[string]string{
		"event_title":      title,
		"event_start":      ev.Start.Format(time.RFC3339),
		"event_end":        ev.End.Format(time.RFC3339),
		"event_place":      "",
		"event_people":     "",
		"event_private":    strconv.FormatBool(private),
		"event_minutes":    "",
		"event_conference": "",
		"event_clock":      ev.Start.In(loc).Format("15:04"),
	}
	if !private {
		params["event_place"] = strings.TrimSpace(calendar.Place(ev))
		params["event_people"] = calendar.AttendeeNames(ev, engagementPrepPeopleLimit)
		params["event_conference"] = ev.ConferenceHost
	}
	return params
}

// produceMeetingPrep submits meeting_prep for every meeting-like event that
// starts within the lead time. Keys are per event, so each fires once.
func (a *App) produceMeetingPrep(ctx context.Context, now time.Time) int {
	if !a.engagementActive() || a.store == nil {
		return 0
	}
	events, err := a.listCalendarEvents(ctx, now, now.Add(engagementMeetingPrepLead+time.Minute))
	if err != nil {
		log.Printf("[engagement] meeting prep: %v", err)
		return 0
	}
	sent := 0
	for _, ev := range upcomingMeetingsToPrepare(events, now) {
		if err := a.submitEngagementSignal(ctx, meetingPrepMoment(ev, now, time.Local)); err != nil {
			log.Printf("[engagement] meeting prep for %s: %v", ev.ID, err)
			continue
		}
		sent++
	}
	return sent
}

// produceCalendarConflicts submits calendar_conflict for today's and
// tomorrow's clashes and the invites still waiting.
func (a *App) produceCalendarConflicts(ctx context.Context, now time.Time) int {
	if !a.engagementActive() || a.store == nil {
		return 0
	}
	events, err := a.listCalendarEvents(ctx, now.Add(-24*time.Hour), now.Add(engagementConflictHorizon))
	if err != nil {
		log.Printf("[engagement] calendar conflicts: %v", err)
		return 0
	}
	sent := 0
	seen := map[string]bool{}
	for day := 0; day < 2; day++ {
		shape := calendar.ShapeDay(events, now.Add(time.Duration(day)*24*time.Hour), time.Local, now)
		for _, m := range conflictMoments(shape, now, time.Local) {
			if seen[m.DedupeKey] {
				continue
			}
			seen[m.DedupeKey] = true
			if err := a.submitEngagementSignal(ctx, m); err != nil {
				log.Printf("[engagement] calendar conflict: %v", err)
				continue
			}
			sent++
		}
	}
	return sent
}

// runCalendarMoments checks for meetings to prepare every minute until ctx
// ends. It starts with the calendar reader.
func (a *App) runCalendarMoments(ctx context.Context) {
	ticker := time.NewTicker(engagementMeetingPrepTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.produceMeetingPrep(ctx, now)
		}
	}
}
