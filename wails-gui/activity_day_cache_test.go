package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestActivityThreadCalendarDayDST(t *testing.T) {
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = old })
	for _, test := range []struct {
		month time.Month
		day   int
		hours int64
	}{{time.March, 8, 23}, {time.November, 1, 25}} {
		start := time.Date(2026, test.month, test.day, 0, 0, 0, 0, zone)
		end := activityThreadDayEnd(start.UnixMilli())
		if got := (end - start.UnixMilli()) / time.Hour.Milliseconds(); got != test.hours {
			t.Fatalf("%s: got %d hours, want %d", start, got, test.hours)
		}
		if got := time.UnixMilli(end).In(zone); got.Hour() != 0 || got.Day() != test.day+1 {
			t.Fatalf("end is not next local midnight: %s", got)
		}
	}
}

func seedPastActivityDay(t *testing.T) (*App, ActivityRow, int64) {
	t.Helper()
	app := newThreadTestApp(t)
	day, _ := threadTestDay(t)
	day = day.AddDate(0, 0, -1)
	row := seedThreadMoment(t, app, threadFixtureMoment{id: "past-work", title: "Review public project", app: "Finder", bundle: "com.apple.finder", start: at(day, 23, 30), end: day.AddDate(0, 0, 1).Add(90 * time.Minute)})
	if _, err := app.MoveActivityToThread(row.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.runActivityThreadPass(t.Context(), day.UnixMilli(), false, time.Now()); err != nil {
		t.Fatal(err)
	}
	return app, row, day.UnixMilli()
}

func TestActivityDayCarriesMidnightThreadWithoutChangingMembership(t *testing.T) {
	app, row, day := seedPastActivityDay(t)
	first, err := app.GetDayThreads(day, activityThreadDayEnd(day))
	if err != nil || len(first.Threads) != 1 {
		t.Fatalf("first day: %+v %v", first, err)
	}
	next := activityThreadDayEnd(day)
	second, err := app.GetDayThreads(next, activityThreadDayEnd(next))
	if err != nil || len(second.Threads) != 1 || second.Threads[0].ID != first.Threads[0].ID {
		t.Fatalf("continuation lost thread identity: %+v %v", second, err)
	}
	if first.Threads[0].ActiveMs != (30*time.Minute).Milliseconds() || second.Threads[0].ActiveMs != (90*time.Minute).Milliseconds() {
		t.Fatalf("active time crosses the day edge: first=%d second=%d", first.Threads[0].ActiveMs, second.Threads[0].ActiveMs)
	}
	if got := threadOf(t, app, row.ID); got != first.Threads[0].ID {
		t.Fatalf("day read moved membership: %s", got)
	}
}

func TestActivityPastDayUsesComputedCachesAfterTTLAndInvalidatesPrivacy(t *testing.T) {
	app, row, day := seedPastActivityDay(t)
	end := activityThreadDayEnd(day)
	started := time.Now()
	cards, err := app.GetActivitiesInRange(day, end)
	if err != nil || len(cards) != 1 {
		t.Fatalf("cold cards: %+v %v", cards, err)
	}
	threads, err := app.GetDayThreads(day, end)
	if err != nil || len(threads.Threads) != 1 {
		t.Fatalf("cold threads: %+v %v", threads, err)
	}
	if cards[0].InProgress {
		t.Fatal("past day reports live work")
	}
	cold := time.Since(started)
	key := activityThreadCacheKey{store: app.activity.store, day: day}
	activityThreadCacheMu.Lock()
	entry, found := activityThreadCache[key]
	if !found {
		t.Fatal("cold read did not materialize the thread summary")
	}
	entry.at = time.Now().Add(-2 * activityThreadCacheTTL)
	activityThreadCache[key] = entry
	activityThreadCacheMu.Unlock()
	metadata := app.activity.store.readPrivacySources.metadataReads.Load()
	started = time.Now()
	again, err := app.GetActivitiesInRange(day, end)
	if err != nil || &again[0] != &cards[0] {
		t.Fatalf("past-day cards were rebuilt: %v", err)
	}
	warm, err := app.GetDayThreads(day, end)
	if err != nil || !reflect.DeepEqual(warm, threads) || app.activity.store.readPrivacySources.metadataReads.Load() != metadata {
		t.Fatalf("past-day thread summary was rebuilt after TTL: %+v %v", warm, err)
	}
	t.Logf("synthetic past-day cold=%s cached=%s", cold, time.Since(started))

	// Protection changes the live proof and withdraws the derived thread.
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, "session-"+row.ID); err != nil {
		t.Fatal(err)
	}
	protected, err := app.GetDayThreads(day, end)
	if err != nil || len(protected.Threads) != 0 {
		t.Fatalf("protected source survived completed-day cache: %+v %v", protected, err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='dismissed' WHERE id=?`, "session-"+row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	closed, err := app.GetDayThreads(day, end)
	if err != nil || len(closed.Threads) != 0 {
		t.Fatalf("Closed Eyes returned completed-day threads: %+v %v", closed, err)
	}
	if _, err := app.setClosedEyesModeEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteActivity(row.ID); err != nil {
		t.Fatal(err)
	}
	forgotten, err := app.GetActivitiesInRange(day, end)
	if err != nil || len(forgotten) != 0 {
		t.Fatalf("forgotten activity survived completed-day cards: %+v %v", forgotten, err)
	}
}

func TestActivityPastDayBusySummaryCache(t *testing.T) {
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	day := today.AddDate(0, 0, -2)
	var thread string
	for i := 0; i < 60; i++ {
		start := day.Add(8*time.Hour + time.Duration(i)*10*time.Minute)
		row := seedThreadMoment(t, app, threadFixtureMoment{id: fmt.Sprintf("busy-%02d", i), title: "Review public project", app: "Finder", bundle: "com.apple.finder", start: start, end: start.Add(8 * time.Minute)})
		if i%10 == 0 {
			thread = ""
		}
		var err error
		thread, err = app.MoveActivityToThread(row.ID, thread)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.runActivityThreadPass(t.Context(), day.UnixMilli(), false, time.Now()); err != nil {
		t.Fatal(err)
	}
	start, end := day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()
	began := time.Now()
	cards, err := app.GetActivitiesInRange(start, end)
	if err != nil || len(cards) != 60 {
		t.Fatalf("busy day cards: %d %v", len(cards), err)
	}
	threads, err := app.GetDayThreads(start, end)
	if err != nil || len(threads.Threads) != 6 {
		t.Fatalf("busy day threads: %d %v", len(threads.Threads), err)
	}
	cold := time.Since(began)
	metadata := app.activity.store.readPrivacySources.metadataReads.Load()
	began = time.Now()
	cached, err := app.GetActivitiesInRange(start, end)
	if err != nil || &cached[0] != &cards[0] {
		t.Fatalf("busy day cards did not open from cache: %v", err)
	}
	if _, err := app.GetDayThreads(start, end); err != nil {
		t.Fatal(err)
	}
	if app.activity.store.readPrivacySources.metadataReads.Load() != metadata {
		t.Fatal("cached busy day rescanned source metadata")
	}
	t.Logf("60 moments / 6 threads: cold=%s cached=%s", cold, time.Since(began))
}

func TestActivityPastDayCacheExcludesMidnightBoundaryNeighbors(t *testing.T) {
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	day := today.AddDate(0, 0, -2)
	end := day.AddDate(0, 0, 1)
	for _, fixture := range []threadFixtureMoment{
		{id: "ends-at-start", title: "Previous day", start: day.Add(-10 * time.Minute), end: day},
		{id: "inside", title: "Selected day", start: day.Add(9 * time.Hour), end: day.Add(10 * time.Hour)},
		{id: "starts-at-end", title: "Next day", start: end, end: end.Add(10 * time.Minute)},
	} {
		fixture.app, fixture.bundle = "Finder", "com.apple.finder"
		seedThreadMoment(t, app, fixture)
	}
	for i := 0; i < 2; i++ {
		views, err := app.GetActivitiesInRange(day.UnixMilli(), end.UnixMilli())
		if err != nil || len(views) != 1 || views[0].ID != "inside" {
			t.Fatalf("calendar-day cache includes a boundary neighbor: %+v %v", views, err)
		}
	}
}
