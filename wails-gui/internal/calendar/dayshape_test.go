package calendar

import (
	"strings"
	"testing"
	"time"
)

func TestShapeDay(t *testing.T) {
	loc := time.UTC
	day := mustTime("2026-10-14T06:00:00Z")
	now := mustTime("2026-10-14T06:30:00Z")
	at := func(hhmm string) time.Time { return mustTime("2026-10-14T" + hhmm + ":00Z") }

	standup := meetingEvent("standup", "Standup", at("09:30"), 15, "Zoom", 3)
	design := meetingEvent("design", "Design review", at("09:45"), 60, "Google Meet", 2)
	lunch := meetingEvent("lunch", "Lunch", at("12:00"), 60, "", 0)
	oneOnOne := meetingEvent("1on1", "1:1", at("14:00"), 30, "", 1)
	clash := meetingEvent("clash", "Vendor call", at("14:15"), 30, "Zoom", 1)
	invite := meetingEvent("invite", "Offsite planning", at("16:00"), 60, "", 4)
	invite.MyStatus = StatusPending
	allDay := Event{ID: "ooo", Title: "Birthday", Start: at("00:00"), End: at("00:00").Add(24 * time.Hour), AllDay: true, Availability: "busy"}
	tomorrow := meetingEvent("tmrw", "Tomorrow", mustTime("2026-10-15T09:00:00Z"), 30, "Zoom", 1)

	shape := ShapeDay([]Event{tomorrow, invite, clash, oneOnOne, lunch, design, standup, allDay}, day, loc, now)

	if len(shape.Events) != 7 {
		t.Fatalf("events in day = %d, want 7", len(shape.Events))
	}
	if shape.FirstMeeting == nil || shape.FirstMeeting.ID != "standup" {
		t.Fatalf("first meeting %+v", shape.FirstMeeting)
	}
	if len(shape.BackToBack) != 1 || shape.BackToBack[0].Events != 2 || !shape.BackToBack[0].End.Equal(at("10:45")) {
		t.Fatalf("back to back %+v", shape.BackToBack)
	}
	if len(shape.Conflicts) != 1 || shape.Conflicts[0].First.ID != "1on1" || shape.Conflicts[0].Second.ID != "clash" {
		t.Fatalf("conflicts %+v", shape.Conflicts)
	}
	if len(shape.PendingInvites) != 1 || shape.PendingInvites[0].ID != "invite" {
		t.Fatalf("pending %+v", shape.PendingInvites)
	}
	// Free: 09:00-09:30 (30 min), 10:45-12:00, 13:00-14:00, 14:45-16:00, 17:00-18:00.
	var free []string
	for _, b := range shape.FreeBlocks {
		free = append(free, b.Start.In(loc).Format("15:04")+"-"+b.End.In(loc).Format("15:04"))
	}
	want := "09:00-09:30 10:45-12:00 13:00-14:00 14:45-16:00 17:00-18:00"
	if got := strings.Join(free, " "); got != want {
		t.Fatalf("free blocks %q, want %q", got, want)
	}
	if shape.MeetingMinutes != 15+60+60+30+30+60 {
		t.Fatalf("meeting minutes %d", shape.MeetingMinutes)
	}

	facts := shape.Facts(loc)
	joined := strings.Join(facts, "\n")
	for _, want := range []string{
		"First meeting at 09:30: Standup.",
		"Back to back 09:30 to 10:45 (2 meetings).",
		"Free 09:00 to 09:30, 10:45 to 12:00, 13:00 to 14:00, 14:45 to 16:00, 17:00 to 18:00.",
		"Conflict: 1:1 overlaps Vendor call.",
		"1 invite waits for an answer: Offsite planning.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("facts missing %q in:\n%s", want, joined)
		}
	}
}

