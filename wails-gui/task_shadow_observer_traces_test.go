package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
	"yansu-agent/agent"
)

const taskShadowObserverPrivateTestValue = "private-observer-payload-must-not-persist"

type taskShadowObserverPanicRunner struct {
	taskShadowRolloutFakeRunner
}

func (r *taskShadowObserverPanicRunner) ExecuteTask(
	context.Context,
	*agent.TaskInput,
	agent.StreamCallback,
) (string, error) {
	panic(taskShadowObserverPrivateTestValue)
}

type taskShadowObserverOnsetErrorRunner struct {
	taskShadowRolloutFakeRunner
}

func (r *taskShadowObserverOnsetErrorRunner) ExecuteTask(
	context.Context,
	*agent.TaskInput,
	agent.StreamCallback,
) (string, error) {
	return "", fmt.Errorf("onset provider: %s", taskShadowObserverPrivateTestValue)
}

func taskShadowObserverTestInput(
	t *testing.T,
	enabled bool,
	flow, admission string,
) TaskShadowObserverTraceInput {
	t.Helper()
	group := sessionGroup{Sessions: []ActivitySessionRow{
		{
			ID:         "observer-session-a",
			StartTime:  1_000,
			EndTime:    2_000,
			EventCount: 2,
			CreatedAt:  900,
		},
		{
			ID:         "observer-session-b",
			StartTime:  2_100,
			EndTime:    3_000,
			EventCount: 1,
			CreatedAt:  2_050,
		},
	}}
	events := []activitymon.ActivityEvent{
		{
			Timestamp: 1_100,
			Kind:      activitymon.EventKeyboard,
			TypedText: taskShadowObserverPrivateTestValue,
		},
		{
			Timestamp:  2_200,
			Kind:       activitymon.EventSpeech,
			SpeechText: taskShadowObserverPrivateTestValue,
		},
	}
	input, err := newTaskShadowObserverTraceInput(
		group,
		events,
		flow,
		admission,
		"generic test admission",
		enabled,
		0,
		4_000,
	)
	if err != nil {
		t.Fatalf("build observer trace input: %v", err)
	}
	return input
}

func taskShadowObserverTerminalPayload() TaskShadowObserverTraceEventPayload {
	return TaskShadowObserverTraceEventPayload{
		Reason:                     "Observer completed without a production run",
		ShadowEnabledAtExecution:   taskShadowObserverBool(true),
		FirstRunDecision:           TaskShadowObserverFirstRunIncluded,
		OnsetWindowCount:           2,
		RetainedOnsetEvidenceCount: 7,
		ProductionOnsetAttemptIDs:  []string{"production-attempt"},
		DiagnosticOnsetAttemptIDs:  []string{"diagnostic-attempt"},
		LifecycleAttemptIDs:        []string{"lifecycle-attempt"},
		TaskEpisodeIDs:             []string{"episode-a"},
		ShadowRunIDs:               []string{"run-a"},
		WorkflowDecisions: []TaskShadowWorkflowReadinessDecision{
			{
				Source:                   TaskShadowWorkflowDecisionCapturedOnset,
				TaskEpisodeID:            "episode-a",
				ProductionOnsetAttemptID: "production-attempt",
				Workflow:                 TaskWorkflowComposeText,
				State:                    TaskShadowWorkflowStateRunCreated,
				Reason:                   "created from immutable onset context",
				FrozenContextHash:        strings.Repeat("a", 64),
				FrozenManifestCount:      3,
				FrozenIncludedCount:      2,
				HasExactActivitySnapshot: true,
				ShadowRunID:              "run-a",
				ExecutionDispatched:      true,
			},
		},
		Continuation: TaskShadowObserverContinuationSummary{
			Version:                 taskShadowContinuationVersion,
			Trigger:                 "activity_group_full",
			ScannedRunCount:         3,
			ExecutionDispatchedIDs:  []string{"run-a"},
			EvaluationDispatchedIDs: []string{},
		},
		Diagnostics: []string{"continued existing Task Episodes without onset detection"},
		Errors:      []string{},
	}
}

