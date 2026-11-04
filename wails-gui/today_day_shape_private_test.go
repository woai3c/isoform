package main

import (
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

// today-review v1.1.13 D7: a private item on the strip carries no names, so
// it can say only "Private".
func TestTodayDayShapePrivateEventCarriesNoNames(t *testing.T) {
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	events := []calendar.Event{
		{ID: "therapy", Title: "Therapy with Dr. Lee", Start: day.Add(14 * time.Hour), End: day.Add(15 * time.Hour)},
		{ID: "untitled", Title: " ", Start: day.Add(16 * time.Hour), End: day.Add(17 * time.Hour)},
		{ID: "dinner", Title: "Dinner with Sam", Start: day.Add(18 * time.Hour), End: day.Add(19*time.Hour + 30*time.Minute)},
	}
	got := shapeTodayCalendar(events, day, day.Add(9*time.Hour))
	if len(got.Events) != 3 {
		t.Fatalf("events = %+v", got.Events)
	}
	for _, event := range got.Events[:2] {
		if !event.Private || event.Title != "" || event.ShortName != "" || event.ShortestName != "" || event.Initial != "" {
			t.Errorf("private event shows %+v", event)
		}
	}
	if dinner := got.Events[2]; dinner.Private || dinner.ShortName != "Dinner with Sam" || dinner.ShortestName != "Dinner" {
		t.Errorf("dinner = %+v", dinner)
	}
}
