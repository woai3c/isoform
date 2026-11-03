package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/platform/activitymon"
)

func enableTestAdaptiveBrief(t *testing.T, svc *mobileBriefService, input *mobileBriefTimingInput) {
	t.Helper()
	settings := svc.config.load()
	settings.Adaptive = true
	if err := svc.config.save(settings); err != nil {
		t.Fatal(err)
	}
	svc.timing = func(time.Time) mobileBriefTimingInput { return *input }
	t.Cleanup(svc.Stop)
}

func TestAdaptiveBriefRunRecordsScheduledTimeInsteadOfPreparation(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(6, 50)}
	svc, _ := newTestMobileBriefService(t, clock)
	input := mobileBriefTimingInput{}
	enableTestAdaptiveBrief(t, svc, &input)
	svc.checkDue(context.Background())
	runs := svc.runs.list()
	if len(runs) != 1 || runs[0].ScheduledAt != formatMobileBriefTime(mobileBriefTestDay(7, 0)) {
		t.Fatalf("run = %+v, want the 07:00 ready time, not the 06:50 preparation time", runs)
	}
	detail := briefScheduleDetailFrom(clock.Now(), time.UTC, svc.upcomingBrief(), mobileBriefSlotMorning)
	if len(detail.Runs) != 1 || detail.Runs[0].ScheduledAt != runs[0].ScheduledAt || detail.Runs[0].Late {
		t.Fatalf("detail = %+v, want the on-time 07:00 run", detail.Runs)
	}
}

func TestAdaptiveBriefFailedMorningIsMadeLaterAndLabelledLate(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 0)}
	svc, _ := missedBriefRig(t, clock)
	input := mobileBriefTimingInput{}
	enableTestAdaptiveBrief(t, svc, &input)
	base := svc.gather
	failing := true
	svc.gather = func(ctx context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		if failing {
			return mobileBriefInput{}, context.DeadlineExceeded
		}
		return base(ctx, slot, start, asOf)
	}
	for i := 0; i < 3; i++ {
		svc.checkDue(context.Background())
		clock.Advance(mobileBriefRetryBackoff(i + 1))
	}
	if runs := svc.runs.list(); len(runs) != 1 || runs[0].Attempts != 3 || runs[0].SameFailures != 3 {
		t.Fatalf("failed attempts = %+v", runs)
	}
	if next, found := svc.nextAdaptivePredictionFor(clock.Now(), svc.config.load(), mobileBriefSlotMorning); !found || next.BriefID != "2026-10-09-morning" {
		t.Fatalf("adaptive retry prediction = %+v, found %v", next, found)
	}
	failing = false
	clock.Set(mobileBriefTestDay(9, 0))
	svc.checkDue(context.Background())
	brief, ok := svc.store.load("2026-10-09-morning")
	if !ok || brief.Generation == nil || brief.Generation.Reason != "late" || brief.Generation.ScheduledAt != formatMobileBriefTime(mobileBriefTestDay(7, 0)) {
		t.Fatalf("late adaptive morning = %+v, found %v", brief.Generation, ok)
	}
	if runs := svc.runs.list(); len(runs) != 1 || runs[0].Status != mobileBriefRunOK {
		t.Fatalf("completed run = %+v", runs)
	}
}

func TestTodayBriefUsesTheRecordedLateCause(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(9, 23)}
	svc, _ := newTestMobileBriefService(t, clock)
	_, err := svc.runs.record(mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: formatMobileBriefTime(mobileBriefTestDay(7, 0)), Status: mobileBriefRunOK, At: formatMobileBriefTime(clock.Now()), Reason: mobileBriefMissedAsleep})
	if err != nil {
		t.Fatal(err)
	}
	run := todayBriefRecordedRun(svc, "2026-10-09-morning")
	if run == nil || !run.Late || run.Reason != mobileBriefMissedAsleep || run.ScheduledAt != formatMobileBriefTime(mobileBriefTestDay(7, 0)) {
		t.Fatalf("recorded run = %+v", run)
	}
	if todayBriefRecordedRun(svc, "other") != nil {
		t.Fatal("another brief must not inherit the cause")
	}
}

func TestMobileBriefAdaptiveDefaultAndOldConfig(t *testing.T) {
	previous := buildFlavor
	buildFlavor = ""
	t.Cleanup(func() { buildFlavor = previous })
	for _, flavor := range []string{appFlavorIsoform, appFlavorYansu} {
		t.Run(flavor, func(t *testing.T) {
			t.Setenv(appFlavorEnv, flavor)
			store := newMobileBriefConfigStore(t.TempDir())
			if err := os.WriteFile(store.path, []byte(`{"version":1,"morning":"08:00","evening":"18:00"}`), 0600); err != nil {
				t.Fatal(err)
			}
			settings := store.load()
			if settings.Adaptive != (flavor == appFlavorIsoform) {
				t.Fatalf("wrong flavor default: %+v", settings)
			}
			settings.Adaptive = !settings.Adaptive
			if err := store.save(settings); err != nil {
				t.Fatal(err)
			}
			if got := newMobileBriefConfigStore(filepath.Dir(store.path)).load(); got != settings {
				t.Fatalf("explicit choice lost: %+v", got)
			}
		})
	}
}