func TestTaskShadowObserverEmptyMaterializationRecoveryPreservesPayloadShape(
	t *testing.T,
) {
	payload, err := normalizeTaskShadowObserverEventPayload(
		taskShadowObserverTerminalPayload(),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(
		string(encoded),
		"materializationRecoveredRunIds",
	) {
		t.Fatalf("empty continuation extension changed historical payload shape: %s", encoded)
	}
}

func requireLatestTaskShadowObserverTrace(
	t *testing.T,
	store *ActivityStore,
	admission, state string,
) TaskShadowObserverTrace {
	t.Helper()
	traces, err := store.ListTaskShadowObserverTraces(10)
	if err != nil {
		t.Fatalf("list observer traces: %v", err)
	}
	if len(traces) == 0 {
		t.Fatal("no observer trace was recorded")
	}
	trace := traces[0]
	events, err := store.ListTaskShadowObserverTraceEvents(trace.ID)
	if err != nil {
		t.Fatalf("list observer trace events: %v", err)
	}
	valid, reason := verifyTaskShadowObserverTrace(trace, events)
	if !valid {
		t.Fatalf("observer trace integrity failed: %s", reason)
	}
	if trace.Admission != admission {
		t.Fatalf("observer admission=%q, want %q", trace.Admission, admission)
	}
	if got := taskShadowObserverDerivedStatus(
		trace,
		taskShadowObserverRuntimeInstanceID,
		true,
	); got != state {
		t.Fatalf("observer state=%q, want %q: %+v", got, state, trace)
	}
	return trace
}

func TestTaskShadowObserverTraceMigrationIsComplete(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	for _, table := range []string{
		"task_shadow_observer_traces_v1",
		"task_shadow_observer_trace_events_v1",
	} {
		ok, err := store.hasTable(table)
		if err != nil || !ok {
			t.Fatalf("table %s complete=%t err=%v", table, ok, err)
		}
	}
	for _, trigger := range []string{
		"task_shadow_observer_trace_events_v1_validate_insert",
		"task_shadow_observer_traces_v1_no_update",
		"task_shadow_observer_traces_v1_no_delete",
		"task_shadow_observer_trace_events_v1_no_update",
		"task_shadow_observer_trace_events_v1_no_delete",
	} {
		ok, err := store.hasTrigger(trigger)
		if err != nil || !ok {
			t.Fatalf("trigger %s complete=%t err=%v", trigger, ok, err)
		}
	}
}

func TestTaskShadowObserverTraceMigrationBootstrapRepairsPartialSchema(t *testing.T) {
	db := newTestDB(t)
	store := &ActivityStore{
		db:                db,
		appliedMigrations: make(map[string]bool),
	}
	for _, migration := range allActivityMigrations {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migration.Fn(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migration %q: %v", migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit migration %q: %v", migration.Name, err)
		}
	}
	if _, err := db.Exec(`
		DROP TRIGGER task_shadow_observer_trace_events_v1_validate_insert
	`); err != nil {
		t.Fatalf("drop observer validation trigger: %v", err)
	}
	if _, err := db.Exec(`
		DROP INDEX idx_task_shadow_observer_traces_v1_exact_input
	`); err != nil {
		t.Fatalf("drop observer exact-input index: %v", err)
	}
	if err := store.ensureMigrationLog(); err != nil {
		t.Fatal(err)
	}
	if err := store.bootstrapMigrationLog(); err != nil {
		t.Fatal(err)
	}
	var recorded int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM migration_log
		WHERE name = 'task_shadow_observer_traces'
	`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatal("partial observer schema was incorrectly marked migrated")
	}
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM migration_log
		WHERE name = 'task_shadow_observer_exact_input'
	`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatal("partial observer exact-input schema was incorrectly marked migrated")
	}

	store.runPendingMigrations()
	hasTrigger, err := store.hasTrigger(
		"task_shadow_observer_trace_events_v1_validate_insert",
	)
	if err != nil || !hasTrigger ||
		!store.appliedMigrations["task_shadow_observer_traces"] {
		t.Fatalf(
			"observer migration repair trigger=%t applied=%t err=%v",
			hasTrigger,
			store.appliedMigrations["task_shadow_observer_traces"],
			err,
		)
	}
	hasIdentity, identityErr := store.hasColumn(
		"task_shadow_observer_traces_v1",
		"input_identity_hash",
	)
	hasIdentityIndex, indexErr := store.hasIndex(
		"idx_task_shadow_observer_traces_v1_exact_input",
	)
	if err := firstErr(identityErr, indexErr); err != nil ||
		!hasIdentity || !hasIdentityIndex ||
		!store.appliedMigrations["task_shadow_observer_exact_input"] {
		t.Fatalf(
			"observer exact-input migration repair column=%t index=%t applied=%t err=%v",
			hasIdentity,
			hasIdentityIndex,
			store.appliedMigrations["task_shadow_observer_exact_input"],
			err,
		)
	}
}

