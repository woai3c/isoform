package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func TestMobileBriefTimingInputMarksRequirePresentInputPoll(t *testing.T) {
	app, _, clock := mobileBriefTimingSignalFixture(t)
	idle, locked := 20*time.Second, false
	observer := &contextSignalService{
		store: app.store, loc: clock.Location(), now: func() time.Time { return clock },
		idle: func() time.Duration { return idle }, locked: func() bool { return locked },
	}
	ctx := context.Background()
	for _, kind := range []activitymon.SystemStateKind{activitymon.SystemStateWake, activitymon.SystemStateUnlocked} {
		observer.handleChange(ctx, activitymon.SystemStateChange{Kind: kind, At: clock})
	}
	if observer.marks.FirstInput != 0 || observer.marks.LastInput != 0 {
		t.Fatalf("system transitions invented input: %+v", observer.marks)
	}
	idle = 0 // The unsupported platform's presence fallback is not input.
	observer.pollIdle(ctx)
	if observer.marks.FirstInput != 0 || observer.marks.LastInput != 0 {
		t.Fatalf("unsupported idle probe invented input: %+v", observer.marks)
	}
	observer.inputTimingSupported = true
	idle = 20 * time.Second
	observer.pollIdle(ctx)
	inputAt := clock.Add(-idle)
	if observer.marks.FirstInput != inputAt.UnixMilli() || observer.marks.LastInput != inputAt.UnixMilli() {
		t.Fatalf("poll did not retain the input timestamp: %+v", observer.marks)
	}
	// The same input remains recent on the next poll. It must not count as
	// more work merely because the observer checked it again.
	clock, idle = clock.Add(20*time.Second), idle+20*time.Second
	observer.pollIdle(ctx)
	if observer.marks.LastInput != inputAt.UnixMilli() {
		t.Fatalf("repeated poll extended input: %+v", observer.marks)
	}
	for _, state := range []string{"locked", "asleep", "away", "invalid_idle"} {
		clock = clock.Add(time.Minute)
		locked, observer.state.Asleep, idle = state == "locked", state == "asleep", 0
		switch state {
		case "away":
			idle = contextPresentWithin
		case "invalid_idle":
			idle = -time.Second
		}
		observer.pollIdle(ctx)
		if observer.marks.LastInput != inputAt.UnixMilli() {
			t.Fatalf("%s invented input: %+v", state, observer.marks)
		}
	}
}

func TestMobileBriefTimingInputMarksResetAtLocalMidnight(t *testing.T) {
	app, _, clock := mobileBriefTimingSignalFixture(t)
	clock = time.Date(clock.Year(), clock.Month(), clock.Day(), 23, 59, 50, 0, clock.Location())
	idle := time.Duration(0)
	observer := &contextSignalService{
		store: app.store, loc: clock.Location(), now: func() time.Time { return clock },
		idle: func() time.Duration { return idle }, locked: func() bool { return false },
		inputTimingSupported: true,
	}
	ctx := context.Background()
	observer.pollIdle(ctx)
	previousDate, previousInput := observer.marks.Date, observer.marks.LastInput
	clock, idle = clock.Add(20*time.Second), 20*time.Second
	observer.pollIdle(ctx)
	if observer.marks.Date == previousDate || observer.marks.FirstInput != 0 || observer.marks.LastInput != 0 || observer.marks.FirstActive != clock.UnixMilli() {
		t.Fatalf("previous day's input seeded the new day: %+v", observer.marks)
	}
	previous, ok := loadContextDayMarks(ctx, app.store, previousDate)
	if !ok || previous.LastInput != previousInput {
		t.Fatalf("day reset rewrote prior input: %+v, %v", previous, ok)
	}
	clock, idle = clock.Add(30*time.Second), 10*time.Second
	observer.pollIdle(ctx)
	want := clock.Add(-idle).UnixMilli()
	if observer.marks.FirstInput != want || observer.marks.LastInput != want {
		t.Fatalf("new day did not record real input: %+v", observer.marks)
	}
	reloaded := &contextSignalService{store: app.store, loc: clock.Location(), now: func() time.Time { return clock }}
	reloaded.loadMarks(ctx)
	if reloaded.marks.FirstInput != want || reloaded.marks.LastInput != want {
		t.Fatalf("input provenance did not survive reload: %+v", reloaded.marks)
	}
}

