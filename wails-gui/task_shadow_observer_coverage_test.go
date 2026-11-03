package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func taskShadowObserverCoverageTestSource(
	id int64,
	groupID, admission, dispatchStatus, traceID string,
	now int64,
) taskShadowObserverCoverageSource {
	return taskShadowObserverCoverageSource{
		ID:             id,
		StartedAt:      now - 100,
		CompletedAt:    now,
		GroupID:        groupID,
		SessionIDsJSON: `["observer-session-a","observer-session-b"]`,
		Expected:       true,
		Admission:      admission,
		DispatchStatus: dispatchStatus,
		DispatchReason: taskShadowObserverPrivateTestValue,
		TraceID:        traceID,
	}
}

func taskShadowObserverCoverageTestTrace(
	t *testing.T,
	store *ActivityStore,
	id string,
	sourcePipelineRunID int64,
	groupID, admission string,
	createdAt int64,
) (TaskShadowObserverTrace, []TaskShadowObserverTraceEvent) {
	t.Helper()
	flow := taskShadowObserverExpectedFlow(admission)
	input := taskShadowObserverTestInput(t, true, flow, admission)
	input.ID = id
	input.GroupID = groupID
	input.SourcePipelineRunID = sourcePipelineRunID
	input.CreatedAt = createdAt
	input.InputIdentityHash = ""
	trace, err := store.RecordTaskShadowObserverTrace(input)
	if err != nil {
		t.Fatalf("record coverage trace %s: %v", id, err)
	}
	events, err := store.ListTaskShadowObserverTraceEvents(trace.ID)
	if err != nil {
		t.Fatalf("list coverage trace events %s: %v", id, err)
	}
	return trace, events
}

func taskShadowObserverCoverageItemByPipelineRun(
	t *testing.T,
	snapshot TaskShadowObserverCoverageSnapshot,
	pipelineRunID int64,
) TaskShadowObserverCoverageItem {
	t.Helper()
	for _, item := range snapshot.Items {
		if item.PipelineRunID == pipelineRunID && item.Kind == "source" {
			return item
		}
	}
	t.Fatalf("coverage item for pipeline run %d not found: %+v", pipelineRunID, snapshot.Items)
	return TaskShadowObserverCoverageItem{}
}

func TestTaskShadowObserverCoverageMigrationIsCompleteAndRepairable(t *testing.T) {
	store := newTestStore(t)
	for _, column := range []struct {
		table string
		name  string
	}{
		{"activity_pipeline_runs", "shadow_observer_expected"},
		{"activity_pipeline_runs", "shadow_observer_admission"},
		{"activity_pipeline_runs", "shadow_observer_dispatch_status"},
		{"activity_pipeline_runs", "shadow_observer_dispatch_reason"},
		{"activity_pipeline_runs", "shadow_observer_trace_id"},
		{"task_shadow_observer_traces_v1", "source_pipeline_run_id"},
	} {
		ok, err := store.hasColumn(column.table, column.name)
		if err != nil || !ok {
			t.Fatalf("column %s.%s complete=%t err=%v", column.table, column.name, ok, err)
		}
	}
	for _, index := range []string{
		"idx_activity_pipeline_runs_shadow_observer_trace",
		"idx_activity_pipeline_runs_shadow_observer_expected",
		"idx_task_shadow_observer_traces_v1_source_pipeline",
	} {
		ok, err := store.hasIndex(index)
		if err != nil || !ok {
			t.Fatalf("index %s complete=%t err=%v", index, ok, err)
		}
	}
	if _, err := store.db.Exec(`
		DROP INDEX idx_task_shadow_observer_traces_v1_source_pipeline
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		DELETE FROM migration_log WHERE name = 'task_shadow_observer_coverage'
	`); err != nil {
		t.Fatal(err)
	}
	store.runPendingMigrations()
	ok, err := store.hasIndex(
		"idx_task_shadow_observer_traces_v1_source_pipeline",
	)
	if err != nil || !ok || !store.appliedMigrations["task_shadow_observer_coverage"] {
		t.Fatalf("coverage migration repair index=%t applied=%t err=%v", ok, store.appliedMigrations["task_shadow_observer_coverage"], err)
	}
}