func TestTaskShadowObserverTraceLifecycleAndDebugLinks(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	trace, err := store.RecordTaskShadowObserverTrace(taskShadowObserverTestInput(
		t,
		true,
		TaskShadowObserverFlowFullOnset,
		TaskShadowObserverAdmissionEligible,
	))
	if err != nil {
		t.Fatalf("record observer trace: %v", err)
	}
	if trace.LatestEvent.Status != TaskShadowObserverEventStarted {
		t.Fatalf("initial status=%q", trace.LatestEvent.Status)
	}
	events, err := store.ListTaskShadowObserverTraceEvents(trace.ID)
	if err != nil {
		t.Fatalf("list initial observer events: %v", err)
	}
	if valid, reason := verifyTaskShadowObserverTrace(trace, events); !valid {
		t.Fatalf("initial trace integrity failed: %s", reason)
	}
	if got := taskShadowObserverDerivedStatus(
		trace,
		taskShadowObserverRuntimeInstanceID,
		true,
	); got != TaskShadowObserverDerivedRunning {
		t.Fatalf("derived initial state=%q", got)
	}

	payload := taskShadowObserverTerminalPayload()
	terminal, added, err := store.CompleteTaskShadowObserverTrace(
		trace.ID,
		TaskShadowObserverEventCompleted,
		payload,
		5_000,
	)
	if err != nil || !added {
		t.Fatalf("complete observer trace added=%t err=%v", added, err)
	}
	if terminal.Sequence != 2 || terminal.Status != TaskShadowObserverEventCompleted {
		t.Fatalf("terminal event=%+v", terminal)
	}

	app := &App{activity: &activityMonitorState{store: store}}
	snapshot, err := app.GetTaskShadowObserverDebugSnapshot(10)
	if err != nil {
		t.Fatalf("get observer snapshot: %v", err)
	}
	if snapshot.Version != taskShadowObserverDebugVersion ||
		snapshot.Counts.Total != 1 || snapshot.Counts.Completed != 1 ||
		len(snapshot.Traces) != 1 {
		t.Fatalf("unexpected observer snapshot: %+v", snapshot)
	}
	detail, err := app.GetTaskShadowObserverDebugDetail(trace.ID)
	if err != nil {
		t.Fatalf("get observer detail: %v", err)
	}
	if !detail.Summary.IntegrityValid || len(detail.Events) != 2 ||
		len(detail.AttemptLinks) != 3 {
		t.Fatalf("unexpected observer detail: %+v", detail)
	}
	for _, link := range detail.AttemptLinks {
		if link.Found || link.MissingReason == "" {
			t.Fatalf("missing attempt link was not explicit: %+v", link)
		}
	}
}

func TestTaskShadowObserverDisabledAndInterruptedStates(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	disabled, err := store.RecordTaskShadowObserverTrace(taskShadowObserverTestInput(
		t,
		false,
		TaskShadowObserverFlowContinuation,
		TaskShadowObserverAdmissionPrefiltered,
	))
	if err != nil {
		t.Fatalf("record disabled observer trace: %v", err)
	}
	if disabled.LatestEvent.Status != TaskShadowObserverEventSkipped ||
		disabled.LatestEvent.Sequence != 1 {
		t.Fatalf("disabled observer event=%+v", disabled.LatestEvent)
	}
	if _, _, err := store.CompleteTaskShadowObserverTrace(
		disabled.ID,
		TaskShadowObserverEventCompleted,
		taskShadowObserverTerminalPayload(),
		5_000,
	); err == nil {
		t.Fatal("terminal disabled trace accepted another terminal event")
	}

	input := taskShadowObserverTestInput(
		t,
		true,
		TaskShadowObserverFlowFullOnset,
		TaskShadowObserverAdmissionEligible,
	)
	input.ID = "prior-process-trace"
	input.RuntimeInstanceID = "prior-process"
	input.CreatedAt = 6_000
	interrupted, err := store.RecordTaskShadowObserverTrace(input)
	if err != nil {
		t.Fatalf("record prior-process trace: %v", err)
	}
	events, err := store.ListTaskShadowObserverTraceEvents(interrupted.ID)
	if err != nil {
		t.Fatalf("list prior-process events: %v", err)
	}
	valid, reason := verifyTaskShadowObserverTrace(interrupted, events)
	if !valid {
		t.Fatalf("prior-process trace integrity failed: %s", reason)
	}
	if got := taskShadowObserverDerivedStatus(
		interrupted,
		taskShadowObserverRuntimeInstanceID,
		true,
	); got != TaskShadowObserverDerivedInterrupted {
		t.Fatalf("derived prior-process state=%q", got)
	}
}

