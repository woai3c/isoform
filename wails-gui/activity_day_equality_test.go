package main

import (
	"reflect"
	"testing"
	"time"
)

func TestActivityPastDayColdWarmRebuiltEquality(t *testing.T) {
	app, _, day := seedPastActivityDay(t)
	start := time.UnixMilli(day)
	for _, fixture := range []threadFixtureMoment{
		{id: "desk-second", title: "Inspect another public project", app: "Finder", bundle: "com.apple.finder", start: start.Add(9 * time.Hour), end: start.Add(9*time.Hour + 20*time.Minute)},
		{id: "desk-third", title: "Read public documentation", app: "Finder", bundle: "com.apple.finder", start: start.Add(11 * time.Hour), end: start.Add(11*time.Hour + 10*time.Minute)},
	} {
		row := seedThreadMoment(t, app, fixture)
		if _, err := app.MoveActivityToThread(row.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.runActivityThreadPass(t.Context(), day, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []int64{day, activityThreadDayEnd(day)} {
		end := activityThreadDayEnd(selected)
		coldCards, err := app.GetActivitiesInRange(selected, end)
		if err != nil {
			t.Fatal(err)
		}
		coldThreads, err := app.GetDayThreads(selected, end)
		if err != nil {
			t.Fatal(err)
		}
		warmCards, err := app.GetActivitiesInRange(selected, end)
		if err != nil {
			t.Fatal(err)
		}
		warmThreads, err := app.GetDayThreads(selected, end)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(coldCards, warmCards) || !reflect.DeepEqual(coldThreads, warmThreads) {
			t.Fatal("warm read differs in content or order")
		}
		activityDayCards.Lock()
		delete(activityDayCards.entries, activityThreadCacheKey{store: app.activity.store, day: selected})
		activityDayCards.Unlock()
		activityThreadCacheForget(selected)
		rebuiltCards, err := app.GetActivitiesInRange(selected, end)
		if err != nil {
			t.Fatal(err)
		}
		rebuiltThreads, err := app.GetDayThreads(selected, end)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(coldCards, rebuiltCards) || !reflect.DeepEqual(coldThreads, rebuiltThreads) {
			t.Fatal("uncached rebuild differs in content or order")
		}
		t.Logf("day %d: %d cards, %d threads match cold/warm/rebuild", selected, len(coldCards), len(coldThreads.Threads))
	}
}
