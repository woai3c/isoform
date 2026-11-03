package main

import (
	"context"
	"testing"
	"time"
)

func TestBriefScheduleDetailListsTheLastThreeWithTheRecordedReasonOnly(t *testing.T) {
	now := mobileBriefTestDay(14, 0)
	settings := defaultMobileBriefSettings()
	settings.Enabled = true
	day := func(offset int) string { return formatMobileBriefTime(mobileBriefTestDay(7, 0).AddDate(0, 0, offset)) }
	state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{
		{BriefID: "2026-10-05-morning", Slot: "morning", ScheduledAt: day(-4), Status: mobileBriefRunOK, At: day(-4)},
		{BriefID: "2026-10-06-morning", Slot: "morning", ScheduledAt: day(-3), Status: mobileBriefRunMissed, Reason: mobileBriefMissedClosed},
		{BriefID: "2026-10-07-morning", Slot: "morning", ScheduledAt: day(-2), Status: mobileBriefRunMissed, Reason: "guessed"},
		{BriefID: "2026-10-08-evening", Slot: "evening", ScheduledAt: day(-1), Status: mobileBriefRunOK, At: day(-1)},
		{BriefID: "2026-10-08-morning", Slot: "morning", ScheduledAt: day(-1), Status: mobileBriefRunMissed, Reason: mobileBriefMissedAsleep},
		{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: day(0), Status: mobileBriefRunFailed, Attempts: 1},
	}, Exists: func(string) bool { return false }}

	detail := briefScheduleDetailFrom(now, time.UTC, state, "morning")
	if detail.Slot != "morning" || !detail.Enabled || detail.Time != settings.Morning || detail.NextRunAt == 0 {
		t.Fatalf("detail = %+v", detail)
	}
	// Today's failed run comes first with its tries and next try (tasks-review v1.0.8 #2).
	want := []BriefScheduleRun{
		{BriefID: "2026-10-09-morning", Status: mobileBriefRunFailed, ScheduledAt: day(0), Attempts: 1, NextTryAt: formatMobileBriefTime(time.UnixMilli(detail.NextRunAt))},
		{BriefID: "2026-10-08-morning", Status: mobileBriefRunMissed, ScheduledAt: day(-1), Reason: mobileBriefMissedAsleep},
		{BriefID: "2026-10-07-morning", Status: mobileBriefRunMissed, ScheduledAt: day(-2)},
	}
	if len(detail.Runs) != len(want) {
		t.Fatalf("runs = %+v, want the last three finished morning runs", detail.Runs)
	}
	for i := range want {
		if detail.Runs[i] != want[i] {
			t.Fatalf("run %d = %+v, want %+v", i, detail.Runs[i], want[i])
		}
	}
}

func TestBriefScheduleDetailRefusesAnUnknownSlot(t *testing.T) {
	if _, err := (&App{}).GetBriefScheduleDetail("noon"); err != errBriefScheduleSlot {
		t.Fatalf("err = %v, want the slot refusal", err)
	}
	if _, err := (&App{}).GetBriefScheduleDetail("morning"); err == nil {
		t.Fatal("no brief service must be an error, not an empty detail")
	}
}

func TestBriefScheduleDetailProjectsLegacyEveningReadyTime(t *testing.T) {
	settings := defaultMobileBriefSettings()
	settings.Evening = "18:00"
	at := func(day, hour, minute int) string {
		return formatMobileBriefTime(time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC))
	}
	state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{
		{BriefID: "2026-10-07-evening", Slot: "evening", Status: mobileBriefRunOK, ScheduledAt: at(7, 17, 50), At: at(7, 17, 55)},
		{BriefID: "2026-10-08-evening", Slot: "evening", Status: mobileBriefRunOK, ScheduledAt: at(8, 17, 50), At: at(8, 18, 45), Reason: mobileBriefMissedClosed},
		{BriefID: "2026-10-09-evening", Slot: "evening", Status: mobileBriefRunOK, ScheduledAt: at(9, 18, 0), At: at(9, 17, 55)},
	}}
	runs := briefScheduleDetailFrom(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), time.UTC, state, mobileBriefSlotEvening).Runs
	if len(runs) != 3 {
		t.Fatalf("runs = %+v", runs)
	}
	for i, day := range []int{9, 8, 7} {
		if runs[i].ScheduledAt != at(day, 18, 0) {
			t.Fatalf("run %d scheduled = %q", day, runs[i].ScheduledAt)
		}
	}
	if runs[0].Late || !runs[1].Late || runs[1].At != at(8, 18, 45) || runs[1].Reason != mobileBriefMissedClosed || runs[2].Late {
		t.Fatalf("late classification = %+v", runs)
	}
	settings.Evening = "19:00"
	state.Settings = settings
	runs = briefScheduleDetailFrom(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), time.UTC, state, mobileBriefSlotEvening).Runs
	if runs[2].ScheduledAt != at(7, 17, 50) {
		t.Fatalf("unrelated historical time was rewritten: %+v", runs[2])
	}
}