func TestShapeDayUsesLocalMidnightAcrossDST(t *testing.T) {
	for _, day := range calendarDSTTransitionDays(t) {
		t.Run(day.Format("2006-01-02"), func(t *testing.T) {
			loc := day.Location()
			next := day.AddDate(0, 0, 1)
			late := Event{ID: "late", Start: next.Add(-30 * time.Minute), End: next.Add(-15 * time.Minute)}
			tomorrow := Event{ID: "tomorrow", Start: next.Add(15 * time.Minute), End: next.Add(30 * time.Minute)}
			shape := ShapeDay([]Event{late, tomorrow}, day, loc, day.Add(12*time.Hour))
			if len(shape.Events) != 1 || shape.Events[0].ID != "late" {
				t.Fatalf("events = %+v, want only the event before local midnight", shape.Events)
			}
		})
	}
}

func calendarDSTTransitionDays(t *testing.T) []time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return []time.Time{
		time.Date(2026, 3, 8, 0, 0, 0, 0, loc),
		time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
	}
}

func TestShapeDayEmpty(t *testing.T) {
	shape := ShapeDay(nil, mustTime("2026-10-14T06:00:00Z"), time.UTC, mustTime("2026-10-14T06:00:00Z"))
	if shape.FirstMeeting != nil || len(shape.Facts(time.UTC)) != 1 {
		t.Fatalf("an empty day has only the free-all-day fact: %+v", shape.Facts(time.UTC))
	}
	if len(shape.FreeBlocks) != 1 || shape.FreeBlocks[0].Start.Hour() != WorkdayStartHour || shape.FreeBlocks[0].End.Hour() != WorkdayEndHour {
		t.Fatalf("free blocks %+v", shape.FreeBlocks)
	}
}

func TestShapeDayDeclinedNotConflict(t *testing.T) {
	at := func(hhmm string) time.Time { return mustTime("2026-10-14T" + hhmm + ":00Z") }
	a := meetingEvent("a", "A", at("10:00"), 60, "Zoom", 1)
	b := meetingEvent("b", "B", at("10:30"), 60, "Zoom", 1)
	b.MyStatus = StatusDeclined
	shape := ShapeDay([]Event{a, b}, at("06:00"), time.UTC, at("06:00"))
	if len(shape.Conflicts) != 0 {
		t.Fatalf("a declined event is not a conflict: %+v", shape.Conflicts)
	}
	if shape.MeetingMinutes != 60 {
		t.Fatalf("declined time does not count: %d", shape.MeetingMinutes)
	}
}

func TestHelpers(t *testing.T) {
	ev := meetingEvent("x", "X", mustTime("2026-10-14T10:00:00Z"), 30, "Zoom", 0)
	ev.Attendees = []Attendee{{Name: "Ann"}, {Email: "bob@example.com"}, {Name: "Cy"}, {Name: "Me", IsMe: true}}
	if got := TimeRange(ev, time.UTC); got != "10:00 to 10:30" {
		t.Fatalf("TimeRange %q", got)
	}
	ev.AllDay = true
	if got := TimeRange(ev, time.UTC); got != "All day" {
		t.Fatalf("TimeRange all day %q", got)
	}
	if got := Place(ev); got != "Zoom" {
		t.Fatalf("Place %q", got)
	}
	ev.ConferenceHost, ev.ConferenceURL, ev.Location = "", "", "Room 4"
	if got := Place(ev); got != "Room 4" {
		t.Fatalf("Place %q", got)
	}
	if got := AttendeeNames(ev, 2); got != "Ann, bob@example.com, +1" {
		t.Fatalf("AttendeeNames %q", got)
	}
	if got := AttendeeNames(ev, 0); got != "Ann, bob@example.com, Cy" {
		t.Fatalf("AttendeeNames unbounded %q", got)
	}
}

func TestDayShapeFactsOmitEmptyMeetingClockAndFreeSlots(t *testing.T) {
	for _, blocks := range [][]Block{nil, {}, {{}, {}}} {
		shape := DayShape{FirstMeeting: &Event{Title: "dinner"}, FreeBlocks: blocks}
		facts := shape.FactsIn(time.UTC, "3:04pm")
		if len(facts) != 1 || facts[0] != "First meeting: dinner." {
			t.Fatalf("empty fields rendered: %v", facts)
		}
	}
}
