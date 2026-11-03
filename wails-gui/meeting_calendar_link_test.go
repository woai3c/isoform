package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/meetingdetect"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func newLinkTestApp(t *testing.T) (*App, *storage.Store) {
	t.Helper()
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &App{store: st}, st
}

func storeEvent(t *testing.T, st *storage.Store, ev calendar.Event) {
	t.Helper()
	row := calendarEventRow(ev)
	for _, att := range ev.Attendees {
		row.Attendees = append(row.Attendees, storage.CalendarAttendeeRow{EventID: ev.ID, Email: att.Email, Name: att.Name, IsMe: att.IsMe})
	}
	if err := st.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{row}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarEventForSessionAndTranscriptHeader(t *testing.T) {
	app, st := newLinkTestApp(t)
	start := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	standup := prepEvent("standup", "Standup", start, 30, "Zoom", 2)
	standup.Attendees = []calendar.Attendee{{Name: "Ann", Email: "ann@example.com"}, {Name: "Me", Email: "me@example.com", IsMe: true}}
	other := prepEvent("other", "Design review", start.Add(3*time.Hour), 60, "Google Meet", 2)
	storeEvent(t, st, standup)
	storeEvent(t, st, other)

	session := ActivitySessionRow{ID: "sess-1", StartTime: start.Add(2 * time.Minute).UnixMilli(), EndTime: start.Add(28 * time.Minute).UnixMilli()}
	ev := app.calendarEventForSession(session, "Zoom")
	if ev == nil || ev.ID != "standup" {
		t.Fatalf("matched %+v", ev)
	}
	header := transcriptHeaderForEvent(ev, time.UTC)
	if header.Title != "Standup" || header.Attendees != "Ann" || header.Place != "Zoom" || header.Time != "10:00 to 10:30" {
		t.Fatalf("header %+v", header)
	}
	markdown, _ := buildTranscriptMarkdownWithEvent("Zoom", session, []activitymon.ActivityEvent{
		{Kind: activitymon.EventSpeech, Timestamp: session.StartTime, SpeechText: "hello there", SpeechSource: "microphone"},
	}, header)
	for _, want := range []string{"# Meeting Transcript: Standup", "- **App**: Zoom", "- **Event**: Standup (10:00 to 10:30)", "- **Attendees**: Ann", "- **Place**: Zoom", "## Transcript"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("transcript missing %q:\n%s", want, markdown)
		}
	}
	// Without an event the header is the old one.
	plain, _ := buildTranscriptMarkdown("Zoom", session, nil)
	if !strings.Contains(plain, "# Meeting Transcript: Zoom") || strings.Contains(plain, "**Event**") {
		t.Fatalf("plain header changed:\n%s", plain)
	}

	app.attachCalendarEventToSession(session, ev)
	linked, err := st.ListCalendarEventsForSession(context.Background(), "sess-1")
	if err != nil || len(linked) != 1 || linked[0].ID != "standup" {
		t.Fatalf("linked %+v %v", linked, err)
	}

	// A session an hour later matches nothing, unless the live link overlaps it.
	late := ActivitySessionRow{ID: "sess-2", StartTime: start.Add(90 * time.Minute).UnixMilli(), EndTime: start.Add(120 * time.Minute).UnixMilli()}
	if got := app.calendarEventForSession(late, "Zoom"); got != nil {
		t.Fatalf("late session matched %+v", got)
	}
	liveMeetingMu.Lock()
	liveMeetingLive = &liveMeetingLink{Event: other, MatchedAt: other.Start.Add(2 * time.Minute), App: "Google Meet"}
	liveMeetingMu.Unlock()
	t.Cleanup(func() {
		liveMeetingMu.Lock()
		liveMeetingLive = nil
		liveMeetingMu.Unlock()
	})
	during := ActivitySessionRow{ID: "sess-3", StartTime: other.Start.Add(5 * time.Minute).UnixMilli(), EndTime: other.Start.Add(50 * time.Minute).UnixMilli()}
	if got := app.calendarEventForSession(during, "FaceTime"); got == nil || got.ID != "other" {
		t.Fatalf("live link should cover the session: %+v", got)
	}
	// A link matched long before the session does not attach to it.
	liveMeetingMu.Lock()
	liveMeetingLive.MatchedAt = other.Start.Add(-2 * time.Hour)
	liveMeetingMu.Unlock()
	if got := app.calendarEventForSession(during, "FaceTime"); got != nil {
		t.Fatalf("a stale live link must not attach: %+v", got)
	}
}

func TestLinkMeetingToCalendarEvent(t *testing.T) {
	app, st := newLinkTestApp(t)
	now := time.Date(2026, 10, 14, 10, 2, 0, 0, time.UTC)
	storeEvent(t, st, prepEvent("zoom", "Zoom standup", now.Add(-2*time.Minute), 30, "Zoom", 2))
	app.linkMeetingToCalendarEvent(meetingdetect.Result{IsMeeting: true, MeetingName: "Zoom", BundleID: "us.zoom.xos"}, now)
	liveMeetingMu.Lock()
	live := liveMeetingLive
	liveMeetingLive = nil
	liveMeetingMu.Unlock()
	if live == nil || live.Event.ID != "zoom" || live.App != "Zoom" {
		t.Fatalf("live link %+v", live)
	}
	app.linkMeetingToCalendarEvent(meetingdetect.Result{IsMeeting: true, MeetingName: "FaceTime"}, now.Add(3*time.Hour))
	liveMeetingMu.Lock()
	live = liveMeetingLive
	liveMeetingMu.Unlock()
	if live != nil {
		t.Fatalf("no event three hours later: %+v", live)
	}
}
