package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestActivityHistoricalCompositionNotifiesOwnerAndCarryDates(t *testing.T) {
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	day := today.AddDate(0, 0, -3)
	next := day.AddDate(0, 0, 1)
	first := seedThreadMoment(t, app, threadFixtureMoment{id: "first", title: "Review public project", app: "Code", start: next.Add(-30 * time.Minute), end: next.Add(-20 * time.Minute)})
	if _, err := app.MoveActivityToThread(first.ID, ""); err != nil {
		t.Fatal(err)
	}
	for _, d := range []int64{day.UnixMilli(), next.UnixMilli()} {
		if _, err := app.runActivityThreadPass(t.Context(), d, false, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := app.GetActivitiesInRange(d, activityThreadDayEnd(d)); err != nil {
			t.Fatal(err)
		}
		if _, err := app.GetDayThreads(d, activityThreadDayEnd(d)); err != nil {
			t.Fatal(err)
		}
	}
	// A run completed late, after both day projections had been cached.
	late := seedThreadMoment(t, app, threadFixtureMoment{id: "late", title: first.Title, app: "Code", start: next.Add(-10 * time.Minute), end: next.Add(20 * time.Minute)})
	if _, err := app.activity.store.db.Exec(`DELETE FROM activities WHERE id=?`, late.ID); err != nil {
		t.Fatal(err)
	}
	run, err := app.activity.store.GetTimelineRunByID("run-late")
	if err != nil {
		t.Fatal(err)
	}
	app.eventBroadcaster = NewEventBroadcaster()
	events := app.eventBroadcaster.AddClient("date-guard")
	triager := &ActivityTriager{app: app}
	if changed, err := triager.composeApplySegment(app.activity.store, &first, *run, ActivityTimelineGroupSummary{Title: first.Title}, time.Now().UnixMilli()); err != nil || !changed {
		t.Fatalf("compose: %t %v", changed, err)
	}
	select {
	case raw := <-events:
		var event struct {
			Event string `json:"event"`
			Args  []struct {
				Dates []string `json:"dates"`
				Live  bool     `json:"live"`
			} `json:"args"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		want := []string{day.Format("2006-01-02"), next.Format("2006-01-02")}
		if event.Event != "activity:activities-updated" || len(event.Args) != 1 || event.Args[0].Live || !reflect.DeepEqual(event.Args[0].Dates, want) {
			t.Fatalf("historical event: %s", raw)
		}
	default:
		t.Fatal("composer did not publish affected dates")
	}
	cards, err := app.GetActivitiesInRange(next.UnixMilli(), activityThreadDayEnd(next.UnixMilli()))
	if err != nil || len(cards) != 1 || cards[0].ActiveMs != (20*time.Minute).Milliseconds() {
		t.Fatalf("late carry cards: %+v %v", cards, err)
	}
	threads, err := app.GetDayThreads(next.UnixMilli(), activityThreadDayEnd(next.UnixMilli()))
	if err != nil || len(threads.Threads) != 1 || threads.Threads[0].ActivityIDs[0] != first.ID {
		t.Fatalf("changed carry: %+v %v", threads, err)
	}
}

func TestActivityDateUpdateLiveRequiresTodayOnly(t *testing.T) {
	app := newThreadTestApp(t)
	app.eventBroadcaster = NewEventBroadcaster()
	events := app.eventBroadcaster.AddClient("date-guard")
	today, _ := threadTestDay(t)
	for _, test := range []struct {
		start, end time.Time
		live       bool
	}{
		{today.Add(time.Hour), today.Add(2 * time.Hour), true},
		{today.Add(-time.Hour), today.Add(time.Hour), false},
	} {
		dates := map[string]bool{}
		activityAddSpanDates(dates, test.start.UnixMilli(), test.end.UnixMilli())
		app.emitActivityDateUpdate(dates)
		raw := <-events
		var event struct {
			Args []struct {
				Live bool `json:"live"`
			} `json:"args"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Args[0].Live != test.live {
			t.Fatalf("unproven live-only update: %s", raw)
		}
	}
}

func TestActivityHistoricalSettlingNotifiesDates(t *testing.T) {
	app := newThreadTestApp(t)
	today, _ := threadTestDay(t)
	day := today.AddDate(0, 0, -3)
	next := day.AddDate(0, 0, 1)
	seedThreadMoment(t, app, threadFixtureMoment{id: "quiet", title: "Review public project", app: "Code", start: next.Add(-time.Hour), end: next.Add(time.Hour)})
	app.eventBroadcaster = NewEventBroadcaster()
	events := app.eventBroadcaster.AddClient("settling-dates")
	triager := &ActivityTriager{app: app}
	triager.settleActivities(app.activity.store, time.Now().UnixMilli())
	select {
	case raw := <-events:
		var event struct {
			Args []struct {
				Dates []string `json:"dates"`
				Live  bool     `json:"live"`
			} `json:"args"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if len(event.Args) != 1 || event.Args[0].Live || !reflect.DeepEqual(event.Args[0].Dates, []string{day.Format("2006-01-02"), next.Format("2006-01-02")}) {
			t.Fatalf("settling missed historical scope: %s", raw)
		}
	default:
		t.Fatal("historical settling did not invalidate its day")
	}
}
