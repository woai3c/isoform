package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

// snapshotBrowserEvent is the wire shape a browser client receives.
type snapshotBrowserEvent struct {
	Event string            `json:"event"`
	Args  []json.RawMessage `json:"args"`
}

// collectBrowserEvents reads everything broadcast within wait.
func collectBrowserEvents(t *testing.T, client chan []byte, wait time.Duration) []snapshotBrowserEvent {
	t.Helper()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var out []snapshotBrowserEvent
	for {
		select {
		case raw := <-client:
			var event snapshotBrowserEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			out = append(out, event)
		case <-timer.C:
			return out
		}
	}
}

func countBrowserEvents(events []snapshotBrowserEvent, name string) int {
	count := 0
	for _, event := range events {
		if event.Event == name {
			count++
		}
	}
	return count
}

// settleDayShape waits until the day's strip refresh has landed, so a test
// reads the value rather than the empty pending answer.
func settleDayShape(t *testing.T, app *App, dateISO string) {
	t.Helper()
	day, err := time.ParseInLocation("2006-01-02", dateISO, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshotValue(t, app.todayDayShapeRefresher(dateISO, day))
}

func TestTodayDayShapeSnapshotServesCachedShapeAndRefreshesOnCalendarChange(t *testing.T) {
	app, _, _, day := dayShapeHoldFixture(t)
	dateISO := day.Format("2006-01-02")
	// A cold read answers empty at once; the refresh emits when it lands.
	settleDayShape(t, app, dateISO)
	first, err := app.GetTodayDayShape(dateISO)
	if err != nil || len(first.Events) != 1 {
		t.Fatalf("first strip = %+v, %v; want the fixture's one event", first, err)
	}
	if first.Now == "" {
		t.Fatal("strip lost its clock")
	}

	start := time.Now()
	if _, err := app.GetTodayDayShape(dateISO); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cached strip read took %v", elapsed)
	}

	storeEvent(t, app.store, calendar.Event{ID: "standup", Source: calendar.SourceEventKit, Title: "Standup", Start: day.Add(10 * time.Hour), End: day.Add(10*time.Hour + 30*time.Minute)})
	app.emitBrowserEvent("calendar:synced")
	deadline := time.Now().Add(3 * time.Second)
	for {
		shape, err := app.GetTodayDayShape(dateISO)
		if err == nil && len(shape.Events) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("strip never picked up the new calendar event: %+v, %v", shape.Events, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMobileBriefHealthSnapshotEmitsOnlyOnChange(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	app.eventBroadcaster = NewEventBroadcaster()
	client := app.eventBroadcaster.AddClient("health-snapshot")
	defer app.eventBroadcaster.RemoveClient("health-snapshot")

	app.GetMobileBriefHealth()
	// The first refresh emits once; let it land before counting.
	collectBrowserEvents(t, client, 100*time.Millisecond)

	start := time.Now()
	app.GetMobileBriefHealth()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cached health read took %v", elapsed)
	}
	// A brief write that leaves health unchanged must not reach the page.
	app.emitBrowserEvent("brief:stored", "unchanged")
	if got := countBrowserEvents(collectBrowserEvents(t, client, 200*time.Millisecond), "mobile:updated"); got != 0 {
		t.Fatalf("unchanged health emitted mobile:updated %d times", got)
	}
}

func TestAboutYouPortraitSnapshotEmitsOnlyOnChange(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	app.ctx = context.Background()
	app.eventBroadcaster = NewEventBroadcaster()
	client := app.eventBroadcaster.AddClient("portrait-snapshot")
	defer app.eventBroadcaster.RemoveClient("portrait-snapshot")

	app.GetAboutYouPortrait()
	collectBrowserEvents(t, client, 100*time.Millisecond)

	start := time.Now()
	app.GetAboutYouPortrait()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cached portrait read took %v", elapsed)
	}
	// A portrait write that leaves the readable portrait unchanged stays quiet.
	app.emitPortraitUpdated()
	if got := countBrowserEvents(collectBrowserEvents(t, client, 200*time.Millisecond), portraitEvent); got != 0 {
		t.Fatalf("unchanged portrait emitted %d times", got)
	}
}

func TestDayNoticedEmitsOnlyOnChange(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	saveNoticedMoment(t, app, "m-lamp", "Comparing Halcyon Arc desk lamps for Priya", "Safari", `[{"type":"person","name":"Priya"}]`, noticedTestAt(10, 0))
	saveNoticedWithSummary(t, app, []noticedRaw{{Kind: noticedKindFollowUp, MomentID: "m-lamp", Text: "Follow up with Priya about the Halcyon Arc lamp"}})
	app.eventBroadcaster = NewEventBroadcaster()
	client := app.eventBroadcaster.AddClient("noticed-emits")
	defer app.eventBroadcaster.RemoveClient("noticed-emits")

	got, _ := app.GetDayNoticed(noticedTestDay)
	if len(got) != 1 {
		t.Fatalf("setup: %+v", got)
	}
	collectBrowserEvents(t, client, 50*time.Millisecond)

	// A write that leaves the day's suggestions unchanged stays quiet.
	app.noticedWriteWatch(noticedTestDay, false)()
	if n := countBrowserEvents(collectBrowserEvents(t, client, 50*time.Millisecond), "noticed:updated"); n != 0 {
		t.Fatalf("unchanged day emitted noticed:updated %d times", n)
	}

	// Not now changes the day, so the page hears about it once, with the date.
	if err := app.DismissNoticed(noticedTestDay, got[0].ID); err != nil {
		t.Fatal(err)
	}
	events := collectBrowserEvents(t, client, 50*time.Millisecond)
	if n := countBrowserEvents(events, "noticed:updated"); n != 1 {
		t.Fatalf("dismissal emitted noticed:updated %d times, want 1", n)
	}
	for _, event := range events {
		if event.Event != "noticed:updated" {
			continue
		}
		var payload map[string]string
		if len(event.Args) != 1 || json.Unmarshal(event.Args[0], &payload) != nil || payload["date"] != noticedTestDay || len(payload) != 1 {
			t.Fatalf("noticed:updated payload = %s, want {\"date\":%q}", event.Args, noticedTestDay)
		}
	}
	if after, _ := app.GetDayNoticed(noticedTestDay); len(after) != 0 {
		t.Fatalf("dismissed suggestion still shown: %+v", after)
	}
}

func TestTodayBeforeYouTalkNextBoundary(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.Local)
	row := func(start time.Time, person string, allDay bool) storage.CalendarEventRow {
		r := storage.CalendarEventRow{ID: "e-" + start.Format("150405"), Title: "Meeting", StartAt: start.UnixMilli(), EndAt: start.Add(30 * time.Minute).UnixMilli(), AllDay: allDay, Availability: "busy"}
		if person != "" {
			r.Attendees = []storage.CalendarAttendeeRow{{EventID: r.ID, Email: "p@example.com", PrincipalID: person}}
		}
		return r
	}
	rows := []storage.CalendarEventRow{
		row(now.Add(-time.Hour), "p1", false), // card already due
		row(now.Add(3*time.Hour), "", false),  // no person, no card
		row(now.Add(5*time.Hour), "p2", false),
		row(now.Add(3*time.Hour), "p1", false), // the next card
		row(now.Add(2*time.Hour), "p3", true),  // all day
	}
	at, ok := nextTodayBeforeYouTalkBoundary(rows, now)
	if !ok || !at.Equal(now.Add(time.Hour)) {
		t.Fatalf("next boundary = %v, %v; want one hour from now (3h meeting minus the 2h window)", at, ok)
	}
	if _, ok := nextTodayBeforeYouTalkBoundary(rows[:2], now); ok {
		t.Fatal("a schedule with no eligible meeting must arm nothing")
	}
}

