package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
	"yansu-agent/agent"
)

type taskShadowObserverExactInputRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *taskShadowObserverExactInputRunner) Init(context.Context) error { return nil }
func (r *taskShadowObserverExactInputRunner) IsReady() bool              { return true }
func (r *taskShadowObserverExactInputRunner) CancelRun(string) error     { return nil }
func (r *taskShadowObserverExactInputRunner) RespondToPermission(string, string) error {
	return nil
}
func (r *taskShadowObserverExactInputRunner) CancelPermission(string) {}
func (r *taskShadowObserverExactInputRunner) Shutdown() error         { return nil }

func (r *taskShadowObserverExactInputRunner) ExecuteTask(
	_ context.Context,
	input *agent.TaskInput,
	callback agent.StreamCallback,
) (string, error) {
	phase := ""
	var response any
	switch {
	case strings.Contains(input.Prompt, "You are Yansu's task-onset detector."):
		phase = "onset"
		response = TaskOnsetResponse{Episodes: []TaskOnsetPrediction{}}
	case strings.Contains(input.Prompt, "You are Yansu's task-lifecycle observer."):
		phase = "lifecycle"
		response = TaskLifecycleResponse{Observations: []TaskLifecycleObservation{}}
	default:
		return "", fmt.Errorf("unexpected exact-input observer prompt")
	}
	r.mu.Lock()
	r.calls = append(r.calls, phase)
	r.mu.Unlock()
	payload, err := json.Marshal(response)
	if err != nil {
		return "", err
	}
	if err := callback(&agent.StreamEvent{
		Type:    "assistant",
		Content: string(payload),
	}); err != nil {
		return "", err
	}
	return "task-shadow-observer-exact-input", nil
}

func (r *taskShadowObserverExactInputRunner) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newTaskShadowObserverExactInputDispatchRig(
	t *testing.T,
) (*ActivityStore, *ActivityTriager, *taskShadowObserverExactInputRunner) {
	t.Helper()
	activityStore := newTestStore(t)
	stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	runner := &taskShadowObserverExactInputRunner{}
	app := &App{
		store:    stateStore,
		runner:   runner,
		activity: &activityMonitorState{store: activityStore},
	}
	if err := app.SaveUIState(triageEnableShadowKey, "true"); err != nil {
		t.Fatal(err)
	}
	triager := NewActivityTriager(app, nil)
	t.Cleanup(triager.Stop)
	return activityStore, triager, runner
}

func taskShadowObserverExactInputFixture() (
	sessionGroup,
	[]activitymon.ActivityEvent,
) {
	return sessionGroup{Sessions: []ActivitySessionRow{{
			ID:         "observer-exact-session",
			StartTime:  1_000,
			EndTime:    2_000,
			EventCount: 1,
			CreatedAt:  2_000,
		}}}, []activitymon.ActivityEvent{{
			Timestamp:   1_500,
			Kind:        activitymon.EventAppSwitch,
			AppName:     "Safari",
			WindowTitle: "Prepare customer brief",
		}}
}