func TestMobileBriefTimingMorningLearnsInputAfterBackgroundWake(t *testing.T) {
	app, service, today := mobileBriefTimingSignalFixture(t)
	clock, idle := today, time.Duration(0)
	observer := &contextSignalService{
		store: app.store, loc: clock.Location(), now: func() time.Time { return clock },
		idle: func() time.Duration { return idle }, locked: func() bool { return false },
		inputTimingSupported: true,
	}
	ctx := context.Background()
	for offset := -3; offset < 0; offset++ {
		day := today.AddDate(0, 0, offset)
		clock = time.Date(day.Year(), day.Month(), day.Day(), 7, 0, 0, 0, day.Location())
		observer.handleChange(ctx, activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: clock})
		clock, idle = clock.Add(2*time.Hour+20*time.Second), 20*time.Second
		observer.pollIdle(ctx)
		clock, idle = clock.Add(6*time.Minute), 20*time.Second
		observer.pollIdle(ctx)
		if observer.marks.FirstActive != clock.Add(-2*time.Hour-6*time.Minute-20*time.Second).UnixMilli() {
			t.Fatalf("legacy wake-inclusive presence behavior changed: %+v", observer.marks)
		}
	}
	input := service.timingInput(today)
	settings := mobileBriefSettings{Enabled: true, Morning: "10:00", Evening: "18:00"}
	prediction := briefTimingPredictionForSlot(t, mobileBriefPredictDay(today, today.Location(), settings, input), mobileBriefSlotMorning)
	want := time.Date(today.Year(), today.Month(), today.Day(), 8, 50, 0, 0, today.Location())
	assertBriefTimingPrediction(t, prediction, want, "learned_activity")
}

func TestMobileBriefTimingBackgroundWakeNearCapDoesNotDeferEvening(t *testing.T) {
	app, service, today := mobileBriefTimingSignalFixture(t)
	clock := time.Date(today.Year(), today.Month(), today.Day(), 9, 0, 0, 0, today.Location())
	observer := &contextSignalService{
		store: app.store, loc: clock.Location(), now: func() time.Time { return clock },
		idle: func() time.Duration { return 0 }, locked: func() bool { return false },
		inputTimingSupported: true,
	}
	ctx := context.Background()
	observer.pollIdle(ctx)
	clock = clock.Add(8 * time.Hour)
	observer.pollIdle(ctx)
	clock = clock.Add(time.Hour + 20*time.Minute)
	observer.handleChange(ctx, activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: clock})
	input := service.timingInput(clock)
	if input.Today.LastActivity.Hour() != 17 || observer.marks.LastActive != clock.UnixMilli() {
		t.Fatalf("wake changed input provenance or legacy presence: input=%+v marks=%+v", input.Today, observer.marks)
	}
	settings := mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "18:00"}
	prediction := briefTimingPredictionForSlot(t, mobileBriefPredictDay(clock, clock.Location(), settings, input), mobileBriefSlotEvening)
	want := time.Date(clock.Year(), clock.Month(), clock.Day(), 18, 0, 0, 0, clock.Location())
	assertBriefTimingPrediction(t, prediction, want, "fixed_slot")
	if !clock.Before(prediction.Deadline) {
		t.Fatalf("fixture does not exercise the still-open delivery window: %+v", prediction)
	}
}

func TestMobileBriefTimingLegacyWakeInclusiveMarksDoNotTrainOrDefer(t *testing.T) {
	app, service, now := mobileBriefTimingSignalFixture(t)
	for offset := -3; offset <= 0; offset++ {
		day := now.AddDate(0, 0, offset)
		date := day.Format("2006-01-02")
		raw, err := json.Marshal(contextDayMarks{Date: date, FirstActive: day.Add(-11 * time.Hour).UnixMilli(), LastActive: day.UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		if err := app.store.SetUIState(context.Background(), contextDayMarksKey(date), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	input := service.timingInput(now)
	for _, day := range append(input.Days, input.Today) {
		if !day.FirstActivity.IsZero() || !day.LastActivity.IsZero() {
			t.Fatalf("legacy presence became input history: %+v", day)
		}
	}
	settings := mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "18:00"}
	for _, prediction := range mobileBriefPredictDay(now, now.Location(), settings, input) {
		if prediction.Reason != "fixed_slot" {
			t.Fatalf("legacy marks changed timing: %+v", prediction)
		}
	}
}
