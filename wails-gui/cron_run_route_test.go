package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	agent "yansu-agent/agent"
)

func TestCronRunRouteRecordsFallbackAndReportedModel(t *testing.T) {
	var persisted []storage.CronRunRow
	recorder := &cronRunRouteRecorder{persist: func(row storage.CronRunRow) error { persisted = append(persisted, row); return nil }}
	ctx := withCronRunRoute(context.Background(), recorder)
	recordCronRunRoute(ctx, &agent.StreamEvent{Type: "run_created", Backend: "claude-code", CandidateBackends: []string{"claude-code", "native"}})
	recordCronRunRoute(ctx, &agent.StreamEvent{Type: "model_route", ServedModel: "claude-opus"})
	recordCronRunRoute(ctx, &agent.StreamEvent{Type: "run_created", Backend: "native", CandidateBackends: []string{"claude-code", "native"}, FallbackFrom: []string{"claude-code"}, BackendReason: "Claude Code: unavailable"})
	row, blocked := recorder.snapshot()
	if row.Model != "" || blocked {
		t.Fatalf("fallback inherited model or blocked state: %#v, %v", row, blocked)
	}
	recordCronRunRoute(ctx, &agent.StreamEvent{Type: "model_route", ServedModel: "gpt-5.4"})
	row, _ = recorder.snapshot()
	state := runRowToState(row)
	if state.Backend != "native" || state.Model != "gpt-5.4" || len(state.CandidateBackends) != 2 || len(state.FallbackFrom) != 1 || state.BackendReason != "Claude Code: unavailable" {
		t.Fatalf("route state = %#v", state)
	}
	if len(persisted) != 4 {
		t.Fatalf("persisted %d route changes, want 4", len(persisted))
	}
	// Ordinary text events do not write the same provenance repeatedly.
	recordCronRunRoute(ctx, &agent.StreamEvent{Type: "assistant", Backend: "native", Content: "done"})
	if len(persisted) != 4 {
		t.Fatal("ordinary stream event persisted unchanged route")
	}
}