func TestTodayBeforeYouTalkBoundaryTimerReplacesArm(t *testing.T) {
	var b boundaryTimer
	var early, late, cancelled atomic.Int32
	b.arm(time.Now().Add(20*time.Millisecond), func() { early.Add(1) })
	b.arm(time.Now().Add(60*time.Millisecond), func() { late.Add(1) })
	time.Sleep(150 * time.Millisecond)
	if early.Load() != 0 || late.Load() != 1 {
		t.Fatalf("replaced arm fired early=%d late=%d; want 0 and 1", early.Load(), late.Load())
	}
	b.arm(time.Now().Add(20*time.Millisecond), func() { cancelled.Add(1) })
	b.arm(time.Time{}, nil)
	time.Sleep(60 * time.Millisecond)
	if cancelled.Load() != 0 {
		t.Fatal("a cancelled arm fired")
	}
}

func TestTodayBeforeYouTalkBoundaryPublisher(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	app.eventBroadcaster = NewEventBroadcaster()
	client := app.eventBroadcaster.AddClient("before-you-talk")
	defer app.eventBroadcaster.RemoveClient("before-you-talk")

	// A meeting whose prep card becomes due in about 300 ms.
	start := time.Now().Add(todayBeforeYouTalkWindow + 300*time.Millisecond)
	ev := calendar.Event{ID: "prep-meeting", Source: calendar.SourceEventKit, Title: "Prep", Start: start, End: start.Add(30 * time.Minute)}
	row := calendarEventRow(ev)
	row.Attendees = []storage.CalendarAttendeeRow{{EventID: ev.ID, Email: "p@example.com", PrincipalID: "person-1"}}
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{row}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	app.armTodayBeforeYouTalkBoundary()
	events := collectBrowserEvents(t, client, 1200*time.Millisecond)
	if got := countBrowserEvents(events, todayBeforeYouTalkEvent); got != 1 {
		t.Fatalf("%s fired %d times for one boundary, want 1", todayBeforeYouTalkEvent, got)
	}
}