func TestTaskShadowObserverDispatchRecordsDisabledQueuedAndCancelledPaths(t *testing.T) {
	newApp := func(t *testing.T) (*ActivityStore, *App, *taskShadowRolloutFakeRunner) {
		t.Helper()
		activityStore := newTestStore(t)
		stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stateStore.Close() })
		runner := &taskShadowRolloutFakeRunner{}
		return activityStore, &App{
			store:    stateStore,
			runner:   runner,
			activity: &activityMonitorState{store: activityStore},
		}, runner
	}
	group := sessionGroup{Sessions: []ActivitySessionRow{{
		ID:         "observer-dispatch-session",
		StartTime:  1_000,
		EndTime:    2_000,
		EventCount: 1,
		CreatedAt:  2_000,
	}}}
	events := []activitymon.ActivityEvent{{
		Timestamp:  1_500,
		Kind:       activitymon.EventClick,
		ClickLabel: "Prepare",
	}}

	t.Run("disabled at admission", func(t *testing.T) {
		activityStore, app, runner := newApp(t)
		if err := app.SaveUIState(triageEnableShadowKey, "false"); err != nil {
			t.Fatal(err)
		}
		triager := NewActivityTriager(app, nil)
		triager.dispatchTaskShadowProductionFlow(
			context.Background(),
			group,
			events,
		)
		triager.wg.Wait()
		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventSkipped,
		)
		if trace.LatestEvent.Sequence != 1 ||
			trace.LatestEvent.Payload.ShadowEnabledAtExecution == nil ||
			*trace.LatestEvent.Payload.ShadowEnabledAtExecution {
			t.Fatalf("disabled admission receipt=%+v", trace.LatestEvent)
		}
		if len(runner.phases) != 0 {
			t.Fatalf("disabled observer invoked runner: %v", runner.phases)
		}
		episodes, err := activityStore.ListTaskEpisodes("", 10)
		if err != nil || len(episodes) != 0 {
			t.Fatalf("disabled observer episodes=%+v err=%v", episodes, err)
		}
	})

	t.Run("disabled after queue", func(t *testing.T) {
		activityStore, app, runner := newApp(t)
		triager := NewActivityTriager(app, nil)
		triager.taskShadowSem <- struct{}{}
		triager.dispatchTaskShadowProductionFlow(
			context.Background(),
			group,
			events,
		)
		if err := app.SaveUIState(triageEnableShadowKey, "false"); err != nil {
			t.Fatal(err)
		}
		<-triager.taskShadowSem
		triager.wg.Wait()
		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventSkipped,
		)
		if trace.LatestEvent.Sequence != 2 ||
			!strings.Contains(trace.LatestEvent.Payload.Reason, "after queueing") {
			t.Fatalf("queued-pause receipt=%+v", trace.LatestEvent)
		}
		if len(runner.phases) != 0 {
			t.Fatalf("queued-pause observer invoked runner: %v", runner.phases)
		}
	})

	t.Run("cancelled before serialized slot", func(t *testing.T) {
		activityStore, app, runner := newApp(t)
		triager := NewActivityTriager(app, nil)
		triager.taskShadowSem <- struct{}{}
		ctx, cancel := context.WithCancel(context.Background())
		triager.dispatchTaskShadowProductionFlow(ctx, group, events)
		cancel()
		triager.wg.Wait()
		<-triager.taskShadowSem
		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventCancelled,
		)
		if trace.LatestEvent.Sequence != 2 {
			t.Fatalf("cancelled receipt=%+v", trace.LatestEvent)
		}
		if len(runner.phases) != 0 {
			t.Fatalf("cancelled observer invoked runner: %v", runner.phases)
		}
	})
}