func taskShadowObserverExactInputEffectCounts(
	t *testing.T,
	store *ActivityStore,
) map[string]int {
	t.Helper()
	tables := []string{
		"task_agent_attempts",
		"task_agent_attempt_events",
		"task_episodes",
		"task_episode_context_snapshots",
		"task_episode_hypotheses",
		"task_episode_evidence",
		"task_episode_transitions",
		"task_episode_links",
		"shadow_runs",
		"shadow_run_evidence",
		"shadow_run_observations",
		"shadow_run_evaluations",
		"shadow_execution_attempts",
		"shadow_task_learning_runs",
		"shadow_task_learning_revisions",
		"shadow_task_learning_materializations",
		"self_model_entries",
		"purpose_open_loops_v1",
		"purpose_open_loop_events_v1",
		"agency_origination_permission_requests_v1",
		"agency_permission_decision_receipts_v1",
	}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		if err := store.db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&count); err != nil {
			t.Fatalf("count exact-input effect table %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func requireTaskShadowObserverExactInputCountsEqual(
	t *testing.T,
	before, after map[string]int,
) {
	t.Helper()
	for table, beforeCount := range before {
		if after[table] != beforeCount {
			t.Fatalf(
				"duplicate observer input changed %s: before=%d after=%d",
				table,
				beforeCount,
				after[table],
			)
		}
	}
}

func taskShadowObserverExactInputTraceBySource(
	t *testing.T,
	traces []TaskShadowObserverTrace,
	sourcePipelineRunID int64,
) TaskShadowObserverTrace {
	t.Helper()
	for _, trace := range traces {
		if trace.SourcePipelineRunID == sourcePipelineRunID {
			return trace
		}
	}
	t.Fatalf("observer trace for source %d not found", sourcePipelineRunID)
	return TaskShadowObserverTrace{}
}

func TestTaskShadowObserverExactInputDispatchRecordsContainedDuplicate(t *testing.T) {
	store, triager, runner := newTaskShadowObserverExactInputDispatchRig(t)
	group, events := taskShadowObserverExactInputFixture()
	first := triager.dispatchTaskShadowFlow(
		context.Background(),
		group,
		events,
		true,
		TaskShadowObserverAdmissionEligible,
		"generic activity admission passed",
		101,
	)
	if first.Status != TaskShadowObserverDispatchRecorded || first.TraceID == "" {
		t.Fatalf("first observer dispatch=%+v", first)
	}
	triager.wg.Wait()
	if runner.CallCount() != 1 {
		t.Fatalf("first exact input model calls=%d, want 1", runner.CallCount())
	}
	before := taskShadowObserverExactInputEffectCounts(t, store)

	second := triager.dispatchTaskShadowFlow(
		context.Background(),
		group,
		events,
		true,
		TaskShadowObserverAdmissionEligible,
		"generic activity admission passed",
		102,
	)
	if second.Status != TaskShadowObserverDispatchRecorded || second.TraceID == "" ||
		!strings.Contains(second.Reason, "already handled") {
		t.Fatalf("duplicate observer dispatch=%+v", second)
	}
	triager.wg.Wait()
	if runner.CallCount() != 1 {
		t.Fatalf("duplicate exact input model calls=%d, want 1", runner.CallCount())
	}
	requireTaskShadowObserverExactInputCountsEqual(
		t,
		before,
		taskShadowObserverExactInputEffectCounts(t, store),
	)

	traces, err := store.ListTaskShadowObserverTraces(10)
	if err != nil || len(traces) != 2 {
		t.Fatalf("exact-input traces=%d err=%v", len(traces), err)
	}
	original := taskShadowObserverExactInputTraceBySource(t, traces, 101)
	duplicate := taskShadowObserverExactInputTraceBySource(t, traces, 102)
	if original.LatestEvent.Status != TaskShadowObserverEventCompleted ||
		duplicate.LatestEvent.Status != TaskShadowObserverEventSkipped ||
		duplicate.LatestEvent.Sequence != 1 {
		t.Fatalf("original=%+v duplicate=%+v", original.LatestEvent, duplicate.LatestEvent)
	}
	receipt := duplicate.LatestEvent.Payload.Duplicate
	if receipt == nil || receipt.OriginalTraceID != original.ID ||
		receipt.OriginalRootHash != original.RootHash ||
		receipt.OriginalEventHash != original.LatestEvent.PayloadHash ||
		receipt.InputIdentityHash != original.InputIdentityHash ||
		duplicate.InputIdentityHash != original.InputIdentityHash ||
		duplicate.LatestEvent.Payload.ShadowEnabledAtExecution != nil ||
		taskShadowObserverHasPartialEffect(duplicate.LatestEvent.Payload) {
		t.Fatalf("duplicate receipt=%+v trace=%+v", receipt, duplicate)
	}
	eventsByTrace := make(map[string][]TaskShadowObserverTraceEvent, len(traces))
	for _, trace := range traces {
		traceEvents, listErr := store.ListTaskShadowObserverTraceEvents(trace.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if valid, reason := verifyTaskShadowObserverTrace(trace, traceEvents); !valid {
			t.Fatalf("trace %s failed integrity: %s", trace.ID, reason)
		}
		eventsByTrace[trace.ID] = traceEvents
	}
	sessionIDsJSON, err := json.Marshal(original.SessionIDs)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := buildTaskShadowObserverCoverageSnapshot(
		[]taskShadowObserverCoverageSource{
			{
				ID:             101,
				StartedAt:      original.CreatedAt,
				CompletedAt:    original.CreatedAt,
				GroupID:        original.GroupID,
				SessionIDsJSON: string(sessionIDsJSON),
				Expected:       true,
				Admission:      original.Admission,
				DispatchStatus: TaskShadowObserverDispatchRecorded,
				TraceID:        original.ID,
			},
			{
				ID:             102,
				StartedAt:      duplicate.CreatedAt,
				CompletedAt:    duplicate.CreatedAt,
				GroupID:        duplicate.GroupID,
				SessionIDsJSON: string(sessionIDsJSON),
				Expected:       true,
				Admission:      duplicate.Admission,
				DispatchStatus: TaskShadowObserverDispatchRecorded,
				TraceID:        duplicate.ID,
			},
		},
		traces,
		eventsByTrace,
		0,
		2,
		10,
		duplicate.CreatedAt+1,
	)
	if snapshot.Health != TaskShadowObserverCoverageHealthHealthy ||
		snapshot.Counts.Covered != 2 || snapshot.Counts.DuplicateTrace != 0 ||
		snapshot.Counts.LinkMismatch != 0 {
		t.Fatalf("exact-input source coverage=%+v", snapshot)
	}
}

func TestTaskShadowObserverExactInputIdentitySeparatesContentAndFlow(t *testing.T) {
	store, triager, runner := newTaskShadowObserverExactInputDispatchRig(t)
	group, events := taskShadowObserverExactInputFixture()
	first := triager.dispatchTaskShadowFlow(
		context.Background(), group, events, true,
		TaskShadowObserverAdmissionEligible,
		"generic activity admission passed", 201,
	)
	triager.wg.Wait()
	changed := append([]activitymon.ActivityEvent(nil), events...)
	changed[0].WindowTitle = "Prepare a materially different brief"
	second := triager.dispatchTaskShadowFlow(
		context.Background(), group, changed, true,
		TaskShadowObserverAdmissionEligible,
		"generic activity admission passed", 202,
	)
	triager.wg.Wait()
	third := triager.dispatchTaskShadowFlow(
		context.Background(), group, changed, false,
		TaskShadowObserverAdmissionCooldown,
		"full onset admission did not apply", 203,
	)
	triager.wg.Wait()
	for index, receipt := range []TaskShadowObserverDispatchReceipt{first, second, third} {
		if receipt.Status != TaskShadowObserverDispatchRecorded ||
			strings.Contains(receipt.Reason, "already handled") {
			t.Fatalf("distinct observer dispatch %d=%+v", index, receipt)
		}
	}
	if runner.CallCount() != 2 {
		t.Fatalf("content-sensitive full-onset model calls=%d, want 2", runner.CallCount())
	}
	traces, err := store.ListTaskShadowObserverTraces(10)
	if err != nil || len(traces) != 3 {
		t.Fatalf("distinct exact-input traces=%d err=%v", len(traces), err)
	}
	identities := make(map[string]bool, len(traces))
	for _, trace := range traces {
		if trace.LatestEvent.Payload.Duplicate != nil {
			t.Fatalf("distinct input was marked duplicate: %+v", trace)
		}
		identities[trace.InputIdentityHash] = true
	}
	if len(identities) != 3 {
		t.Fatalf("content or flow identity aliased: %+v", traces)
	}
}

func TestTaskShadowObserverExactInputConcurrentAdmissionRunsOnce(t *testing.T) {
	store, triager, runner := newTaskShadowObserverExactInputDispatchRig(t)
	group, events := taskShadowObserverExactInputFixture()
	start := make(chan struct{})
	receipts := make([]TaskShadowObserverDispatchReceipt, 2)
	var dispatches sync.WaitGroup
	for index := range receipts {
		dispatches.Add(1)
		go func(index int) {
			defer dispatches.Done()
			<-start
			receipts[index] = triager.dispatchTaskShadowFlow(
				context.Background(), group, events, true,
				TaskShadowObserverAdmissionEligible,
				"generic activity admission passed", int64(301+index),
			)
		}(index)
	}
	close(start)
	dispatches.Wait()
	triager.wg.Wait()
	if runner.CallCount() != 1 {
		t.Fatalf("concurrent exact input model calls=%d, want 1", runner.CallCount())
	}
	for index, receipt := range receipts {
		if receipt.Status != TaskShadowObserverDispatchRecorded || receipt.TraceID == "" {
			t.Fatalf("concurrent observer dispatch %d=%+v", index, receipt)
		}
	}
	traces, err := store.ListTaskShadowObserverTraces(10)
	if err != nil || len(traces) != 2 {
		t.Fatalf("concurrent exact-input traces=%d err=%v", len(traces), err)
	}
	duplicateCount := 0
	for _, trace := range traces {
		if trace.LatestEvent.Payload.Duplicate != nil {
			duplicateCount++
		}
	}
	if duplicateCount != 1 {
		t.Fatalf("concurrent exact input duplicate receipts=%d traces=%+v", duplicateCount, traces)
	}
}

func TestTaskShadowObserverExactInputReusablePolicy(t *testing.T) {
	identity := strings.Repeat("a", 64)
	tests := []struct {
		name     string
		trace    TaskShadowObserverTrace
		reusable bool
	}{
		{
			name: "completed",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				LatestEvent:       TaskShadowObserverTraceEvent{Status: TaskShadowObserverEventCompleted},
			},
			reusable: true,
		},
		{
			name: "disabled",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				LatestEvent:       TaskShadowObserverTraceEvent{Status: TaskShadowObserverEventSkipped},
			},
			reusable: true,
		},
		{
			name: "current runtime started",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				RuntimeInstanceID: taskShadowObserverRuntimeInstanceID,
				LatestEvent:       TaskShadowObserverTraceEvent{Status: TaskShadowObserverEventStarted},
			},
			reusable: true,
		},
		{
			name: "prior runtime interrupted",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				RuntimeInstanceID: "prior-runtime",
				LatestEvent:       TaskShadowObserverTraceEvent{Status: TaskShadowObserverEventStarted},
			},
		},
		{
			name: "partial effect error",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				LatestEvent: TaskShadowObserverTraceEvent{
					Status: TaskShadowObserverEventCompletedWithErrors,
					Payload: TaskShadowObserverTraceEventPayload{
						TaskEpisodeIDs: []string{"episode"},
					},
				},
			},
			reusable: true,
		},
		{
			name: "error only",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				LatestEvent: TaskShadowObserverTraceEvent{
					Status:  TaskShadowObserverEventCompletedWithErrors,
					Payload: TaskShadowObserverTraceEventPayload{Errors: []string{"transient"}},
				},
			},
		},
		{
			name: "cancelled",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				LatestEvent:       TaskShadowObserverTraceEvent{Status: TaskShadowObserverEventCancelled},
			},
		},
		{
			name: "failed",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				LatestEvent:       TaskShadowObserverTraceEvent{Status: TaskShadowObserverEventFailed},
			},
		},
		{
			name: "duplicate receipt",
			trace: TaskShadowObserverTrace{
				InputIdentityHash: identity,
				LatestEvent: TaskShadowObserverTraceEvent{
					Status: TaskShadowObserverEventSkipped,
					Payload: TaskShadowObserverTraceEventPayload{
						Duplicate: &TaskShadowObserverDuplicateReceipt{},
					},
				},
			},
		},
		{
			name: "legacy identity",
			trace: TaskShadowObserverTrace{
				LatestEvent: TaskShadowObserverTraceEvent{Status: TaskShadowObserverEventCompleted},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reusable, reason := taskShadowObserverReusableOriginal(test.trace)
			if reusable != test.reusable || strings.TrimSpace(reason) == "" {
				t.Fatalf("reusable=%t reason=%q, want %t", reusable, reason, test.reusable)
			}
		})
	}
}