func TestBriefScheduleDetailWalkthroughStoredEveningHistory(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 11, 2, 13, 30, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	if err := svc.config.save(mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}); err != nil {
		t.Fatal(err)
	}
	for _, day := range []int{30, 31} {
		prep := time.Date(2026, 10, day, 17, 50, 0, 0, time.UTC)
		if _, err := svc.runs.record(mobileBriefRun{BriefID: prep.Format("2006-01-02") + "-evening", Slot: mobileBriefSlotEvening, Status: mobileBriefRunOK, ScheduledAt: formatMobileBriefTime(prep), At: formatMobileBriefTime(prep.Add(time.Minute))}); err != nil {
			t.Fatal(err)
		}
	}
	prep := time.Date(2026, 11, 1, 17, 50, 0, 0, time.UTC)
	if _, err := svc.runs.record(mobileBriefRun{BriefID: "2026-11-01-evening", Slot: mobileBriefSlotEvening, Status: mobileBriefRunOK, ScheduledAt: formatMobileBriefTime(prep), At: formatMobileBriefTime(prep.Add(11 * time.Minute))}); err != nil {
		t.Fatal(err)
	}
	detail := briefScheduleDetailFrom(clock.Now(), time.UTC, svc.upcomingBrief(), mobileBriefSlotEvening)
	if len(detail.Runs) != 3 {
		t.Fatalf("stored runs = %+v", detail.Runs)
	}
	for _, run := range detail.Runs {
		at, err := time.Parse(time.RFC3339, run.ScheduledAt)
		if err != nil || at.Hour() != 18 || at.Minute() != 0 || run.Late {
			t.Fatalf("stored evening run = %+v", run)
		}
	}
}

func TestBriefScheduleDetailPreservesProvenEarlierSchedule(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{{
		BriefID: "2026-10-31-evening", Slot: mobileBriefSlotEvening, Status: mobileBriefRunOK,
		ScheduledAt: "2026-10-31T17:50:00Z", ScheduledReady: true, At: "2026-10-31T17:49:00Z",
	}}}
	runs := briefScheduleDetailFrom(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC), time.UTC, state, mobileBriefSlotEvening).Runs
	if len(runs) != 1 || runs[0].ScheduledAt != "2026-10-31T17:50:00Z" {
		t.Fatalf("proven earlier schedule was changed: %+v", runs)
	}
}

func TestBriefScheduleDetailUsesStoredBriefTimingEvidence(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	svc, _ := newTestMobileBriefService(t, clock)
	if err := svc.config.save(mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}); err != nil {
		t.Fatal(err)
	}
	brief := mobileBriefStoredBrief(t, "2026-10-31", mobileBriefSlotEvening, 18)
	brief.Generation = &MobileBriefGeneration{ScheduledAt: "2026-10-31T17:50:00Z"}
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.runs.record(mobileBriefRun{BriefID: brief.BriefID, Slot: mobileBriefSlotEvening, Status: mobileBriefRunOK, ScheduledAt: "2026-10-31T17:40:00Z", At: "2026-10-31T18:45:00Z"}); err != nil {
		t.Fatal(err)
	}
	detail := briefScheduleDetailFrom(clock.Now(), time.UTC, svc.upcomingBrief(), mobileBriefSlotEvening)
	if len(detail.Runs) != 1 || detail.Runs[0].ScheduledAt != "2026-10-31T17:50:00Z" || !detail.Runs[0].Late {
		t.Fatalf("stored timing evidence lost: %+v", detail.Runs)
	}
}

func TestBriefMissedReasonComesOnlyFromWhatWasObserved(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(18, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	at := mobileBriefTestDay(7, 0)
	if got := svc.missedReason(at); got != "" {
		t.Fatalf("nothing observed: reason = %q, want none", got)
	}
	svc.startedAt = mobileBriefTestDay(9, 0)
	if got := svc.missedReason(at); got != mobileBriefMissedClosed {
		t.Fatalf("started after the slot: reason = %q, want closed", got)
	}
	svc.wakeWindows = []mobileBriefWakeWindow{{SleepAt: mobileBriefTestDay(6, 0), WakeAt: mobileBriefTestDay(8, 0)}}
	if got := svc.missedReason(at); got != mobileBriefMissedAsleep {
		t.Fatalf("asleep at the slot: reason = %q, want asleep", got)
	}
	svc.wakeWindows, svc.startedAt = nil, time.Time{}
	svc.sleeping, svc.sleepAt = true, mobileBriefTestDay(6, 30)
	if got := svc.missedReason(at); got != mobileBriefMissedAsleep {
		t.Fatalf("still asleep since before the slot: reason = %q, want asleep", got)
	}
}

func TestBriefMissedSlotRecordsWhySleptThroughAndNotWhenItKeptFailing(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(18, 0)}
	svc, _ := missedBriefRig(t, clock)
	svc.wakeWindows = []mobileBriefWakeWindow{{SleepAt: mobileBriefTestDay(6, 0), WakeAt: mobileBriefTestDay(17, 59)}}
	svc.checkDue(context.Background())
	runs := svc.runs.list()
	if len(runs) == 0 || runs[0].BriefID != "2026-10-09-morning" || runs[0].Status != mobileBriefRunMissed || runs[0].Reason != mobileBriefMissedAsleep {
		t.Fatalf("runs = %+v, want the morning missed while asleep", runs)
	}

	store := newMobileBriefRunStore(t.TempDir())
	scheduled := formatMobileBriefTime(mobileBriefTestDay(7, 0))
	_, _ = store.record(mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: scheduled, Status: mobileBriefRunFailed, At: scheduled})
	missed, _ := store.record(mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: scheduled, Status: mobileBriefRunMissed, At: scheduled, Reason: mobileBriefMissedAsleep})
	if missed.Reason != "" || missed.Attempts != 1 {
		t.Fatalf("a slot that failed then closed = %+v, want no sleep reason", missed)
	}
}