func TestTaskShadowObserverDispatchExplainsContainedProductionOutcomes(t *testing.T) {
	newApp := func(t *testing.T, runner agent.AgentRunner) (*ActivityStore, *App) {
		t.Helper()
		activityStore := newTestStore(t)
		stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stateStore.Close() })
		app := &App{
			store:    stateStore,
			runner:   runner,
			activity: &activityMonitorState{store: activityStore},
		}
		if err := app.SaveUIState(triageEnableShadowKey, "true"); err != nil {
			t.Fatal(err)
		}
		return activityStore, app
	}
	group := sessionGroup{Sessions: []ActivitySessionRow{{
		ID:         "observer-contained-session",
		StartTime:  1_000,
		EndTime:    3_000,
		EventCount: 1,
		CreatedAt:  3_000,
	}}}
	events := []activitymon.ActivityEvent{{
		Timestamp:   1_500,
		Kind:        activitymon.EventAppSwitch,
		AppName:     "Safari",
		WindowTitle: "Draft",
	}}

	t.Run("guided First Run is excluded before model work", func(t *testing.T) {
		runner := &taskShadowRolloutFakeRunner{}
		activityStore, app := newApp(t, runner)
		firstRun := seedFirstRunContamination(t, activityStore)
		excludeFirstRunSource(
			t,
			activityStore,
			firstRun,
			"activity_session",
			group.Sessions[0].ID,
		)
		triager := NewActivityTriager(app, nil)
		triager.dispatchTaskShadowProductionFlow(context.Background(), group, events)
		triager.wg.Wait()

		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventCompleted,
		)
		payload := trace.LatestEvent.Payload
		if payload.FirstRunDecision != TaskShadowObserverFirstRunExcluded ||
			payload.FirstRunReason == "" || payload.OnsetWindowCount != 0 ||
			len(payload.ProductionOnsetAttemptIDs) != 0 ||
			len(payload.TaskEpisodeIDs) != 0 || len(payload.ShadowRunIDs) != 0 {
			t.Fatalf("guided First Run receipt=%+v", payload)
		}
		if len(runner.phases) != 0 {
			t.Fatalf("guided First Run invoked runner: %v", runner.phases)
		}
	})

	t.Run("empty retained evidence completes without model work", func(t *testing.T) {
		runner := &workPatternAblationFakeRunner{}
		activityStore, app := newApp(t, runner)
		emptyGroup := group
		emptyGroup.Sessions = append([]ActivitySessionRow(nil), group.Sessions...)
		emptyGroup.Sessions[0].ID = "observer-empty-session"
		emptyGroup.Sessions[0].EventCount = 0
		triager := NewActivityTriager(app, nil)
		triager.dispatchTaskShadowProductionFlow(
			context.Background(),
			emptyGroup,
			nil,
		)
		triager.wg.Wait()

		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventCompleted,
		)
		payload := trace.LatestEvent.Payload
		if payload.OnsetWindowCount != 0 ||
			payload.RetainedOnsetEvidenceCount != 0 ||
			len(payload.ProductionOnsetAttemptIDs) != 0 ||
			!strings.Contains(payload.Reason, "No onset window") {
			t.Fatalf("empty-evidence receipt=%+v", payload)
		}
		if len(runner.inputs) != 0 {
			t.Fatalf("empty evidence invoked runner %d times", len(runner.inputs))
		}
	})

	t.Run("unavailable runner retains source counts and error category", func(t *testing.T) {
		activityStore, app := newApp(t, nil)
		triager := NewActivityTriager(app, nil)
		triager.dispatchTaskShadowProductionFlow(context.Background(), group, events)
		triager.wg.Wait()

		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventCompletedWithErrors,
		)
		payload := trace.LatestEvent.Payload
		if payload.OnsetWindowCount == 0 ||
			payload.RetainedOnsetEvidenceCount == 0 ||
			len(payload.Errors) != 1 || payload.Errors[0] != "agent runner unavailable" ||
			len(payload.ProductionOnsetAttemptIDs) != 0 {
			t.Fatalf("runner-unavailable receipt=%+v", payload)
		}
	})

	t.Run("no prediction links the production attempt without inventing work", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		runner := &workPatternAblationFakeRunner{}
		activityStore, app := newApp(t, runner)
		triager := NewActivityTriager(app, nil)
		triager.dispatchTaskShadowProductionFlow(context.Background(), group, events)
		triager.wg.Wait()

		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventCompleted,
		)
		payload := trace.LatestEvent.Payload
		if payload.OnsetWindowCount != 1 ||
			len(payload.ProductionOnsetAttemptIDs) != 1 ||
			len(payload.DiagnosticOnsetAttemptIDs) != 0 ||
			len(payload.TaskEpisodeIDs) != 0 || len(payload.ShadowRunIDs) != 0 ||
			!strings.Contains(payload.Reason, "without a new task-mirror run") {
			t.Fatalf("no-prediction receipt=%+v", payload)
		}
		if len(runner.inputs) != 1 {
			t.Fatalf("no-prediction runner calls=%d, want 1", len(runner.inputs))
		}
	})

	t.Run("onset failure links its attempt and redacts provider detail", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		runner := &taskShadowObserverOnsetErrorRunner{}
		activityStore, app := newApp(t, runner)
		triager := NewActivityTriager(app, nil)
		triager.dispatchTaskShadowProductionFlow(context.Background(), group, events)
		triager.wg.Wait()

		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventCompletedWithErrors,
		)
		payload := trace.LatestEvent.Payload
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload.ProductionOnsetAttemptIDs) != 1 ||
			len(payload.TaskEpisodeIDs) != 0 || len(payload.ShadowRunIDs) != 0 ||
			len(payload.Errors) != 1 ||
			!strings.Contains(payload.Errors[0], "detail sha256:") ||
			strings.Contains(string(payloadJSON), taskShadowObserverPrivateTestValue) {
			t.Fatalf("onset-failure receipt=%s", payloadJSON)
		}
	})

	t.Run("panic is contained as a content-safe failed trace", func(t *testing.T) {
		runner := &taskShadowObserverPanicRunner{}
		activityStore, app := newApp(t, runner)
		triager := NewActivityTriager(app, nil)
		triager.dispatchTaskShadowProductionFlow(context.Background(), group, events)
		triager.wg.Wait()

		trace := requireLatestTaskShadowObserverTrace(
			t,
			activityStore,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventFailed,
		)
		payloadJSON, err := json.Marshal(trace.LatestEvent.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payloadJSON), taskShadowObserverPrivateTestValue) ||
			len(trace.LatestEvent.Payload.Errors) != 1 ||
			!strings.Contains(
				trace.LatestEvent.Payload.Errors[0],
				"detail sha256:",
			) {
			t.Fatalf("panic receipt=%s", payloadJSON)
		}
	})
}