func TestCronRunBlockedAfterActionDoesNotScheduleRetry(t *testing.T) {
	for _, test := range []struct {
		name         string
		scheduleKind string
		blockedEvent bool
		typedError   bool
		metadataFail bool
	}{
		{"manual blocked event", "manual", true, false, false},
		{"one-time typed cancellation", "at", false, true, false},
		{"recurring validation after actions", "every", false, false, false},
		{"metadata failure before successful action receipt", "every", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			scheduleKind := test.scheduleKind
			store, err := storage.New(filepath.Join(t.TempDir(), "cron.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			ctx := context.Background()
			job := &storage.CronJobRow{
				ID: "job", Name: "Playbook", Enabled: true, TargetKind: "agent", ScheduleKind: scheduleKind,
				ScheduleConfig: `{"at_ms":1,"every_ms":3600000}`, RetryPolicy: `{"max_attempts":3,"base_ms":1000}`,
				RetryAttempt: 1, RetryRunAt: 1,
			}
			if err := store.SaveCronJob(ctx, *job); err != nil {
				t.Fatal(err)
			}
			scheduler := NewCronScheduler(store, func(string, any) {}, func(string, string) {}, func(runCtx context.Context, _ *storage.CronJobRow) (*cronExecResult, error) {
				if test.metadataFail {
					recorder := runCtx.Value(cronRunRouteContextKey{}).(*cronRunRouteRecorder)
					persist := recorder.persist
					first := true
					recorder.persist = func(row storage.CronRunRow) error {
						if first {
							first = false
							return errors.New("route metadata storage unavailable")
						}
						return persist(row)
					}
				}
				if err := recordCronRunRoute(runCtx, &agent.StreamEvent{Type: "run_created", Backend: "claude-code", CandidateBackends: []string{"claude-code", "native"}}); err != nil {
					return nil, err
				}
				if test.blockedEvent {
					if err := recordCronRunRoute(runCtx, &agent.StreamEvent{Type: "error", Backend: "claude-code", Blocked: true, ActionsStarted: 1, Error: "assistant stopped after 1 action"}); err != nil {
						return nil, err
					}
				}
				if test.typedError {
					return nil, &agent.BlockedRunError{Actions: 1, Cause: context.Canceled}
				}
				if err := recordCronRunRoute(runCtx, &agent.StreamEvent{Type: "action_started", Backend: "claude-code", ActionsStarted: 1}); err != nil {
					return nil, err
				}
				return nil, errors.New("run produced no deliverable")
			})
			scheduler.jobs[job.ID] = job
			scheduler.executeJob(ctx, job, "manual")
			runs, err := store.ListCronRuns(ctx, job.ID, 10)
			if err != nil || len(runs) != 1 || runs[0].Status != "blocked" || runs[0].Backend != "claude-code" || runs[0].ActionsStarted != 1 {
				t.Fatalf("blocked history = %#v, %v", runs, err)
			}
			saved, err := store.LoadCronJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.RetryRunAt != 0 || saved.RetryAttempt != 0 || saved.ConsecutiveErrors != 1 || saved.LastRunStatus != "blocked" {
				t.Fatalf("blocked job may repeat or did not count failure: %#v", saved)
			}
			if scheduleKind == "at" && (saved.Enabled || saved.NextRunAt != 0) {
				t.Fatal("one-time run remained scheduled after an uncertain action")
			}
		})
	}
}

func TestCronRunRouteMetadataFailureRetriesOnChangedMetadata(t *testing.T) {
	var persisted []storage.CronRunRow
	recorder := &cronRunRouteRecorder{persist: func(row storage.CronRunRow) error {
		persisted = append(persisted, row)
		if len(persisted) == 1 {
			return errors.New("route metadata storage unavailable")
		}
		return nil
	}}
	ctx := withCronRunRoute(context.Background(), recorder)
	for _, event := range []*agent.StreamEvent{
		{Type: "run_created", Backend: "claude-code"},
		{Type: "assistant", Backend: "claude-code", Content: "working"},
		{Type: "model_route", Backend: "claude-code", ServedModel: "claude-opus"},
		{Type: "action_started", Backend: "claude-code", ActionsStarted: 1},
	} {
		if err := recordCronRunRoute(ctx, event); err != nil {
			t.Fatalf("%s aborted execution after metadata failure: %v", event.Type, err)
		}
	}
	row, blocked := recorder.snapshot()
	if len(persisted) != 3 || row.Model != "claude-opus" || row.ActionsStarted != 1 || blocked {
		t.Fatalf("route did not recover: attempts=%d row=%+v blocked=%v", len(persisted), row, blocked)
	}
	if persisted[2].ActionsStarted != 1 || persisted[2].Backend != "claude-code" || persisted[2].Model != "claude-opus" {
		t.Fatalf("action receipt did not recover complete attribution: %+v", persisted[2])
	}
}

func TestCronRunActionReceiptPersistenceFailureRemainsClosed(t *testing.T) {
	storageErr := errors.New("route storage unavailable")
	recorder := &cronRunRouteRecorder{persist: func(storage.CronRunRow) error { return storageErr }}
	ctx := withCronRunRoute(context.Background(), recorder)
	event := &agent.StreamEvent{Type: "action_started", Backend: "claude-code", ActionsStarted: 1}
	for i := 0; i < 2; i++ {
		if err := recordCronRunRoute(ctx, event); !errors.Is(err, storageErr) {
			t.Fatalf("event persistence returned %v, want refusal after storage failure", err)
		}
	}
	row, _ := recorder.snapshot()
	if row.ActionsStarted != 1 {
		t.Fatal("lost action count needed for blocked finalization")
	}
}

func TestCronRunRouteMetadataFailureAfterDurableActionIsBestEffort(t *testing.T) {
	writes := 0
	recorder := &cronRunRouteRecorder{persist: func(storage.CronRunRow) error {
		writes++
		if writes == 2 {
			return errors.New("model attribution storage unavailable")
		}
		return nil
	}}
	ctx := withCronRunRoute(context.Background(), recorder)
	for _, event := range []*agent.StreamEvent{
		{Type: "action_started", Backend: "claude-code", ActionsStarted: 1},
		{Type: "model_route", Backend: "claude-code", ServedModel: "claude-opus"},
		{Type: "done", Backend: "claude-code", ActionsStarted: 1},
	} {
		if err := recordCronRunRoute(ctx, event); err != nil {
			t.Fatalf("%s treated metadata failure as unsafe action persistence: %v", event.Type, err)
		}
	}
	row, blocked := recorder.snapshot()
	if writes != 2 || blocked || row.ActionsStarted != 1 || row.Model != "claude-opus" {
		t.Fatalf("metadata failure changed action status: writes=%d row=%+v blocked=%v", writes, row, blocked)
	}
}

func TestCronRunBlockedNativeEventNamesIsoform(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "cron.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	job := &storage.CronJobRow{ID: "native", Name: "Native run", Enabled: true, ScheduleKind: "manual", TargetKind: "agent"}
	if err := store.SaveCronJob(ctx, *job); err != nil {
		t.Fatal(err)
	}
	scheduler := NewCronScheduler(store, func(string, any) {}, func(string, string) {}, func(runCtx context.Context, _ *storage.CronJobRow) (*cronExecResult, error) {
		if err := recordCronRunRoute(runCtx, &agent.StreamEvent{Type: "done", Backend: "native", Blocked: true, ActionsStarted: 1}); err != nil {
			return nil, err
		}
		return &cronExecResult{Summary: "Partial result"}, nil
	})
	scheduler.jobs[job.ID] = job
	scheduler.executeJob(ctx, job, "manual")
	runs, err := store.ListCronRuns(ctx, job.ID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	if runs[0].Status != "blocked" || !strings.Contains(runs[0].Error, "Isoform stopped") || strings.Contains(runs[0].Error, "unknown outcome") {
		t.Fatalf("native failure attributed incorrectly: %#v", runs[0])
	}
}

func TestRecoveredScheduleCatchesUpOnlyNewOccurrences(t *testing.T) {
	for _, test := range []struct {
		name        string
		elapsed     time.Duration
		actions     int
		wantNewRuns int
	}{
		{"same occurrence cannot replay", time.Minute, 1, 0},
		{"later occurrence may catch up", 25 * time.Hour, 1, 1},
		{"zero-action occurrence may retry", time.Minute, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := storage.New(filepath.Join(t.TempDir(), "cron.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			ctx := context.Background()
			started := time.Now().Add(-test.elapsed).UnixMilli()
			job := storage.CronJobRow{ID: "system", Name: "System schedule", SurfaceKind: "system", Enabled: true, ScheduleKind: "every", ScheduleConfig: `{"every_ms":86400000}`, TargetKind: "agent", NextRunAt: started}
			if err := store.SaveCronJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := store.InsertCronRun(ctx, storage.CronRunRow{ID: "interrupted", JobID: job.ID, Status: "running", StartedAt: started, TriggerKind: "scheduled", Backend: "claude-code", ActionsStarted: test.actions}); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkStaleRunsAsError(ctx); err != nil {
				t.Fatal(err)
			}
			newRuns := 0
			scheduler := NewCronScheduler(store, func(string, any) {}, func(string, string) {}, func(context.Context, *storage.CronJobRow) (*cronExecResult, error) {
				newRuns++
				return &cronExecResult{Summary: "New occurrence"}, nil
			})
			if err := scheduler.loadJobs(ctx); err != nil {
				t.Fatal(err)
			}
			scheduler.startupCatchup(ctx)
			scheduler.wg.Wait()
			if newRuns != test.wantNewRuns {
				t.Fatalf("new runs = %d, want %d", newRuns, test.wantNewRuns)
			}
			saved, err := store.LoadCronJob(ctx, job.ID)
			if err != nil || !saved.Enabled {
				t.Fatalf("system schedule disabled: %#v, %v", saved, err)
			}
		})
	}
}