func TestTaskShadowObserverExactInputIgnoresCorruptOriginal(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	input := taskShadowObserverTestInput(
		t,
		true,
		TaskShadowObserverFlowFullOnset,
		TaskShadowObserverAdmissionEligible,
	)
	input.ID = "exact-input-corrupt-original"
	input.SourcePipelineRunID = 401
	original, err := store.RecordTaskShadowObserverTrace(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, added, err := store.CompleteTaskShadowObserverTrace(
		original.ID,
		TaskShadowObserverEventCompleted,
		TaskShadowObserverTraceEventPayload{Reason: "completed without downstream work"},
		5_000,
	); err != nil || !added {
		t.Fatalf("complete exact-input original added=%t err=%v", added, err)
	}
	retryInput := input
	retryInput.ID = "exact-input-after-corruption"
	retryInput.SourcePipelineRunID = 402
	retryInput.CreatedAt = 6_000
	if _, reusable, _, err := store.FindReusableTaskShadowObserverTrace(retryInput); err != nil || !reusable {
		t.Fatalf("valid exact-input original reusable=%t err=%v", reusable, err)
	}
	if _, err := store.db.Exec(`
		DROP TRIGGER task_shadow_observer_trace_events_v1_no_update
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE task_shadow_observer_trace_events_v1
		SET status = 'failed'
		WHERE trace_id = ? AND sequence = 2
	`, original.ID); err != nil {
		t.Fatal(err)
	}
	if found, reusable, reason, err := store.FindReusableTaskShadowObserverTrace(retryInput); err != nil || reusable || found.ID != "" || reason != "" {
		t.Fatalf(
			"corrupt exact-input original found=%+v reusable=%t reason=%q err=%v",
			found,
			reusable,
			reason,
			err,
		)
	}
}

func TestTaskShadowObserverDuplicatePayloadRejectsImpliedWork(t *testing.T) {
	base := TaskShadowObserverTraceEventPayload{
		FirstRunDecision: TaskShadowObserverFirstRunUnknown,
		Duplicate: &TaskShadowObserverDuplicateReceipt{
			PolicyVersion:     TaskShadowObserverDuplicatePolicyVersion,
			OriginalTraceID:   "original-trace",
			OriginalRootHash:  strings.Repeat("a", 64),
			OriginalEventHash: strings.Repeat("b", 64),
			OriginalStatus:    TaskShadowObserverEventCompleted,
			InputIdentityHash: strings.Repeat("c", 64),
		},
	}
	tests := []struct {
		name   string
		mutate func(*TaskShadowObserverTraceEventPayload)
	}{
		{
			name: "execution state",
			mutate: func(payload *TaskShadowObserverTraceEventPayload) {
				payload.ShadowEnabledAtExecution = taskShadowObserverBool(false)
			},
		},
		{
			name: "First Run decision",
			mutate: func(payload *TaskShadowObserverTraceEventPayload) {
				payload.FirstRunDecision = TaskShadowObserverFirstRunIncluded
			},
		},
		{
			name: "continuation metadata",
			mutate: func(payload *TaskShadowObserverTraceEventPayload) {
				payload.Continuation.Version = taskShadowContinuationVersion
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := base
			test.mutate(&payload)
			if _, err := normalizeTaskShadowObserverEventPayload(payload); err == nil {
				t.Fatal("duplicate payload with implied work was accepted")
			}
		})
	}
}

func TestTaskShadowObserverExactInputRefreshesOriginalBeforeReceipt(t *testing.T) {
	t.Run("completed original", func(t *testing.T) {
		store := newTaskShadowDebugTestStore(t)
		input := taskShadowObserverTestInput(
			t,
			true,
			TaskShadowObserverFlowFullOnset,
			TaskShadowObserverAdmissionEligible,
		)
		input.ID = "exact-input-stale-started-original"
		input.SourcePipelineRunID = 501
		stale, err := store.RecordTaskShadowObserverTrace(input)
		if err != nil {
			t.Fatal(err)
		}
		terminal, added, err := store.CompleteTaskShadowObserverTrace(
			stale.ID,
			TaskShadowObserverEventCompleted,
			TaskShadowObserverTraceEventPayload{Reason: "completed while duplicate admission waited"},
			5_000,
		)
		if err != nil || !added {
			t.Fatalf("complete stale original added=%t err=%v", added, err)
		}
		duplicateInput := input
		duplicateInput.ID = "exact-input-after-original-completed"
		duplicateInput.SourcePipelineRunID = 502
		duplicateInput.CreatedAt = 6_000
		duplicate, err := store.RecordTaskShadowObserverDuplicateTrace(
			duplicateInput,
			stale,
		)
		if err != nil {
			t.Fatal(err)
		}
		receipt := duplicate.LatestEvent.Payload.Duplicate
		if receipt == nil || receipt.OriginalStatus != TaskShadowObserverEventCompleted ||
			receipt.OriginalEventHash != terminal.PayloadHash {
			t.Fatalf("refreshed duplicate receipt=%+v terminal=%+v", receipt, terminal)
		}
	})

	t.Run("retryable original", func(t *testing.T) {
		store := newTaskShadowDebugTestStore(t)
		input := taskShadowObserverTestInput(
			t,
			true,
			TaskShadowObserverFlowFullOnset,
			TaskShadowObserverAdmissionEligible,
		)
		input.ID = "exact-input-stale-retryable-original"
		input.SourcePipelineRunID = 511
		stale, err := store.RecordTaskShadowObserverTrace(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, added, err := store.CompleteTaskShadowObserverTrace(
			stale.ID,
			TaskShadowObserverEventCompletedWithErrors,
			TaskShadowObserverTraceEventPayload{
				Reason: "transient dependency failure",
				Errors: []string{"dependency unavailable"},
			},
			5_000,
		); err != nil || !added {
			t.Fatalf("complete retryable original added=%t err=%v", added, err)
		}
		duplicateInput := input
		duplicateInput.ID = "exact-input-after-retryable-original"
		duplicateInput.SourcePipelineRunID = 512
		duplicateInput.CreatedAt = 6_000
		if _, err := store.RecordTaskShadowObserverDuplicateTrace(
			duplicateInput,
			stale,
		); !errors.Is(err, errTaskShadowObserverDuplicateOriginalNotReusable) {
			t.Fatalf("retryable stale original error=%v", err)
		}
	})
}
