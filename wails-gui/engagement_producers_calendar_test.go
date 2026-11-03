package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
)

func prepEvent(id, title string, start time.Time, minutes int, host string, others int) calendar.Event {
	ev := calendar.Event{ID: id, Title: title, Start: start, End: start.Add(time.Duration(minutes) * time.Minute), Availability: "busy"}
	if host != "" {
		ev.ConferenceURL, ev.ConferenceHost = "https://example.invalid/"+id, host
	}
	for i := 0; i < others; i++ {
		ev.Attendees = append(ev.Attendees, calendar.Attendee{Name: []string{"Ann", "Bob", "Cy", "Dee"}[i%4], Email: "p@example.com"})
	}
	return ev
}

func TestMeetingPrepMoment(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 46, 0, 0, time.UTC)
	ev := prepEvent("ev1", "Pricing sync", now.Add(14*time.Minute), 30, "Zoom", 4)
	m := meetingPrepMoment(ev, now, time.UTC)
	if m.Kind != engagement.KindMeetingPrep || m.SubjectID != "ev1" || m.DedupeKey != "meeting_prep:ev1" {
		t.Fatalf("moment %+v", m)
	}
	if !m.DueAt.Equal(ev.Start) || !m.ExpiresAt.Equal(ev.Start) {
		t.Fatalf("a prep moment is due and expires at the start: %+v", m)
	}
	if m.Params["minutes"] != "14" || m.Params["place"] != "on Zoom" || m.Params["people"] != "with Ann, Bob, Cy, +1" || m.Params["time"] != "10:00 to 10:30" {
		t.Fatalf("params %+v", m.Params)
	}
	set, _ := engagement.Copy(engagement.KindMeetingPrep, "en")
	if got := engagement.Render(set.Sealed, m.Params); got != "Pricing sync starts in 14 min on Zoom with Ann, Bob, Cy, +1." {
		t.Fatalf("rendered %q", got)
	}
	inPerson := prepEvent("ev2", "Lunch", now.Add(5*time.Minute), 60, "", 1)
	inPerson.Location = "Cafe Nero"
	m = meetingPrepMoment(inPerson, now, time.UTC)
	if m.Params["place"] != "at Cafe Nero" {
		t.Fatalf("in-person place %q", m.Params["place"])
	}
}

func TestUpcomingMeetingsToPrepare(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	soon := prepEvent("soon", "Soon", now.Add(10*time.Minute), 30, "Zoom", 0)
	later := prepEvent("later", "Later", now.Add(20*time.Minute), 30, "Zoom", 1)
	solo := prepEvent("solo", "Focus", now.Add(5*time.Minute), 30, "", 0)
	started := prepEvent("started", "Started", now.Add(-time.Minute), 30, "Zoom", 1)
	allDay := prepEvent("allday", "Offsite", now.Add(5*time.Minute), 60, "", 3)
	allDay.AllDay = true
	withPeople := prepEvent("people", "1:1", now.Add(14*time.Minute), 30, "", 1)
	got := upcomingMeetingsToPrepare([]calendar.Event{later, withPeople, solo, started, allDay, soon}, now)
	if len(got) != 2 || got[0].ID != "soon" || got[1].ID != "people" {
		t.Fatalf("got %+v", got)
	}
}

func TestConflictMoments(t *testing.T) {
	now := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	at := func(hhmm string) time.Time {
		return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC).Add(mustDuration(hhmm))
	}
	a := prepEvent("a", "Design review", at("10:00"), 60, "Zoom", 2)
	b := prepEvent("b", "Vendor call", at("10:30"), 30, "Google Meet", 1)
	invite := prepEvent("inv", "Offsite planning", at("16:00"), 60, "", 3)
	invite.MyStatus = calendar.StatusPending
	past := prepEvent("p1", "Early", at("06:00"), 60, "Zoom", 1)
	past2 := prepEvent("p2", "Early too", at("06:30"), 60, "Zoom", 1)
	shape := calendar.ShapeDay([]calendar.Event{a, b, invite, past, past2}, now, time.UTC, now)
	moments := conflictMoments(shape, now, time.UTC)
	if len(moments) != 2 {
		t.Fatalf("moments %+v", moments)
	}
	overlap, waiting := moments[0], moments[1]
	if overlap.DedupeKey != "calendar_conflict:overlap:a:b" ||
		overlap.Params["event_type"] != "overlap" ||
		overlap.Params["event_first_title"] != "Design review" ||
		overlap.Params["event_first_start"] != "2026-10-14T10:00:00Z" ||
		overlap.Params["event_second_title"] != "Vendor call" ||
		overlap.Params["event_overlap_at"] != "2026-10-14T10:30:00Z" {
		t.Fatalf("overlap %+v", overlap)
	}
	if waiting.DedupeKey != "calendar_conflict:invite:inv" ||
		waiting.Params["event_type"] != "invite" ||
		waiting.Params["event_title"] != "Offsite planning" ||
		waiting.Params["event_start"] != "2026-10-14T16:00:00Z" ||
		waiting.Params["event_end"] != "2026-10-14T17:00:00Z" {
		t.Fatalf("invite %+v", waiting)
	}
	if !overlap.ExpiresAt.Equal(b.Start) || !waiting.ExpiresAt.Equal(invite.Start) {
		t.Fatalf("expiry %v %v", overlap.ExpiresAt, waiting.ExpiresAt)
	}
	// A pair is keyed the same whichever order it is found in.
	if engagement.DedupeKey(engagement.KindCalendarConflict, "overlap", "a", "b") != overlap.DedupeKey {
		t.Fatal("key order")
	}
}

func mustDuration(hhmm string) time.Duration {
	h, m, ok := calendar.ParseClock(hhmm)
	if !ok {
		panic(hhmm)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
}

func TestCalendarProducersAreInertWithoutTheService(t *testing.T) {
	app := &App{}
	if app.produceMeetingPrep(context.Background(), time.Now()) != 0 || app.produceCalendarConflicts(context.Background(), time.Now()) != 0 {
		t.Fatal("without the engagement service nothing is produced")
	}
}
