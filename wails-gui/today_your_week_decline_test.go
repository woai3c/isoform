package main

import "testing"

func TestTodayYourWeekDeclineIsPersistedPerWeek(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	const week = "2026-10-19"
	if declined, err := app.GetTodayYourWeekDeclined(week); err != nil || declined {
		t.Fatalf("initial decline = %t, %v", declined, err)
	}
	if err := app.DeclineTodayYourWeek(week); err != nil {
		t.Fatal(err)
	}
	// A new App using the same persisted store represents a page or app reload.
	reopened := &App{store: app.store}
	if declined, err := reopened.GetTodayYourWeekDeclined(week); err != nil || !declined {
		t.Fatalf("reopened decline = %t, %v", declined, err)
	}
	if declined, err := reopened.GetTodayYourWeekDeclined("2026-10-26"); err != nil || declined {
		t.Fatalf("other week = %t, %v", declined, err)
	}
	if err := app.DeclineTodayYourWeek("not-a-week"); err == nil {
		t.Fatal("invalid week was accepted")
	}
	if err := (&App{}).DeclineTodayYourWeek(week); err == nil {
		t.Fatal("a missing store must report the failed save")
	}
}