func TestTaskShadowObserverReconciliationDistinguishesWorkflowOutcomes(t *testing.T) {
	const horizonEnd = int64(3_000)

	t.Run("missing frozen source context", func(t *testing.T) {
		store := newTestStore(t)
		input := taskEpisodeFixture(
			"observer-missing-context",
			1_000,
			1_100,
			TaskEpisodeStatusCandidate,
		)
		input.InitialHypothesis.TaskHypothesis = "Review the supplied proposal"
		input.InitialHypothesis.DesiredResultHypothesis =
			"Findings that identify the material gaps and risks"
		input.FrozenContext.Manifest = nil
		episode, added, err := store.CreateTaskEpisode(input)
		if err != nil || !added {
			t.Fatalf("create context-poor episode: added=%t err=%v", added, err)
		}
		triager := &ActivityTriager{app: &App{
			activity: &activityMonitorState{store: store},
		}}
		result := triager.reconcileRecentEligibleTaskEpisodesDetailed(
			context.Background(),
			horizonEnd,
		)
		if len(result.Decisions) != 1 ||
			result.Decisions[0].TaskEpisodeID != episode.ID ||
			result.Decisions[0].State !=
				TaskShadowWorkflowStateFrozenContextNotReady ||
			result.Decisions[0].ExecutionDispatched || len(result.RunIDs) != 0 ||
			len(result.Errors) != 0 {
			t.Fatalf("missing-context result=%+v", result)
		}
	})

	t.Run("existing run", func(t *testing.T) {
		store := newTestStore(t)
		episode := createAnalyzeShadowTaskEpisodeFixture(t, store)
		run, added, err := store.CreateShadowTaskRunFromEpisode(
			episode,
			"gpt-5.6-sol",
			1_200,
		)
		if err != nil || !added {
			t.Fatalf("create existing run: added=%t err=%v", added, err)
		}
		triager := &ActivityTriager{app: &App{
			activity: &activityMonitorState{store: store},
		}}
		result := triager.reconcileRecentEligibleTaskEpisodesDetailed(
			context.Background(),
			horizonEnd,
		)
		if len(result.Decisions) != 1 ||
			result.Decisions[0].State != TaskShadowWorkflowStateExistingRun ||
			result.Decisions[0].ShadowRunID != run.ID ||
			result.Decisions[0].ExecutionDispatched || len(result.RunIDs) != 0 ||
			len(result.Errors) != 0 {
			t.Fatalf("existing-run result=%+v", result)
		}
	})

	t.Run("run creation failure", func(t *testing.T) {
		store := newTestStore(t)
		input := taskEpisodeFixture(
			"observer-run-create-failure",
			1_000,
			1_100,
			TaskEpisodeStatusCandidate,
		)
		input.InitialHypothesis.TaskHypothesis = "Review the supplied proposal"
		input.InitialHypothesis.DesiredResultHypothesis =
			"Findings that identify the material gaps and risks"
		input.FrozenContext.Manifest = []TaskContextManifestEntry{{
			SourceKind: taskContextSourceActivitySnapshotKind,
			SourceID:   "activity-session:missing-profile",
			ObservedAt: 999,
			Included:   true,
		}}
		input.FrozenContext.WorkProfileSnapshotID = "missing-profile"
		input.FrozenContext.WorkProfileSHA256 = strings.Repeat("a", 64)
		episode, added, err := store.CreateTaskEpisode(input)
		if err != nil || !added {
			t.Fatalf("create missing-profile episode: added=%t err=%v", added, err)
		}
		triager := &ActivityTriager{app: &App{
			activity: &activityMonitorState{store: store},
		}}
		result := triager.reconcileRecentEligibleTaskEpisodesDetailed(
			context.Background(),
			horizonEnd,
		)
		if len(result.Decisions) != 1 ||
			result.Decisions[0].TaskEpisodeID != episode.ID ||
			result.Decisions[0].State != TaskShadowWorkflowStateRunCreateFailed ||
			result.Decisions[0].Reason == "" ||
			result.Decisions[0].ExecutionDispatched || len(result.RunIDs) != 0 ||
			len(result.Errors) != 1 {
			t.Fatalf("run-create-failure result=%+v", result)
		}
	})
}

