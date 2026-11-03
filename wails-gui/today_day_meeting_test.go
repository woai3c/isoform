package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func TestTodayDayMeetingTargetRechecksExactMeetingAndPrivacy(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	setCalendarFirstDayPermission(t, app, true)
	now := time.Now().In(time.Local)
	person := peopleTestContact(t, app, "Mei Chen")
	future := beforeYouTalkEvent("future", person.ID, now.Add(3*time.Hour), now.Add(4*time.Hour))
	// Keep the fixture within Today or Tomorrow even near midnight.
	personal := beforeYouTalkEvent("personal", person.ID, now.Add(time.Hour), now.Add(2*time.Hour))
	personal.CalendarTitle = "Personal"
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{future, personal}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	target, err := app.todayDayMeetingTargetAt(context.Background(), future.ID, now)
	if err != nil || target.Kind != "prep" || target.Meeting == nil || target.Meeting.MeetingID != future.ID || target.Meeting.PersonID != person.ID {
		t.Fatalf("meeting target = %+v, %v", target, err)
	}
	published, err := app.GetTodayDayMeetingTarget(future.ID)
	if err != nil || published.Meeting == nil || published.Meeting.MeetingID != future.ID {
		t.Fatalf("published exact meeting = %+v, %v", published, err)
	}
	if _, err := app.todayDayMeetingTargetAt(context.Background(), personal.ID, now); err == nil {
		t.Fatal("personal time became a meeting target")
	}
	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := app.todayDayMeetingTargetAt(context.Background(), future.ID, now); err == nil {
		t.Fatal("paused calendar remained readable")
	}
	if _, err := app.GetTodayDayMeetingTarget(future.ID); err == nil {
		t.Fatal("paused calendar target was published")
	}
}

func TestTodayDayMeetingTargetOpensOnlyReadablePastNotes(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	setCalendarFirstDayPermission(t, app, true)
	now := time.Now().In(time.Local)
	// Use a short interval so it remains in today's strip near midnight.
	start := now.Add(-time.Minute)
	if start.Day() != now.Day() {
		now = time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
		start = now.Add(-time.Minute)
	}
	session := ActivitySessionRow{ID: "past-session", StartTime: start.UnixMilli(), EndTime: now.UnixMilli(), TriggerKind: "meeting_ended", AppNames: `["Code"]`, TriageStatus: "dismissed", IsMeeting: true, MeetingApp: "Code", CreatedAt: start.UnixMilli()}
	if err := app.activity.store.SaveSession(session, []activitymon.ActivityEvent{{Timestamp: start.UnixMilli(), AppName: "Code", AppBundleID: "com.microsoft.VSCode", WindowTitle: "Review"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.saveMeetingNotesSlice(meetingNotesHeader{Key: "call", MeetingApp: "Code", Title: "Review"}, session.ID, session.StartTime, session.EndTime, 10); err != nil {
		t.Fatal(err)
	}
	event := beforeYouTalkEvent("past", "", start, now)
	event.SessionID = session.ID
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{event}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stored, found, err := app.store.GetCalendarEvent(context.Background(), event.ID)
	if err != nil || !found {
		t.Fatalf("stored meeting: %v, %v", found, err)
	}
	policy, err := app.mcpReadPrivacy(context.Background())
	if err != nil || !policy.calendarAllowed(stored) {
		t.Fatalf("calendar policy: %v", err)
	}
	notes, err := app.GetMeetingNotesForSessions([]string{session.ID})
	if err != nil || len(notes) == 0 {
		t.Fatalf("readable notes: %d, %v", len(notes), err)
	}
	target, err := app.todayDayMeetingTargetAt(context.Background(), event.ID, now)
	if err != nil || target.Kind != "notes" || target.SessionID != session.ID {
		t.Fatalf("past target = %+v, %v", target, err)
	}
	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := app.todayDayMeetingTargetAt(context.Background(), event.ID, now); err == nil {
		t.Fatal("paused notes remained readable")
	}
}

func TestTodayDayMeetingPrepUsesExactMeetingBeyondTwoHours(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	setCalendarFirstDayPermission(t, app, true)
	now := time.Now()
	person := peopleTestContact(t, app, "Mei Chen")
	event := beforeYouTalkEvent("later", person.ID, now.Add(3*time.Hour), now.Add(4*time.Hour))
	if err := app.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "calendar", SourceID: event.ID, MeetingPrep: true, MeetingStartMs: event.StartAt, GeneratedTitle: event.Title, GeneratedPeople: []string{person.DisplayName}}
	if _, err := app.buildTodayChatContext(t.Context(), ref); err != nil {
		t.Fatalf("day strip prep unavailable: %v", err)
	}
	ref.MeetingStartMs--
	if _, err := app.buildTodayChatContext(t.Context(), ref); err == nil {
		t.Fatal("stale meeting time accepted")
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	ref.MeetingStartMs = event.StartAt
	if _, err := app.buildTodayChatContext(t.Context(), ref); err == nil {
		t.Fatal("paused calendar context accepted")
	}
}

func TestTodayPrepMeetingByPersonIDNeedsExactlyOneMeetingStillToComeToday(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	setCalendarFirstDayPermission(t, app, true)
	local := time.Now().In(time.Local)
	now := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, time.Local)
	mei := peopleTestContact(t, app, "Mei Chen")
	ana := peopleTestContact(t, app, "Ana Ruiz")
	sam := peopleTestContact(t, app, "Sam Okafor")
	meiLater := beforeYouTalkEvent("mei-later", mei.ID, now.Add(2*time.Hour), now.Add(3*time.Hour))
	meiEarlier := beforeYouTalkEvent("mei-earlier", mei.ID, now.Add(-2*time.Hour), now.Add(-time.Hour))
	meiTomorrow := beforeYouTalkEvent("mei-tomorrow", mei.ID, now.Add(26*time.Hour), now.Add(27*time.Hour))
	anaOne := beforeYouTalkEvent("ana-one", ana.ID, now.Add(time.Hour), now.Add(2*time.Hour))
	anaTwo := beforeYouTalkEvent("ana-two", ana.ID, now.Add(4*time.Hour), now.Add(5*time.Hour))
	// Sam is only named in the title: a name never finds a meeting.
	named := beforeYouTalkEvent("named", "", now.Add(3*time.Hour), now.Add(4*time.Hour))
	named.Title = "Sam Okafor 1:1"
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{meiLater, meiEarlier, meiTomorrow, anaOne, anaTwo, named}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if id, err := app.todayPrepMeetingForPersonAt(context.Background(), mei.ID, now); err != nil || id != meiLater.ID {
		t.Fatalf("one meeting still to come = %q, %v", id, err)
	}
	for name, person := range map[string]string{"two meetings": ana.ID, "name only": sam.ID, "no person": "", "model name": "Mei Chen"} {
		if id, err := app.todayPrepMeetingForPersonAt(context.Background(), person, now); err == nil || id != "" {
			t.Fatalf("%s opened %q", name, id)
		}
	}
	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	if id, err := app.todayPrepMeetingForPersonAt(context.Background(), mei.ID, now); err == nil {
		t.Fatalf("paused calendar opened %q", id)
	}
}
