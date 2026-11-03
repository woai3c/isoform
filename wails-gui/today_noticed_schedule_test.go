package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestTodayNoticedAddsRealSchedulesAndUndoes(t *testing.T) {
	for _, variant := range []string{noticedKindFollowUp, noticedKindRoutine, "routine_untimed"} {
		kind := variant
		if variant == "routine_untimed" {
			kind = noticedKindRoutine
		}
		t.Run(variant, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newMobileBriefPurposeTestApp(t)
			app.ctx = context.Background()
			app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
			close(app.cronScheduler.loadedCh)
			saveNoticedMoment(t, app, "moment", "Comparing Halcyon Arc desk lamps for Priya", "Safari", `[{"type":"person","name":"Priya"}]`, noticedTestAt(10, 0))
			raw := noticedRaw{Kind: noticedKindFollowUp, MomentID: "moment", Text: "Ask Priya which Halcyon Arc finish they want"}
			if kind == noticedKindRoutine {
				raw = noticedRoutineRaw("moment", "Open the expense sheet", "Add the receipts")
				later := time.Now().Add(10 * time.Minute)
				raw.Routine.When = fmt.Sprintf("daily %02d:%02d", later.Hour(), later.Minute())
				if variant == "routine_untimed" {
					raw.Routine.When = ""
				}
			}
			saveNoticedWithSummary(t, app, []noticedRaw{raw})
			found, err := app.GetDayNoticed(noticedTestDay)
			if err != nil || len(found) != 1 {
				t.Fatalf("suggestion=%+v err=%v", found, err)
			}
			target := MobileBriefActivityTarget{Date: noticedTestDay, SuggestionID: found[0].ID, Action: kind}
			saved, err := app.AddTodayNoticedSchedule(target)
			if err != nil || saved.ID == "" || saved.Revision == "" {
				t.Fatalf("saved=%+v err=%v", saved, err)
			}
			row, err := app.store.LoadCronJob(t.Context(), saved.ID)
			if err != nil || row == nil || row.TargetKind != "agent" {
				t.Fatalf("schedule=%+v err=%v", row, err)
			}
			if kind == noticedKindFollowUp && (row.ScheduleKind != "manual" || !strings.Contains(row.TargetConfig, "Ask Priya")) {
				t.Fatalf("follow up=%+v", row)
			}
			if variant == noticedKindRoutine && (row.ScheduleKind != "cron" || !strings.Contains(row.TargetConfig, "Add the receipts")) {
				t.Fatalf("routine=%+v", row)
			}
			if variant == "routine_untimed" && (row.ScheduleKind != "manual" || !strings.Contains(row.TargetConfig, "Add the receipts")) {
				t.Fatalf("untimed routine=%+v", row)
			}
			var config cronTargetConfig
			if err := json.Unmarshal([]byte(row.TargetConfig), &config); err != nil || !app.todayNoticedScheduleReadable(config.TodayNoticed) {
				t.Fatalf("missing source proof: %+v %v", config.TodayNoticed, err)
			}
			if err := app.store.SetUIState(t.Context(), uiLanguageStateKey, "zh-CN"); err != nil {
				t.Fatal(err)
			}
			if !app.todayNoticedScheduleReadable(config.TodayNoticed) {
				t.Fatal("language change withdrew a readable source")
			}
			if err := app.store.SetUIState(t.Context(), uiLanguageStateKey, "en"); err != nil {
				t.Fatal(err)
			}
			app.closedEyes.enabled.Store(true)
			if _, err := app.executeAgentJob(t.Context(), row); !errors.Is(err, errTodayNoticedUnavailable) {
				t.Fatalf("withdrawn source ran: %v", err)
			}
			app.closedEyes.enabled.Store(false)
			listed := false
			for _, scheduled := range app.GetUpcoming() {
				if scheduled.JobID == saved.ID {
					listed = true
				}
			}
			if !listed {
				t.Fatal("created schedule is absent from Scheduled")
			}
			again, err := app.AddTodayNoticedSchedule(target)
			if err != nil || again.ID != saved.ID {
				t.Fatalf("duplicate=%+v err=%v", again, err)
			}
			if variant == noticedKindRoutine {
				changed := noticedRoutineRaw("moment", "Open a different sheet", "Add other receipts")
				saveNoticedWithSummary(t, app, []noticedRaw{changed})
				if _, err := app.EditTodayNoticedSchedule(target, saved, "", changed.Routine); err == nil {
					t.Fatal("edited against changed drafted steps")
				}
			}
			if err := app.UndoTodayNoticedSchedule(target, saved); err != nil {
				t.Fatal(err)
			}
			row, err = app.store.LoadCronJob(t.Context(), saved.ID)
			if !errors.Is(err, sql.ErrNoRows) || row != nil {
				t.Fatalf("after undo=%+v err=%v", row, err)
			}
		})
	}
}