func TestMobileBriefAdaptiveScheduleJSONAndLegacySave(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(6, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	input := mobileBriefTimingInput{}
	enableTestAdaptiveBrief(t, svc, &input)
	var oldClient MobileBriefSchedule
	if err := json.Unmarshal([]byte(`{"enabled":true,"morning":"07:00","evening":"18:00"}`), &oldClient); err != nil {
		t.Fatal(err)
	}
	view, err := svc.SetSchedule(oldClient)
	if err != nil {
		t.Fatal(err)
	}
	if view.AdaptiveTiming == nil || !*view.AdaptiveTiming || view.NextPredictedAt != "2026-10-09T06:50:00Z" || view.NextPredictedSlot != "morning" || view.TimingReason != "fixed_slot" {
		t.Fatalf("legacy save lost adaptive choice/prediction: %+v", view)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["adaptiveTiming"] != true || wire["nextPredictedAt"] != view.NextPredictedAt {
		t.Fatalf("missing additive fields: %s", data)
	}
	// Health shows when the brief is ready, its 7:00 slot, not when
	// preparing starts (today-review v1.1.4 #2).
	if got := svc.Health(); got.NextRunAt != "2026-10-09T07:00:00Z" || got.NextSlot != view.NextPredictedSlot {
		t.Fatalf("health disagrees: %+v", got)
	}
	bridge := &mobileBridge{app: &App{mobileBriefSvc: svc}}
	response := httptest.NewRecorder()
	bridge.serveStatus(response)
	var status struct {
		Data struct {
			Schedule MobileBriefSchedule `json:"schedule"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Data.Schedule.NextPredictedAt != view.NextPredictedAt || status.Data.Schedule.TimingReason != "fixed_slot" {
		t.Fatalf("phone status lost the prediction: %s", response.Body.String())
	}
	off := false
	view.AdaptiveTiming = &off
	view, err = svc.SetSchedule(view)
	if err != nil || view.NextPredictedAt != "" || view.TimingReason != "" || svc.config.load().Adaptive {
		t.Fatalf("manual fixed mode failed: %+v, %v", view, err)
	}
}

func TestMobileBriefAdaptiveQuietSleepCapAndOnce(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 0)}
	svc, gathers := newTestMobileBriefService(t, clock)
	input := mobileBriefTimingInput{Quiet: engagement.QuietWindow{Start: 22 * time.Hour, End: 8 * time.Hour}}
	enableTestAdaptiveBrief(t, svc, &input)
	ctx := context.Background()
	svc.checkDue(ctx)
	if gathers.Load() != 1 {
		t.Fatal("configured 07:00 slot did not fire during quiet hours")
	}
	if morning, ok := svc.store.load("2026-10-09-morning"); !ok || morning.AsOf != formatMobileBriefTime(mobileBriefTestDay(7, 0)) {
		t.Fatalf("morning brief was not made at 07:00: %+v", morning.AsOf)
	}
	clock.Set(mobileBriefTestDay(8, 0))
	input.Sleeping = true
	svc.checkDue(ctx)
	if gathers.Load() != 1 {
		t.Fatal("generated while sleeping")
	}
	input.Sleeping = false
	svc.checkDue(ctx)
	svc.checkDue(ctx)
	// Reopen the durable store, then change the fixed time on the same date.
	svc.store = newMobileBriefStore(filepath.Dir(svc.config.path))
	settings := svc.config.load()
	settings.Morning = "08:00"
	if err := svc.config.save(settings); err != nil {
		t.Fatal(err)
	}
	svc.checkDue(ctx)
	if gathers.Load() != 1 || !svc.store.exists("2026-10-09-morning") {
		t.Fatalf("slot generated %d times", gathers.Load())
	}
	clock.Set(mobileBriefTestDay(18, 20))
	svc.systemStateChanged(activitymon.SystemStateChange{Kind: activitymon.SystemStateSleep, At: mobileBriefTestDay(17, 0)})
	svc.systemStateChanged(activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: clock.Now()})
	// A wake within the evening's 30-minute grace recovers it once.
	svc.checkDue(ctx)
	svc.checkDue(ctx)
	if gathers.Load() != 2 || !svc.store.exists("2026-10-09-evening") {
		t.Fatalf("the slept-through evening was not caught up once: %d gathers", gathers.Load())
	}
	if evening, _ := svc.store.load("2026-10-09-evening"); evening.Generation == nil || evening.Generation.Reason != "mac_asleep" || evening.Generation.Message != "Your evening brief, a bit late." {
		t.Fatalf("the late evening is not labelled: %+v", evening.Generation)
	}
}

func TestMobileBriefAdaptiveRechecksBeforePublication(t *testing.T) {
	// A slot is rechecked for sleep, schedule and date changes before publishing.
	for _, change := range []string{"sleep", "disabled", "manual", "date"} {
		t.Run(change, func(t *testing.T) {
			clock := &fakeMobileBriefClock{now: mobileBriefTestDay(18, 0)}
			svc, _ := newTestMobileBriefService(t, clock)
			input := mobileBriefTimingInput{}
			enableTestAdaptiveBrief(t, svc, &input)
			gather := svc.gather
			svc.gather = func(ctx context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
				result, err := gather(ctx, slot, start, asOf)
				switch change {
				case "quiet":
					input.Quiet = engagement.QuietWindow{Start: 18 * time.Hour, End: 20 * time.Hour}
				case "sleep":
					svc.systemStateChanged(activitymon.SystemStateChange{Kind: activitymon.SystemStateSleep, At: clock.Now()})
				case "disabled", "manual":
					settings := svc.config.load()
					if change == "disabled" {
						settings.Enabled = false
					} else {
						settings.Adaptive = false
					}
					if err := svc.config.save(settings); err != nil {
						t.Fatal(err)
					}
				case "activity":
					input.Today = mobileBriefTimingDay{Date: "2026-10-09", LastActivity: clock.Now()}
				case "date":
					clock.Set(mobileBriefTestDay(0, 0).AddDate(0, 0, 1))
				}
				return result, err
			}
			svc.checkDue(context.Background())
			if _, ok := svc.store.latest(); ok {
				t.Fatal("a result outside the delivery gate became visible")
			}
			if len(svc.runs.list()) != 0 {
				t.Fatal("a timing deferral was recorded as a generation failure")
			}
		})
	}
}

func TestMobileBriefAdaptivePredictionHonorsRetryCooldown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failed time.Time
		readAt time.Time
		quiet  engagement.QuietWindow
		wantAt time.Time
		slot   string
	}{
		{"retry within catch-up grace", mobileBriefTestDay(7, 0), mobileBriefTestDay(7, 1), engagement.QuietWindow{}, mobileBriefTestDay(7, 0).Add(mobileBriefRetryDelay), mobileBriefSlotMorning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeMobileBriefClock{now: tc.failed}
			svc, _ := newTestMobileBriefService(t, clock)
			input := mobileBriefTimingInput{Quiet: tc.quiet}
			enableTestAdaptiveBrief(t, svc, &input)
			gather := svc.gather
			attempts := 0
			svc.gather = func(ctx context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
				attempts++
				if attempts == 1 {
					return mobileBriefInput{}, errors.New("temporary brief source failure")
				}
				return gather(ctx, slot, start, asOf)
			}
			svc.checkDue(context.Background())
			if attempts != 1 || !svc.retryAfter.Equal(tc.failed.Add(mobileBriefRetryDelay)) {
				t.Fatalf("failure did not establish the retry cooldown: attempts=%d retry=%s", attempts, svc.retryAfter)
			}
			clock.Set(tc.readAt)
			schedule, err := svc.Schedule()
			if err != nil {
				t.Fatal(err)
			}
			wantAt := formatMobileBriefTime(tc.wantAt)
			if schedule.NextPredictedAt != wantAt || schedule.NextPredictedSlot != tc.slot {
				t.Errorf("schedule predicted %s %s, want %s %s", schedule.NextPredictedSlot, schedule.NextPredictedAt, tc.slot, wantAt)
			}
			if health := svc.Health(); health.NextRunAt != wantAt || health.NextSlot != tc.slot {
				t.Errorf("health predicted %s %s, want %s %s", health.NextSlot, health.NextRunAt, tc.slot, wantAt)
			}
			if tc.wantAt.After(tc.readAt) {
				clock.Set(tc.wantAt.Add(-time.Second))
				svc.checkDue(context.Background())
				if attempts != 1 {
					t.Fatal("execution retried before the advertised eligible time")
				}
			}
			clock.Set(tc.wantAt)
			svc.checkDue(context.Background())
			if attempts != 2 || !svc.store.exists("2026-10-09-"+tc.slot) {
				t.Fatalf("execution did not match the eligible prediction: attempts=%d", attempts)
			}
			if tc.slot == mobileBriefSlotEvening && svc.store.exists("2026-10-09-morning") {
				t.Fatal("a retry generated the morning brief after its window closed")
			}
		})
	}
}
