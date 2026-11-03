package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/meetingdetect"
)

// A meeting session is linked to the calendar event behind it twice: when
// the meeting opens (the event overlapping now within ten minutes whose
// conference host matches the app) and when the transcript is saved (the
// event overlapping the session's span), because the activity session row
// only exists at the end. The link stores session_id on the event and gives
// the session and the transcript header the event's title and attendees.

// liveMeetingLink is the event matched when the current meeting opened.
type liveMeetingLink struct {
	Event     calendar.Event
	MatchedAt time.Time
	App       string
}

var (
	liveMeetingMu   sync.Mutex
	liveMeetingLive *liveMeetingLink
)

// linkMeetingToCalendarEvent runs when a meeting opens. It is called on its
// own goroutine, so it never holds the monitor lock.
func (a *App) linkMeetingToCalendarEvent(result meetingdetect.Result, now time.Time) {
	if a == nil || a.store == nil {
		return
	}
	events, err := a.listCalendarEvents(context.Background(), now.Add(-2*time.Hour), now.Add(2*time.Hour))
	if err != nil {
		log.Printf("[meeting-link] events unavailable: %v", err)
		return
	}
	match := calendar.MatchSession(events, now, result.MeetingName)
	liveMeetingMu.Lock()
	if match == nil {
		liveMeetingLive = nil
	} else {
		liveMeetingLive = &liveMeetingLink{Event: *match, MatchedAt: now, App: result.MeetingName}
	}
	liveMeetingMu.Unlock()
	if match != nil {
		log.Printf("[meeting-link] %s meeting matches calendar event %q", result.MeetingName, match.Title)
	}
}

// calendarEventForSession picks the event behind a finished meeting session:
// the event overlapping the session's span whose host matches the app, or the
// event matched when the meeting opened if it overlaps the session.
func (a *App) calendarEventForSession(session ActivitySessionRow, meetingApp string) *calendar.Event {
	if a == nil || a.store == nil {
		return nil
	}
	start := time.UnixMilli(session.StartTime)
	end := time.UnixMilli(session.EndTime)
	if session.EndTime <= 0 {
		end = time.Now()
	}
	events, err := a.listCalendarEvents(context.Background(), start.Add(-calendar.MatchWindow), end.Add(calendar.MatchWindow))
	if err != nil {
		log.Printf("[meeting-link] events unavailable: %v", err)
	}
	if match := calendar.MatchSpan(events, start, end, meetingApp); match != nil {
		return match
	}
	liveMeetingMu.Lock()
	live := liveMeetingLive
	liveMeetingMu.Unlock()
	matchedDuring := live != nil && !live.MatchedAt.Before(start.Add(-calendar.MatchWindow)) && !live.MatchedAt.After(end.Add(calendar.MatchWindow))
	if matchedDuring && live.Event.Overlaps(start.Add(-calendar.MatchWindow), end.Add(calendar.MatchWindow)) {
		ev := live.Event
		return &ev
	}
	return nil
}

// attachCalendarEventToSession stores the session on the event.
func (a *App) attachCalendarEventToSession(session ActivitySessionRow, ev *calendar.Event) {
	if a == nil || a.store == nil || ev == nil {
		return
	}
	if err := a.store.SetCalendarEventSession(context.Background(), ev.ID, session.ID); err != nil {
		log.Printf("[meeting-link] link event %s to session %s: %v", ev.ID, session.ID, err)
		return
	}
	liveMeetingMu.Lock()
	if liveMeetingLive != nil && liveMeetingLive.Event.ID == ev.ID {
		liveMeetingLive = nil
	}
	liveMeetingMu.Unlock()
}

// transcriptEventHeader is what the transcript's header says about the
// calendar event behind the meeting.
type transcriptEventHeader struct {
	Title     string
	Attendees string
	Place     string
	Time      string
}

func transcriptHeaderForEvent(ev *calendar.Event, loc *time.Location) *transcriptEventHeader {
	if ev == nil {
		return nil
	}
	return &transcriptEventHeader{
		Title:     strings.TrimSpace(ev.Title),
		Attendees: calendar.AttendeeNames(*ev, 0),
		Place:     calendar.Place(*ev),
		Time:      calendar.TimeRange(*ev, loc),
	}
}