func TestTaskShadowObserverV1AndV2RootHashCompatibility(t *testing.T) {
	input := taskShadowObserverTestInput(
		t,
		true,
		TaskShadowObserverFlowFullOnset,
		TaskShadowObserverAdmissionEligible,
	)
	trace := TaskShadowObserverTrace{
		ID:                        input.ID,
		ContractVersion:           TaskShadowObserverTraceContractVersionV1,
		RuntimeInstanceID:         input.RuntimeInstanceID,
		GroupID:                   input.GroupID,
		SessionIDs:                input.SessionIDs,
		SessionManifestHash:       input.SessionManifestHash,
		Source:                    input.Source,
		Flow:                      input.Flow,
		Admission:                 input.Admission,
		AdmissionReason:           input.AdmissionReason,
		WindowStart:               input.WindowStart,
		WindowEnd:                 input.WindowEnd,
		SessionCount:              input.SessionCount,
		DeclaredEventCount:        input.DeclaredEventCount,
		RetainedEventCount:        input.RetainedEventCount,
		RetainedEventManifestHash: input.RetainedEventManifestHash,
		ShadowEnabledAtAdmission:  input.ShadowEnabledAtAdmission,
		CreatedAt:                 input.CreatedAt,
	}
	rootHash, err := taskShadowObserverCanonicalHash(
		taskShadowObserverRootPayloadFromTrace(trace),
	)
	if err != nil {
		t.Fatal(err)
	}
	trace.RootHash = rootHash
	event, _, err := taskShadowObserverEvent(
		trace.ID,
		1,
		TaskShadowObserverEventStarted,
		TaskShadowObserverTraceEventPayload{
			Reason:           "Shadow enabled at observer admission",
			FirstRunDecision: TaskShadowObserverFirstRunUnknown,
		},
		rootHash,
		trace.CreatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	trace.LatestEvent = event
	if valid, reason := verifyTaskShadowObserverTrace(
		trace,
		[]TaskShadowObserverTraceEvent{event},
	); !valid {
		t.Fatalf("v1 trace no longer verifies: %s", reason)
	}

	store := newTestStore(t)
	input.ID = "coverage-v2-root"
	input.SourcePipelineRunID = 42
	v2, err := store.RecordTaskShadowObserverTrace(input)
	if err != nil {
		t.Fatal(err)
	}
	if v2.ContractVersion != TaskShadowObserverTraceContractVersion ||
		v2.SourcePipelineRunID != 42 {
		t.Fatalf("v2 source provenance=%+v", v2)
	}
	events, err := store.ListTaskShadowObserverTraceEvents(v2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if valid, reason := verifyTaskShadowObserverTrace(v2, events); !valid {
		t.Fatalf("v2 trace integrity failed: %s", reason)
	}
}

func TestTaskShadowObserverCoverageHealthyExactLinkAndReadOnlyAPI(t *testing.T) {
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	triager := NewActivityTriager(app, nil)
	now := time.Now().UnixMilli()
	group := sessionGroup{Sessions: []ActivitySessionRow{
		{ID: "observer-session-a", StartTime: 1_000, EndTime: 2_000, EventCount: 2, CreatedAt: 900},
		{ID: "observer-session-b", StartTime: 2_100, EndTime: 3_000, EventCount: 1, CreatedAt: 2_050},
	}}
	legacy := newGroupPipelineRun(group, time.UnixMilli(now-2_000))
	legacy.ShadowObserverExpected = false
	legacy.ShadowObserverDispatchStatus = ""
	legacy.ShadowObserverAdmission = ""
	if _, err := store.SaveActivityPipelineRun(legacy); err != nil {
		t.Fatal(err)
	}
	run := newGroupPipelineRun(group, time.UnixMilli(now-1_000))
	run.ShadowObserverAdmission = TaskShadowObserverAdmissionEligible
	triager.recordPipelineRun(&run)
	if run.ID <= 0 {
		t.Fatal("instrumented source row was not persisted")
	}
	trace, _ := taskShadowObserverCoverageTestTrace(
		t,
		store,
		"coverage-healthy-trace",
		run.ID,
		run.GroupID,
		TaskShadowObserverAdmissionEligible,
		now,
	)
	applyTaskShadowObserverDispatchReceipt(
		&run,
		TaskShadowObserverAdmissionEligible,
		TaskShadowObserverDispatchReceipt{
			Status:  TaskShadowObserverDispatchRecorded,
			Reason:  "observer trace recorded",
			TraceID: trace.ID,
		},
	)
	run.CompletedAt = now
	triager.recordPipelineRun(&run)
	if _, err := store.db.Exec(`
		UPDATE activity_pipeline_runs
		SET shadow_observer_dispatch_reason = ?
		WHERE id = ?`, taskShadowObserverPrivateTestValue, run.ID); err != nil {
		t.Fatal(err)
	}

	snapshot, err := app.GetTaskShadowObserverCoverageSnapshot(200)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != TaskShadowObserverCoverageVersion ||
		snapshot.Health != TaskShadowObserverCoverageHealthHealthy ||
		snapshot.Counts.InstrumentedSources != 1 ||
		snapshot.Counts.LegacySources != 1 ||
		snapshot.Counts.Covered != 1 {
		t.Fatalf("healthy coverage snapshot=%+v", snapshot)
	}
	item := taskShadowObserverCoverageItemByPipelineRun(t, snapshot, run.ID)
	if item.State != TaskShadowObserverCoverageCovered ||
		item.TraceID != trace.ID ||
		item.DispatchReason != "observer trace recorded" ||
		!item.IntegrityValid {
		t.Fatalf("healthy coverage item=%+v", item)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), taskShadowObserverPrivateTestValue) {
		t.Fatal("coverage API exposed untrusted source diagnostic text")
	}
	if !browserRPCAllowlist["GetTaskShadowObserverCoverageSnapshot"] {
		t.Fatal("coverage snapshot is not browser-readable")
	}
	for _, method := range []string{
		"RepairTaskShadowObserverCoverage",
		"ReplayTaskShadowObserverCoverage",
		"RetryTaskShadowObserverDispatch",
		"LinkTaskShadowObserverTrace",
	} {
		if browserRPCAllowlist[method] {
			t.Fatalf("coverage mutation %s must remain browser-forbidden", method)
		}
	}
}

func TestTaskShadowObserverCoverageCanonicalizesSourceSessionMembership(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixMilli()
	trace, events := taskShadowObserverCoverageTestTrace(
		t,
		store,
		"coverage-session-membership-trace",
		42,
		"coverage-session-membership-group",
		TaskShadowObserverAdmissionEligible,
		now,
	)
	source := taskShadowObserverCoverageTestSource(
		42,
		trace.GroupID,
		trace.Admission,
		TaskShadowObserverDispatchRecorded,
		trace.ID,
		now,
	)
	// Production source rows retain traversal order. The immutable observer root
	// stores the same unique membership in canonical order.
	source.SessionIDsJSON = `["observer-session-b","observer-session-a"]`
	snapshot := buildTaskShadowObserverCoverageSnapshot(
		[]taskShadowObserverCoverageSource{source},
		[]TaskShadowObserverTrace{trace},
		map[string][]TaskShadowObserverTraceEvent{trace.ID: events},
		0,
		1,
		200,
		now,
	)
	item := taskShadowObserverCoverageItemByPipelineRun(t, snapshot, source.ID)
	if snapshot.Version != TaskShadowObserverCoverageVersion ||
		snapshot.Health != TaskShadowObserverCoverageHealthHealthy ||
		snapshot.Counts.Covered != 1 ||
		item.State != TaskShadowObserverCoverageCovered {
		t.Fatalf("reordered exact session membership was not covered: snapshot=%+v item=%+v", snapshot, item)
	}

	source.SessionIDsJSON = `["observer-session-a","observer-session-a"]`
	duplicateSnapshot := buildTaskShadowObserverCoverageSnapshot(
		[]taskShadowObserverCoverageSource{source},
		[]TaskShadowObserverTrace{trace},
		map[string][]TaskShadowObserverTraceEvent{trace.ID: events},
		0,
		1,
		200,
		now,
	)
	duplicateItem := taskShadowObserverCoverageItemByPipelineRun(
		t,
		duplicateSnapshot,
		source.ID,
	)
	if duplicateSnapshot.Health != TaskShadowObserverCoverageHealthAttention ||
		duplicateSnapshot.Counts.LinkMismatch != 1 ||
		duplicateItem.State != TaskShadowObserverCoverageLinkMismatch {
		t.Fatalf("duplicate source session identity did not fail closed: snapshot=%+v item=%+v", duplicateSnapshot, duplicateItem)
	}
}

func TestTaskShadowObserverCoverageTracksDisabledAndFailedDispatches(t *testing.T) {
	newApp := func(t *testing.T) (*ActivityStore, *App) {
		t.Helper()
		activityStore := newTestStore(t)
		stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stateStore.Close() })
		app := &App{
			store:    stateStore,
			activity: &activityMonitorState{store: activityStore},
		}
		if err := app.SaveUIState(triageEnableShadowKey, "false"); err != nil {
			t.Fatal(err)
		}
		return activityStore, app
	}
	group := sessionGroup{Sessions: []ActivitySessionRow{{
		ID:         "coverage-dispatch-session",
		StartTime:  1_000,
		EndTime:    2_000,
		EventCount: 1,
		CreatedAt:  900,
	}}}
	events := []activitymon.ActivityEvent{{
		Timestamp:  1_500,
		Kind:       activitymon.EventClick,
		ClickLabel: taskShadowObserverPrivateTestValue,
	}}

	t.Run("disabled trace remains covered", func(t *testing.T) {
		store, app := newApp(t)
		triager := NewActivityTriager(app, nil)
		run := newGroupPipelineRun(group, time.Now())
		run.ShadowObserverAdmission = TaskShadowObserverAdmissionEligible
		triager.recordPipelineRun(&run)
		receipt := triager.dispatchTaskShadowProductionFlow(
			context.Background(),
			group,
			events,
			run.ID,
		)
		applyTaskShadowObserverDispatchReceipt(
			&run,
			TaskShadowObserverAdmissionEligible,
			receipt,
		)
		run.CompletedAt = time.Now().UnixMilli()
		triager.recordPipelineRun(&run)
		triager.wg.Wait()
		trace, err := store.GetTaskShadowObserverTrace(receipt.TraceID)
		if err != nil {
			t.Fatal(err)
		}
		if trace.SourcePipelineRunID != run.ID ||
			trace.LatestEvent.Status != TaskShadowObserverEventSkipped {
			t.Fatalf("disabled linked trace=%+v", trace)
		}
		snapshot, err := app.GetTaskShadowObserverCoverageSnapshot(200)
		if err != nil {
			t.Fatal(err)
		}
		if item := taskShadowObserverCoverageItemByPipelineRun(
			t,
			snapshot,
			run.ID,
		); item.State != TaskShadowObserverCoverageCovered {
			t.Fatalf("disabled coverage item=%+v", item)
		}
	})

	t.Run("observer persistence failure stays source-backed", func(t *testing.T) {
		store, app := newApp(t)
		triager := NewActivityTriager(app, nil)
		run := newGroupPipelineRun(group, time.Now())
		run.ShadowObserverAdmission = TaskShadowObserverAdmissionEligible
		triager.recordPipelineRun(&run)
		if _, err := store.db.Exec(`
			CREATE TRIGGER coverage_fail_observer_root
			BEFORE INSERT ON task_shadow_observer_traces_v1
			BEGIN
				SELECT RAISE(ABORT, 'private observer database failure');
			END
		`); err != nil {
			t.Fatal(err)
		}
		receipt := triager.dispatchTaskShadowProductionFlow(
			context.Background(),
			group,
			events,
			run.ID,
		)
		if receipt.Status != TaskShadowObserverDispatchRecordFailed ||
			receipt.TraceID != "" {
			t.Fatalf("failed dispatch receipt=%+v", receipt)
		}
		applyTaskShadowObserverDispatchReceipt(
			&run,
			TaskShadowObserverAdmissionEligible,
			receipt,
		)
		run.CompletedAt = time.Now().UnixMilli()
		triager.recordPipelineRun(&run)
		snapshot, err := app.GetTaskShadowObserverCoverageSnapshot(200)
		if err != nil {
			t.Fatal(err)
		}
		item := taskShadowObserverCoverageItemByPipelineRun(t, snapshot, run.ID)
		if item.State != TaskShadowObserverCoverageDispatchFailed ||
			strings.Contains(item.DispatchReason, "private") {
			t.Fatalf("failed dispatch coverage item=%+v", item)
		}
		var roots int
		if err := store.db.QueryRow(`
			SELECT COUNT(*) FROM task_shadow_observer_traces_v1
		`).Scan(&roots); err != nil || roots != 0 {
			t.Fatalf("failed observer roots=%d err=%v", roots, err)
		}
	})
}