func TestTodayNoticedDoesNotDeleteAChangedSchedule(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newMobileBriefPurposeTestApp(t)
	app.ctx = context.Background()
	app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	close(app.cronScheduler.loadedCh)
	saveNoticedMoment(t, app, "moment", "Comparing Halcyon Arc desk lamps for Priya", "Safari", `[{"type":"person","name":"Priya"}]`, noticedTestAt(10, 0))
	saveNoticedWithSummary(t, app, []noticedRaw{{Kind: noticedKindFollowUp, MomentID: "moment", Text: "Ask Priya which Halcyon Arc finish they want"}})
	found, _ := app.GetDayNoticed(noticedTestDay)
	target := MobileBriefActivityTarget{Date: noticedTestDay, SuggestionID: found[0].ID, Action: noticedKindFollowUp}
	saved, err := app.AddTodayNoticedSchedule(target)
	if err != nil {
		t.Fatal(err)
	}
	title := "Changed by the user"
	edited, err := app.EditTodayNoticedSchedule(target, saved, title, nil)
	if err != nil || edited.ID != saved.ID || edited.Revision == saved.Revision {
		t.Fatalf("edited=%+v err=%v", edited, err)
	}
	if err := app.UndoTodayNoticedSchedule(target, saved); err == nil {
		t.Fatal("removed an edited schedule")
	}
	row, _ := app.store.LoadCronJob(t.Context(), saved.ID)
	if row == nil || row.Name != title {
		t.Fatalf("edited schedule=%+v", row)
	}
}

