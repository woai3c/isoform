package calendar

import (
	"testing"
	"time"
)

func meetingEvent(id, title string, start time.Time, minutes int, host string, others int) Event {
	ev := Event{
		ID:           id,
		Title:        title,
		Start:        start,
		End:          start.Add(time.Duration(minutes) * time.Minute),
		Availability: "busy",
	}
	if host != "" {
		ev.ConferenceURL = "https://example.invalid/" + id
		ev.ConferenceHost = host
	}
	for i := 0; i < others; i++ {
		ev.Attendees = append(ev.Attendees, Attendee{Email: "p@example.com"})
	}
	return ev
}

func TestMatchSession(t *testing.T) {
	now := mustTime("2026-10-14T10:02:00Z")
	events := []Event{
		meetingEvent("zoom", "Zoom standup", now.Add(-2*time.Minute), 30, "Zoom", 2),
		meetingEvent("meet", "Design review", now.Add(-2*time.Minute), 30, "Google Meet", 2),
		meetingEvent("nolink", "1:1", now.Add(5*time.Minute), 30, "", 1),
		meetingEvent("later", "Later", now.Add(40*time.Minute), 30, "Zoom", 2),
		meetingEvent("solo", "Focus time", now, 60, "", 0),
	}
	if got := MatchSession(events, now, "Zoom"); got == nil || got.ID != "zoom" {
		t.Fatalf("Zoom should match the Zoom event, got %+v", got)
	}
	if got := MatchSession(events, now, "Google Meet"); got == nil || got.ID != "meet" {
		t.Fatalf("Meet should match the Meet event, got %+v", got)
	}
	// An unknown app matches the event without a link, not one on another
	// platform.
	if got := MatchSession(events, now, "FaceTime"); got == nil || got.ID != "nolink" {
		t.Fatalf("FaceTime should match the link-less event, got %+v", got)
	}
	// Outside the window nothing matches.
	if got := MatchSession(events, now.Add(-30*time.Minute), "Zoom"); got != nil {
		t.Fatalf("nothing should match half an hour early, got %+v", got)
	}
	// An event starting within ten minutes matches.
	if got := MatchSession(events[2:3], now, ""); got == nil || got.ID != "nolink" {
		t.Fatalf("an event starting in five minutes should match, got %+v", got)
	}
	// Focus time has no attendees and no link, so it is never a meeting.
	if got := MatchSession(events[4:], now, "Zoom"); got != nil {
		t.Fatalf("focus time must not match, got %+v", got)
	}
}

func TestMatchSessionPrefersClosestStart(t *testing.T) {
	now := mustTime("2026-10-14T10:00:00Z")
	events := []Event{
		meetingEvent("a", "A", now.Add(-50*time.Minute), 60, "Zoom", 1),
		meetingEvent("b", "B", now.Add(-3*time.Minute), 30, "Zoom", 1),
	}
	if got := MatchSession(events, now, "Zoom"); got == nil || got.ID != "b" {
		t.Fatalf("the event starting closest to now wins, got %+v", got)
	}
}

func TestMatchSessionSkipsDeclinedAndFree(t *testing.T) {
	now := mustTime("2026-10-14T10:00:00Z")
	declined := meetingEvent("d", "Declined", now, 30, "Zoom", 1)
	declined.MyStatus = StatusDeclined
	free := meetingEvent("f", "Free", now, 30, "Zoom", 1)
	free.Availability = "free"
	if got := MatchSession([]Event{declined, free}, now, "Zoom"); got != nil {
		t.Fatalf("declined and free events must not match, got %+v", got)
	}
}

func TestMatchSpan(t *testing.T) {
	start := mustTime("2026-10-14T10:03:00Z")
	end := start.Add(25 * time.Minute)
	events := []Event{
		meetingEvent("zoom", "Zoom standup", mustTime("2026-10-14T10:00:00Z"), 30, "Zoom", 2),
		meetingEvent("after", "After", mustTime("2026-10-14T11:00:00Z"), 30, "Zoom", 2),
	}
	if got := MatchSpan(events, start, end, "Zoom"); got == nil || got.ID != "zoom" {
		t.Fatalf("got %+v", got)
	}
	if got := MatchSpan(events, mustTime("2026-10-14T12:00:00Z"), mustTime("2026-10-14T12:30:00Z"), "Zoom"); got != nil {
		t.Fatalf("a session an hour later must not match, got %+v", got)
	}
}

func TestLiveEvent(t *testing.T) {
	now := mustTime("2026-10-14T10:00:00Z")
	soon := meetingEvent("soon", "Soon", now.Add(8*time.Minute), 30, "", 1)
	later := meetingEvent("later", "Later", now.Add(12*time.Minute), 30, "", 1)
	past := meetingEvent("past", "Past", now.Add(-60*time.Minute), 30, "", 1)
	if got := LiveEvent([]Event{later, past, soon}, now); got == nil || got.ID != "soon" {
		t.Fatalf("got %+v", got)
	}
	if got := LiveEvent([]Event{later, past}, now); got != nil {
		t.Fatalf("nothing live, got %+v", got)
	}
}