func TestTaskShadowObserverTraceIsAppendOnlyAndHashChained(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	oversized := taskShadowObserverTestInput(
		t,
		true,
		TaskShadowObserverFlowFullOnset,
		TaskShadowObserverAdmissionEligible,
	)
	oversized.ID = strings.Repeat("x", taskShadowObserverMaxIDBytes+1)
	if _, err := store.RecordTaskShadowObserverTrace(oversized); err == nil {
		t.Fatal("observer trace accepted an oversized identifier")
	}
	trace, err := store.RecordTaskShadowObserverTrace(taskShadowObserverTestInput(
		t,
		true,
		TaskShadowObserverFlowFullOnset,
		TaskShadowObserverAdmissionEligible,
	))
	if err != nil {
		t.Fatalf("record observer trace: %v", err)
	}
	if _, err := store.db.Exec(`
		UPDATE task_shadow_observer_traces_v1
		SET admission_reason = 'changed'
		WHERE id = ?`, trace.ID); err == nil {
		t.Fatal("observer trace root allowed mutation")
	}
	if _, err := store.db.Exec(`
		DELETE FROM task_shadow_observer_trace_events_v1
		WHERE trace_id = ?`, trace.ID); err == nil {
		t.Fatal("observer trace event allowed deletion")
	}

	outOfSequence, outOfSequenceJSON, err := taskShadowObserverEvent(
		trace.ID,
		3,
		TaskShadowObserverEventCompleted,
		taskShadowObserverTerminalPayload(),
		trace.LatestEvent.PayloadHash,
		4_900,
	)
	if err != nil {
		t.Fatalf("build out-of-sequence observer event: %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO task_shadow_observer_trace_events_v1 (
			id, trace_id, sequence, status, payload_json, payload_hash,
			previous_hash, occurred_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outOfSequence.ID,
		outOfSequence.TraceID,
		outOfSequence.Sequence,
		outOfSequence.Status,
		outOfSequenceJSON,
		outOfSequence.PayloadHash,
		outOfSequence.PreviousHash,
		outOfSequence.OccurredAt,
		outOfSequence.CreatedAt,
	); err == nil {
		t.Fatal("observer trace accepted an out-of-sequence event")
	}
	if _, err := store.db.Exec(`
		INSERT INTO task_shadow_observer_trace_events_v1 (
			id, trace_id, sequence, status, payload_json, payload_hash,
			previous_hash, occurred_at, created_at
		) VALUES (?, ?, 2, 'completed', ?, ?, ?, 4900, 4900)`,
		"malformed-observer-event",
		trace.ID,
		`{"not-json"`,
		strings.Repeat("b", 64),
		trace.LatestEvent.PayloadHash,
	); err == nil {
		t.Fatal("observer trace accepted malformed event JSON")
	}

	badEvent, payloadJSON, err := taskShadowObserverEvent(
		trace.ID,
		2,
		TaskShadowObserverEventCompleted,
		taskShadowObserverTerminalPayload(),
		strings.Repeat("0", 64),
		5_000,
	)
	if err != nil {
		t.Fatalf("build bad observer event: %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO task_shadow_observer_trace_events_v1 (
			id, trace_id, sequence, status, payload_json, payload_hash,
			previous_hash, occurred_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		badEvent.ID,
		badEvent.TraceID,
		badEvent.Sequence,
		badEvent.Status,
		payloadJSON,
		badEvent.PayloadHash,
		badEvent.PreviousHash,
		badEvent.OccurredAt,
		badEvent.CreatedAt,
	); err == nil {
		t.Fatal("observer trace accepted a broken hash-chain link")
	}
	terminal, added, err := store.CompleteTaskShadowObserverTrace(
		trace.ID,
		TaskShadowObserverEventCompleted,
		taskShadowObserverTerminalPayload(),
		5_000,
	)
	if err != nil || !added {
		t.Fatalf("complete valid observer trace: %v", err)
	}
	retry, retryAdded, err := store.CompleteTaskShadowObserverTrace(
		trace.ID,
		TaskShadowObserverEventCompleted,
		taskShadowObserverTerminalPayload(),
		6_000,
	)
	if err != nil || retryAdded || retry.ID != terminal.ID ||
		retry.OccurredAt != terminal.OccurredAt {
		t.Fatalf(
			"idempotent terminal retry event=%+v added=%t err=%v",
			retry,
			retryAdded,
			err,
		)
	}
	if _, _, err := store.CompleteTaskShadowObserverTrace(
		trace.ID,
		TaskShadowObserverEventFailed,
		TaskShadowObserverTraceEventPayload{Reason: "different terminal"},
		6_000,
	); err == nil {
		t.Fatal("observer trace accepted a second terminal event")
	}
}

func TestTaskShadowObserverTraceExcludesRawContentAndDetectsTampering(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	trace, err := store.RecordTaskShadowObserverTrace(taskShadowObserverTestInput(
		t,
		true,
		TaskShadowObserverFlowFullOnset,
		TaskShadowObserverAdmissionEligible,
	))
	if err != nil {
		t.Fatalf("record observer trace: %v", err)
	}
	payload := taskShadowObserverTerminalPayload()
	payload.FirstRunDecision = TaskShadowObserverFirstRunError
	payload.FirstRunReason = "first-run: " + taskShadowObserverPrivateTestValue
	payload.Errors = []string{"onset: " + taskShadowObserverPrivateTestValue}
	payload.Diagnostics = []string{
		"profile diagnostic: " + taskShadowObserverPrivateTestValue,
		taskShadowObserverPrivateTestValue + " [detail sha256:" +
			strings.Repeat("a", 64) + "]",
	}
	payload.WorkflowDecisions[0].State = TaskShadowWorkflowStateReadinessError
	payload.WorkflowDecisions[0].Reason = "readiness: " + taskShadowObserverPrivateTestValue
	if _, _, err := store.CompleteTaskShadowObserverTrace(
		trace.ID,
		TaskShadowObserverEventCompletedWithErrors,
		payload,
		5_000,
	); err != nil {
		t.Fatalf("complete private observer trace: %v", err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	detail, err := app.GetTaskShadowObserverDebugDetail(trace.ID)
	if err != nil {
		t.Fatalf("get private observer detail: %v", err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("encode observer detail: %v", err)
	}
	if strings.Contains(string(encoded), taskShadowObserverPrivateTestValue) {
		t.Fatalf("observer detail leaked raw private content: %s", encoded)
	}

	if _, err := store.db.Exec(`
		DROP TRIGGER task_shadow_observer_trace_events_v1_no_update
	`); err != nil {
		t.Fatalf("drop immutable trigger for corruption fixture: %v", err)
	}
	if _, err := store.db.Exec(`
		UPDATE task_shadow_observer_trace_events_v1
		SET status = 'failed'
		WHERE trace_id = ? AND sequence = 2`, trace.ID); err != nil {
		t.Fatalf("tamper observer fixture: %v", err)
	}
	snapshot, err := app.GetTaskShadowObserverDebugSnapshot(10)
	if err != nil {
		t.Fatalf("get tampered observer snapshot: %v", err)
	}
	if len(snapshot.Traces) != 1 || snapshot.Traces[0].State != TaskShadowObserverDerivedCorrupt ||
		snapshot.Traces[0].IntegrityValid || snapshot.Counts.Corrupt != 1 {
		t.Fatalf("tampered trace was not contained: %+v", snapshot)
	}
}

func TestTaskShadowObserverDebugReadsAreBoundedReadOnlyAndBrowserAllowlisted(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	for index := 0; index < 3; index++ {
		input := taskShadowObserverTestInput(
			t,
			false,
			TaskShadowObserverFlowContinuation,
			TaskShadowObserverAdmissionCooldown,
		)
		input.ID = "bounded-observer-" + string(rune('a'+index))
		input.CreatedAt = int64(5_000 + index)
		if _, err := store.RecordTaskShadowObserverTrace(input); err != nil {
			t.Fatalf("record bounded observer trace %d: %v", index, err)
		}
	}
	var rootsBefore, eventsBefore int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM task_shadow_observer_traces_v1`).Scan(&rootsBefore); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM task_shadow_observer_trace_events_v1`).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	snapshot, err := app.GetTaskShadowObserverDebugSnapshot(2)
	if err != nil {
		t.Fatalf("get bounded observer snapshot: %v", err)
	}
	if snapshot.Limit != 2 || len(snapshot.Traces) != 2 {
		t.Fatalf("observer snapshot was not bounded: %+v", snapshot)
	}
	for _, summary := range snapshot.Traces {
		if _, err := app.GetTaskShadowObserverDebugDetail(summary.ID); err != nil {
			t.Fatalf("open observer detail %s: %v", summary.ID, err)
		}
	}
	var rootsAfter, eventsAfter int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM task_shadow_observer_traces_v1`).Scan(&rootsAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM task_shadow_observer_trace_events_v1`).Scan(&eventsAfter); err != nil {
		t.Fatal(err)
	}
	if rootsBefore != rootsAfter || eventsBefore != eventsAfter {
		t.Fatalf("debug reads mutated observer storage: roots %d/%d events %d/%d", rootsBefore, rootsAfter, eventsBefore, eventsAfter)
	}
	for _, method := range []string{
		"GetTaskShadowObserverDebugSnapshot",
		"GetTaskShadowObserverDebugDetail",
	} {
		if !browserRPCAllowlist[method] {
			t.Fatalf("%s is not browser RPC allowlisted", method)
		}
	}
	for _, method := range []string{
		"RecordTaskShadowObserverTrace",
		"CompleteTaskShadowObserverTrace",
		"RecoverTaskShadowObserverTrace",
		"ReplayTaskShadowObserverTrace",
	} {
		if browserRPCAllowlist[method] {
			t.Fatalf("observer mutation %s must not be browser RPC allowlisted", method)
		}
	}
}