func TestTaskShadowObserverCoverageClassifiesEveryGapWithoutRepair(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixMilli()
	sources := []taskShadowObserverCoverageSource{
		taskShadowObserverCoverageTestSource(1, "pending-group", TaskShadowObserverAdmissionEligible, TaskShadowObserverDispatchPending, "", now),
		taskShadowObserverCoverageTestSource(2, "failed-group", TaskShadowObserverAdmissionEligible, TaskShadowObserverDispatchRecordFailed, "", now),
		taskShadowObserverCoverageTestSource(3, "missing-group", TaskShadowObserverAdmissionEligible, TaskShadowObserverDispatchRecorded, "missing-trace", now),
		taskShadowObserverCoverageTestSource(4, "root-only-group", TaskShadowObserverAdmissionEligible, TaskShadowObserverDispatchPending, "", now),
		taskShadowObserverCoverageTestSource(5, "source-group", TaskShadowObserverAdmissionEligible, TaskShadowObserverDispatchRecorded, "mismatch-trace", now),
		taskShadowObserverCoverageTestSource(6, "corrupt-group", TaskShadowObserverAdmissionEligible, TaskShadowObserverDispatchRecorded, "corrupt-trace", now),
		taskShadowObserverCoverageTestSource(7, "duplicate-group", TaskShadowObserverAdmissionEligible, TaskShadowObserverDispatchRecorded, "duplicate-one", now),
	}
	rootOnly, rootOnlyEvents := taskShadowObserverCoverageTestTrace(t, store, "root-only-trace", 4, "root-only-group", TaskShadowObserverAdmissionEligible, now)
	mismatch, mismatchEvents := taskShadowObserverCoverageTestTrace(t, store, "mismatch-trace", 5, "trace-group", TaskShadowObserverAdmissionEligible, now+1)
	corrupt, corruptEvents := taskShadowObserverCoverageTestTrace(t, store, "corrupt-trace", 6, "corrupt-group", TaskShadowObserverAdmissionEligible, now+2)
	corrupt.RootHash = strings.Repeat("f", 64)
	duplicate, duplicateEvents := taskShadowObserverCoverageTestTrace(t, store, "duplicate-one", 7, "duplicate-group", TaskShadowObserverAdmissionEligible, now+3)
	duplicateTwo := duplicate
	duplicateTwo.ID = "duplicate-two"
	duplicateTwo.LatestEvent.TraceID = duplicateTwo.ID
	pruned, prunedEvents := taskShadowObserverCoverageTestTrace(t, store, "pruned-trace", 99, "pruned-group", TaskShadowObserverAdmissionEligible, now+4)
	unlinked, unlinkedEvents := taskShadowObserverCoverageTestTrace(t, store, "unlinked-trace", 0, "unlinked-group", TaskShadowObserverAdmissionEligible, now+5)
	traces := []TaskShadowObserverTrace{
		rootOnly,
		mismatch,
		corrupt,
		duplicate,
		duplicateTwo,
		pruned,
		unlinked,
	}
	events := map[string][]TaskShadowObserverTraceEvent{
		rootOnly.ID:  rootOnlyEvents,
		mismatch.ID:  mismatchEvents,
		corrupt.ID:   corruptEvents,
		duplicate.ID: duplicateEvents,
		pruned.ID:    prunedEvents,
		unlinked.ID:  unlinkedEvents,
	}
	snapshot := buildTaskShadowObserverCoverageSnapshot(
		sources,
		traces,
		events,
		0,
		len(traces),
		200,
		now+10,
	)
	if snapshot.Health != TaskShadowObserverCoverageHealthAttention ||
		snapshot.Counts.PendingDispatch != 1 ||
		snapshot.Counts.DispatchFailed != 1 ||
		snapshot.Counts.TraceMissing != 1 ||
		snapshot.Counts.CoveredRootOnly != 1 ||
		snapshot.Counts.LinkMismatch != 1 ||
		snapshot.Counts.TraceCorrupt != 1 ||
		snapshot.Counts.DuplicateTrace != 1 ||
		snapshot.Counts.SourcePruned != 1 ||
		snapshot.Counts.UnlinkedTrace != 1 {
		t.Fatalf("gap coverage snapshot=%+v", snapshot)
	}
	wants := map[int64]string{
		1: TaskShadowObserverCoveragePendingDispatch,
		2: TaskShadowObserverCoverageDispatchFailed,
		3: TaskShadowObserverCoverageTraceMissing,
		4: TaskShadowObserverCoverageCoveredRootOnly,
		5: TaskShadowObserverCoverageLinkMismatch,
		6: TaskShadowObserverCoverageTraceCorrupt,
		7: TaskShadowObserverCoverageDuplicateTrace,
	}
	for pipelineRunID, want := range wants {
		if item := taskShadowObserverCoverageItemByPipelineRun(t, snapshot, pipelineRunID); item.State != want {
			t.Fatalf("pipeline run %d state=%q want=%q item=%+v", pipelineRunID, item.State, want, item)
		}
	}
	var rootCount int
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM task_shadow_observer_traces_v1
	`).Scan(&rootCount); err != nil {
		t.Fatal(err)
	}
	if rootCount != 6 {
		t.Fatalf("coverage read changed observer roots: %d", rootCount)
	}
}

func TestTaskShadowObserverCoverageRejectsDuplicateSourceAndTraceLinks(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixMilli()
	first := ActivityPipelineRunRow{
		Kind:                         "group",
		StartedAt:                    now,
		CompletedAt:                  now,
		GroupID:                      "unique-source-a",
		SessionIDs:                   `["a"]`,
		ShadowObserverExpected:       true,
		ShadowObserverAdmission:      TaskShadowObserverAdmissionEligible,
		ShadowObserverDispatchStatus: TaskShadowObserverDispatchRecorded,
		ShadowObserverTraceID:        "unique-trace",
	}
	if _, err := store.SaveActivityPipelineRun(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.GroupID = "unique-source-b"
	if _, err := store.SaveActivityPipelineRun(second); err == nil {
		t.Fatal("duplicate source-row trace link was accepted")
	}
	input := taskShadowObserverTestInput(t, true, TaskShadowObserverFlowFullOnset, TaskShadowObserverAdmissionEligible)
	input.ID = "unique-root-a"
	input.SourcePipelineRunID = 77
	input.CreatedAt = now
	if _, err := store.RecordTaskShadowObserverTrace(input); err != nil {
		t.Fatal(err)
	}
	input.ID = "unique-root-b"
	if _, err := store.RecordTaskShadowObserverTrace(input); err == nil {
		t.Fatal("duplicate immutable source pipeline link was accepted")
	}
}

func TestTaskShadowObserverCoverageTreatsPrunedSourceAsRetentionInfo(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UnixMilli()
	trace, events := taskShadowObserverCoverageTestTrace(
		t,
		store,
		"retained-root",
		99,
		"pruned-source-group",
		TaskShadowObserverAdmissionEligible,
		now,
	)
	snapshot := buildTaskShadowObserverCoverageSnapshot(
		nil,
		[]TaskShadowObserverTrace{trace},
		map[string][]TaskShadowObserverTraceEvent{trace.ID: events},
		0,
		1,
		200,
		now+1,
	)
	if snapshot.Health != TaskShadowObserverCoverageHealthHealthy ||
		snapshot.Counts.SourcePruned != 1 ||
		len(snapshot.Items) != 1 ||
		snapshot.Items[0].State != TaskShadowObserverCoverageSourcePruned {
		t.Fatalf("pruned source coverage snapshot=%+v", snapshot)
	}
	if !strings.Contains(snapshot.HealthReason, "outlived bounded source diagnostics") {
		t.Fatalf("pruned source health reason=%q", snapshot.HealthReason)
	}
}

func TestTaskShadowObserverCoverageScanTruncationFailsClosed(t *testing.T) {
	now := time.Now().UnixMilli()
	snapshot := buildTaskShadowObserverCoverageSnapshot(
		nil,
		nil,
		nil,
		4,
		3,
		10,
		now,
	)
	if snapshot.Health != TaskShadowObserverCoverageHealthAttention ||
		snapshot.Counts.ScanTruncated != 1 ||
		snapshot.Counts.LegacyTraces != 4 {
		t.Fatalf("truncated coverage snapshot=%+v", snapshot)
	}
	if !strings.Contains(snapshot.Items[0].Reason, fmt.Sprint(3)) {
		t.Fatalf("truncated coverage reason=%q", snapshot.Items[0].Reason)
	}
}