func TestTodayNoticedRejectsWithdrawnSuggestions(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newMobileBriefPurposeTestApp(t)
	app.ctx = context.Background()
	app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	close(app.cronScheduler.loadedCh)
	saveNoticedMoment(t, app, "moment", "Comparing Halcyon Arc desk lamps for Priya", "Safari", `[{"type":"person","name":"Priya"}]`, noticedTestAt(10, 0))
	saveNoticedWithSummary(t, app, []noticedRaw{{Kind: noticedKindFollowUp, MomentID: "moment", Text: "Ask Priya which Halcyon Arc finish they want"}})
	found, err := app.GetDayNoticed(noticedTestDay)
	if err != nil || len(found) != 1 {
		t.Fatalf("setup=%+v err=%v", found, err)
	}
	target := MobileBriefActivityTarget{Date: noticedTestDay, SuggestionID: found[0].ID, Action: noticedKindFollowUp}
	app.closedEyes.enabled.Store(true)
	if _, err := app.AddTodayNoticedSchedule(target); err == nil {
		t.Fatal("accepted a paused suggestion")
	}
	jobs, err := app.store.ListCronJobs(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func TestTodayNoticedRollsBackIfSourceIsWithdrawnDuringAdd(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newMobileBriefPurposeTestApp(t)
	app.ctx = context.Background()
	app.cronScheduler = NewCronScheduler(app.store, func(event string, _ any) {
		if event == "cron:state" {
			app.closedEyes.enabled.Store(true)
		}
	}, func(string, string) {}, nil)
	close(app.cronScheduler.loadedCh)
	saveNoticedMoment(t, app, "moment", "Comparing Halcyon Arc desk lamps for Priya", "Safari", `[{"type":"person","name":"Priya"}]`, noticedTestAt(10, 0))
	saveNoticedWithSummary(t, app, []noticedRaw{{Kind: noticedKindFollowUp, MomentID: "moment", Text: "Ask Priya which Halcyon Arc finish they want"}})
	found, err := app.GetDayNoticed(noticedTestDay)
	if err != nil || len(found) != 1 {
		t.Fatalf("setup=%+v err=%v", found, err)
	}
	target := MobileBriefActivityTarget{Date: noticedTestDay, SuggestionID: found[0].ID, Action: noticedKindFollowUp}
	if _, err := app.AddTodayNoticedSchedule(target); err == nil {
		t.Fatal("accepted a source withdrawn while saving")
	}
	jobs, err := app.store.ListCronJobs(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func TestTodayNoticedUndoRefusesAStartedRun(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newMobileBriefPurposeTestApp(t)
	app.ctx = context.Background()
	app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	close(app.cronScheduler.loadedCh)
	saveNoticedMoment(t, app, "moment", "Comparing Halcyon Arc desk lamps for Priya", "Safari", `[{"type":"person","name":"Priya"}]`, noticedTestAt(10, 0))
	saveNoticedWithSummary(t, app, []noticedRaw{{Kind: noticedKindFollowUp, MomentID: "moment", Text: "Ask Priya which Halcyon Arc finish they want"}})
	found, err := app.GetDayNoticed(noticedTestDay)
	if err != nil || len(found) != 1 {
		t.Fatalf("setup=%+v err=%v", found, err)
	}
	target := MobileBriefActivityTarget{Date: noticedTestDay, SuggestionID: found[0].ID, Action: noticedKindFollowUp}
	saved, err := app.AddTodayNoticedSchedule(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.InsertCronRun(t.Context(), storage.CronRunRow{ID: "run", JobID: saved.ID, Status: "running", StartedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := app.UndoTodayNoticedSchedule(target, saved); err == nil {
		t.Fatal("undid a started schedule")
	}
	if row, err := app.store.LoadCronJob(t.Context(), saved.ID); err != nil || row == nil {
		t.Fatalf("schedule was removed: %+v %v", row, err)
	}
}

func TestTodayNoticedUndoSerializesWithEditAndDispatch(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newMobileBriefPurposeTestApp(t)
	app.ctx = context.Background()
	app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	close(app.cronScheduler.loadedCh)
	saveNoticedMoment(t, app, "moment", "Comparing Halcyon Arc desk lamps for Priya", "Safari", `[{"type":"person","name":"Priya"}]`, noticedTestAt(10, 0))
	saveNoticedWithSummary(t, app, []noticedRaw{{Kind: noticedKindFollowUp, MomentID: "moment", Text: "Ask Priya which Halcyon Arc finish they want"}})
	found, err := app.GetDayNoticed(noticedTestDay)
	if err != nil || len(found) != 1 {
		t.Fatalf("setup=%+v err=%v", found, err)
	}
	target := MobileBriefActivityTarget{Date: noticedTestDay, SuggestionID: found[0].ID, Action: noticedKindFollowUp}
	saved, err := app.AddTodayNoticedSchedule(target)
	if err != nil {
		t.Fatal(err)
	}
	row, err := app.store.LoadCronJob(t.Context(), saved.ID)
	if err != nil || row == nil {
		t.Fatalf("schedule=%+v err=%v", row, err)
	}
	// Dispatch reserves running before it writes its run record.
	app.cronScheduler.trackedLock()
	app.cronScheduler.running[saved.ID] = true
	app.cronScheduler.trackedUnlock()
	if err := app.UndoTodayNoticedSchedule(target, saved); err == nil {
		t.Fatal("undid a dispatched schedule before its run row existed")
	}
	app.cronScheduler.trackedLock()
	delete(app.cronScheduler.running, saved.ID)
	app.cronScheduler.trackedUnlock()

	start := make(chan struct{})
	undo := make(chan error, 1)
	edit := make(chan error, 1)
	go func() { <-start; undo <- app.UndoTodayNoticedSchedule(target, saved) }()
	go func() {
		<-start
		name := "Edited schedule"
		_, err := app.cronScheduler.UpdateJob(context.Background(), saved.ID, CronJobPatch{Name: &name})
		edit <- err
	}()
	close(start)
	undoErr, editErr := <-undo, <-edit
	current, err := app.store.LoadCronJob(t.Context(), saved.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if editErr == nil {
		if undoErr == nil || current == nil || current.Name != "Edited schedule" {
			t.Fatalf("successful edit lost: undo=%v row=%+v", undoErr, current)
		}
	} else if undoErr != nil || current != nil {
		t.Fatalf("neither edit nor undo completed: edit=%v undo=%v row=%+v", editErr, undoErr, current)
	}
	if current == nil && app.cronScheduler.dispatchJob(context.Background(), row, "manual") {
		t.Fatal("dispatched a schedule after Undo removed it")
	}
}
