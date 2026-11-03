package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func persistShadowTaskLearningMaterializationTestRun(
	t *testing.T,
	store *ActivityStore,
	projection ShadowTaskLearningProjection,
	createdAt int64,
) ShadowTaskLearningRun {
	t.Helper()
	run, added, err := PersistShadowTaskLearningProjection(
		store,
		projection,
		createdAt,
	)
	if err != nil || !added {
		t.Fatalf("persist materialization test run: added=%v err=%v", added, err)
	}
	return run
}

func TestShadowTaskLearningMaterializationResolutionFailsClosed(t *testing.T) {
	t.Run("missing then error then terminal", func(t *testing.T) {
		store := newTestStore(t)
		projection := testShadowTaskLearningProjection()
		run := persistShadowTaskLearningMaterializationTestRun(
			t,
			store,
			projection,
			1_000,
		)
		resolution, err := BuildShadowTaskLearningMaterializationResolution(
			store,
			run.ID,
		)
		if err != nil || resolution.Complete ||
			resolution.CandidateCount != 1 || resolution.MissingCount != 1 ||
			!reflect.DeepEqual(
				resolution.PendingRevisionIDs,
				[]string{"revision:one"},
			) {
			t.Fatalf("missing resolution=%+v err=%v", resolution, err)
		}

		if _, added, err := store.AddShadowTaskLearningMaterialization(
			ShadowTaskLearningMaterialization{
				RevisionID: projection.Candidates[0].RevisionID,
				TargetKind: projection.Candidates[0].TargetKind,
				Status:     ShadowTaskLearningMaterializationError,
				Error:      "target unavailable",
				CreatedAt:  1_100,
			},
		); err != nil || !added {
			t.Fatalf("persist error receipt: added=%v err=%v", added, err)
		}
		resolution, err = BuildShadowTaskLearningMaterializationResolution(
			store,
			run.ID,
		)
		if err != nil || resolution.Complete || resolution.ErrorCount != 1 ||
			resolution.MissingCount != 0 {
			t.Fatalf("error resolution=%+v err=%v", resolution, err)
		}

		if _, added, err := store.AddShadowTaskLearningMaterialization(
			ShadowTaskLearningMaterialization{
				RevisionID: projection.Candidates[0].RevisionID,
				TargetKind: projection.Candidates[0].TargetKind,
				TargetID:   "memory-target",
				Status:     ShadowTaskLearningMaterializationMaterialized,
				CreatedAt:  1_200,
			},
		); err != nil || !added {
			t.Fatalf("persist terminal receipt: added=%v err=%v", added, err)
		}
		resolution, err = BuildShadowTaskLearningMaterializationResolution(
			store,
			run.ID,
		)
		if err != nil || !resolution.Complete ||
			resolution.TerminalCount != 1 ||
			resolution.MaterializedCount != 1 || resolution.ErrorCount != 0 ||
			len(resolution.PendingRevisionIDs) != 0 {
			t.Fatalf("terminal resolution=%+v err=%v", resolution, err)
		}
	})

	t.Run("mismatched terminal status remains pending", func(t *testing.T) {
		store := newTestStore(t)
		projection := testShadowTaskLearningProjection()
		run := persistShadowTaskLearningMaterializationTestRun(
			t,
			store,
			projection,
			1_000,
		)
		if _, _, err := store.AddShadowTaskLearningMaterialization(
			ShadowTaskLearningMaterialization{
				RevisionID: projection.Candidates[0].RevisionID,
				TargetKind: projection.Candidates[0].TargetKind,
				Status:     ShadowTaskLearningMaterializationSkipped,
				CreatedAt:  1_100,
			},
		); err != nil {
			t.Fatal(err)
		}
		resolution, err := BuildShadowTaskLearningMaterializationResolution(
			store,
			run.ID,
		)
		if err != nil || resolution.Complete || resolution.InvalidCount != 1 ||
			resolution.TerminalCount != 0 {
			t.Fatalf("mismatched resolution=%+v err=%v", resolution, err)
		}
		app := &App{activity: &activityMonitorState{store: store}}
		if recovery, reconcileErr := app.reconcileShadowTaskLearningMaterializations(
			run.ID,
			1_200,
		); reconcileErr == nil || recovery.Resolution.InvalidCount != 1 {
			t.Fatalf("conflicting receipt recovery=%+v err=%v", recovery, reconcileErr)
		}
	})

	t.Run("mismatched target kind remains pending", func(t *testing.T) {
		store := newTestStore(t)
		projection := testShadowTaskLearningProjection()
		run := persistShadowTaskLearningMaterializationTestRun(
			t,
			store,
			projection,
			1_000,
		)
		if _, _, err := store.AddShadowTaskLearningMaterialization(
			ShadowTaskLearningMaterialization{
				RevisionID: projection.Candidates[0].RevisionID,
				TargetKind: ShadowTaskLearningTargetKnowledge,
				TargetID:   "knowledge-target",
				Status:     ShadowTaskLearningMaterializationMaterialized,
				CreatedAt:  1_100,
			},
		); err != nil {
			t.Fatal(err)
		}
		resolution, err := BuildShadowTaskLearningMaterializationResolution(
			store,
			run.ID,
		)
		if err != nil || resolution.Complete || resolution.InvalidCount != 1 ||
			resolution.MissingCount != 0 {
			t.Fatalf("target-kind resolution=%+v err=%v", resolution, err)
		}
	})

	t.Run("conflicting terminal targets remain pending", func(t *testing.T) {
		store := newTestStore(t)
		projection := testShadowTaskLearningProjection()
		run := persistShadowTaskLearningMaterializationTestRun(
			t,
			store,
			projection,
			1_000,
		)
		for index, targetID := range []string{"memory-target-one", "memory-target-two"} {
			if _, _, err := store.AddShadowTaskLearningMaterialization(
				ShadowTaskLearningMaterialization{
					RevisionID: projection.Candidates[0].RevisionID,
					TargetKind: projection.Candidates[0].TargetKind,
					TargetID:   targetID,
					Status:     ShadowTaskLearningMaterializationMaterialized,
					CreatedAt:  int64(1_100 + index),
				},
			); err != nil {
				t.Fatal(err)
			}
		}
		resolution, err := BuildShadowTaskLearningMaterializationResolution(
			store,
			run.ID,
		)
		if err != nil || resolution.Complete || resolution.InvalidCount != 1 ||
			resolution.TerminalCount != 0 {
			t.Fatalf("target-conflict resolution=%+v err=%v", resolution, err)
		}
	})

	for _, tc := range []struct {
		name     string
		decision string
		status   string
		targetID string
		count    func(ShadowTaskLearningMaterializationResolution) int
	}{
		{
			name:     "materialized terminal",
			decision: ShadowTaskLearningDecisionQualified,
			status:   ShadowTaskLearningMaterializationMaterialized,
			targetID: "memory-target",
			count: func(value ShadowTaskLearningMaterializationResolution) int {
				return value.MaterializedCount
			},
		},
		{
			name:     "skipped terminal",
			decision: ShadowTaskLearningDecisionCollecting,
			status:   ShadowTaskLearningMaterializationSkipped,
			count: func(value ShadowTaskLearningMaterializationResolution) int {
				return value.SkippedCount
			},
		},
		{
			name:     "denied terminal",
			decision: ShadowTaskLearningDecisionQualified,
			status:   ShadowTaskLearningMaterializationDenied,
			count: func(value ShadowTaskLearningMaterializationResolution) int {
				return value.DeniedCount
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			projection := testShadowTaskLearningProjection()
			projection.Candidates[0].Qualification.Decision = tc.decision
			run := persistShadowTaskLearningMaterializationTestRun(
				t,
				store,
				projection,
				1_000,
			)
			if _, added, err := store.AddShadowTaskLearningMaterialization(
				ShadowTaskLearningMaterialization{
					RevisionID: projection.Candidates[0].RevisionID,
					TargetKind: projection.Candidates[0].TargetKind,
					TargetID:   tc.targetID,
					Status:     tc.status,
					CreatedAt:  1_100,
				},
			); err != nil || !added {
				t.Fatalf("persist terminal receipt: added=%v err=%v", added, err)
			}
			resolution, err := BuildShadowTaskLearningMaterializationResolution(
				store,
				run.ID,
			)
			if err != nil || !resolution.Complete ||
				resolution.TerminalCount != 1 || tc.count(resolution) != 1 {
				t.Fatalf("terminal resolution=%+v err=%v", resolution, err)
			}
		})
	}

	t.Run("no candidate projection is complete", func(t *testing.T) {
		store := newTestStore(t)
		projection := ShadowTaskLearningProjection{
			PolicyVersion:   ShadowTaskLearningPolicyVersion,
			AuthorityEffect: ShadowTaskLearningAuthorityNone,
			EvaluationCount: 1,
			ExcludedEvaluations: []ShadowTaskLearningExclusion{{
				EvaluationID: "excluded-evaluation",
				Reason:       "insufficient source-backed evidence",
			}},
		}
		run := persistShadowTaskLearningMaterializationTestRun(
			t,
			store,
			projection,
			1_000,
		)
		resolution, err := BuildShadowTaskLearningMaterializationResolution(
			store,
			run.ID,
		)
		if err != nil || !resolution.Complete ||
			resolution.CandidateCount != 0 || resolution.TerminalCount != 0 {
			t.Fatalf("no-candidate resolution=%+v err=%v", resolution, err)
		}
	})
}

func TestShadowTaskLearningMaterializationReceiptRetryIgnoresRetryTime(
	t *testing.T,
) {
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	persistShadowTaskLearningMaterializationTestRun(t, store, projection, 1_000)
	input := ShadowTaskLearningMaterialization{
		RevisionID: projection.Candidates[0].RevisionID,
		TargetKind: projection.Candidates[0].TargetKind,
		TargetID:   "memory-target",
		Status:     ShadowTaskLearningMaterializationMaterialized,
		CreatedAt:  1_100,
	}
	first, added, err := store.AddShadowTaskLearningMaterialization(input)
	if err != nil || !added {
		t.Fatalf("first receipt: added=%v err=%v", added, err)
	}
	input.CreatedAt = 9_999
	second, added, err := store.AddShadowTaskLearningMaterialization(input)
	if err != nil || added || second.ID != first.ID || second.CreatedAt != 1_100 {
		t.Fatalf("retry receipt=%+v added=%v err=%v", second, added, err)
	}
}

func TestReconcileShadowTaskLearningMaterializationsIsModelFreeAndIdempotent(
	t *testing.T,
) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	run := persistShadowTaskLearningMaterializationTestRun(
		t,
		store,
		projection,
		1_000,
	)
	candidate := projection.Candidates[0]
	targetID := shadowTaskLearningTargetID(candidate)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	content := buildShadowLearningMemoryMarkdown(
		store, targetID, candidate, time.UnixMilli(run.CreatedAt),
	)
	if err := os.WriteFile(
		filepath.Join(dir, targetID+".md"),
		[]byte(content),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	recovery, err := app.reconcileShadowTaskLearningMaterializations(
		run.ID,
		2_000,
	)
	if err != nil || !recovery.Resolution.Complete ||
		!reflect.DeepEqual(
			recovery.AttemptedRevisionIDs,
			[]string{candidate.RevisionID},
		) ||
		!reflect.DeepEqual(
			recovery.ResolvedRevisionIDs,
			[]string{candidate.RevisionID},
		) || memoryEntryHasHistory(dir, targetID) {
		t.Fatalf("first recovery=%+v history=%v err=%v", recovery, memoryEntryHasHistory(dir, targetID), err)
	}
	second, err := app.reconcileShadowTaskLearningMaterializations(
		run.ID,
		3_000,
	)
	if err != nil || !second.Resolution.Complete ||
		len(second.AttemptedRevisionIDs) != 0 ||
		len(second.ResolvedRevisionIDs) != 0 || memoryEntryHasHistory(dir, targetID) {
		t.Fatalf("idempotent recovery=%+v history=%v err=%v", second, memoryEntryHasHistory(dir, targetID), err)
	}
	receipts, err := store.ListShadowTaskLearningMaterializationsForRun(run.ID)
	if err != nil || len(receipts) != 1 || receipts[0].CreatedAt != 2_000 ||
		receipts[0].Status != ShadowTaskLearningMaterializationMaterialized {
		t.Fatalf("recovery receipts=%+v err=%v", receipts, err)
	}
}

func TestReconcileShadowTaskLearningMaterializationsConcurrentMemoryRetry(
	t *testing.T,
) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	run := persistShadowTaskLearningMaterializationTestRun(
		t,
		store,
		projection,
		1_000,
	)
	app := &App{activity: &activityMonitorState{store: store}}
	var wg sync.WaitGroup
	errorsByRetry := make([]error, 2)
	for index := range errorsByRetry {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errorsByRetry[index] = app.reconcileShadowTaskLearningMaterializations(
				run.ID,
				int64(2_000+index),
			)
		}(index)
	}
	wg.Wait()
	for index, err := range errorsByRetry {
		if err != nil {
			t.Fatalf("concurrent recovery %d: %v", index, err)
		}
	}
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	targetID := shadowTaskLearningTargetID(projection.Candidates[0])
	if memoryEntryHasHistory(dir, targetID) {
		t.Fatal("concurrent identical Memory recovery created history")
	}
	receipts, err := store.ListShadowTaskLearningMaterializationsForRun(run.ID)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("concurrent receipts=%+v err=%v", receipts, err)
	}
}

func TestReconcileShadowTaskLearningMaterializationsAvoidsDuplicateKnowledgeHistory(
	t *testing.T,
) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	projection.Candidates[0].TargetKind = ShadowTaskLearningTargetKnowledge
	run := persistShadowTaskLearningMaterializationTestRun(
		t,
		store,
		projection,
		1_000,
	)
	candidate := projection.Candidates[0]
	targetID := shadowTaskLearningTargetID(candidate)
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	content := buildOKFKnowledgeMarkdown(
		targetID,
		shadowTaskLearningTitle(candidate.Statement),
		"Repeated, source-backed work pattern learned from evaluated user outcomes.",
		time.UnixMilli(run.CreatedAt),
		"shadow_task_evaluation",
		nil,
		nil,
		shadowTaskLearningEvidencePoints(candidate),
	)
	if err := os.WriteFile(
		filepath.Join(dir, targetID+".md"),
		[]byte(content),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	recovery, err := app.reconcileShadowTaskLearningMaterializations(
		run.ID,
		2_000,
	)
	if err != nil || !recovery.Resolution.Complete ||
		knowledgeEntryHasHistory(dir, targetID) {
		t.Fatalf(
			"knowledge recovery=%+v history=%v err=%v",
			recovery,
			knowledgeEntryHasHistory(dir, targetID),
			err,
		)
	}
}

func TestReconcileShadowTaskLearningMaterializationsReusesTypedSelfTarget(
	t *testing.T,
) {
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	projection.Candidates[0].TargetKind = ShadowTaskLearningTargetTypedSelf
	projection.Candidates[0].SuggestedSelfKind = selfModelKindPreference
	projection.Candidates[0].SuggestedSelfStatus = selfModelStatusProvisional
	run := persistShadowTaskLearningMaterializationTestRun(
		t,
		store,
		projection,
		1_000,
	)
	candidate := projection.Candidates[0]
	app := &App{activity: &activityMonitorState{store: store}}
	preexistingID, errorText := app.applyQualifiedShadowTaskLearningCandidate(
		candidate,
		run.CreatedAt,
	)
	if errorText != "" || preexistingID == "" {
		t.Fatalf("preexisting typed Self target=%q error=%q", preexistingID, errorText)
	}
	before, err := store.ListCurrentSelfModelEntries()
	if err != nil || len(before) != 1 {
		t.Fatalf("typed Self before=%+v err=%v", before, err)
	}
	recovery, err := app.reconcileShadowTaskLearningMaterializations(
		run.ID,
		2_000,
	)
	if err != nil || !recovery.Resolution.Complete {
		t.Fatalf("typed Self recovery=%+v err=%v", recovery, err)
	}
	after, err := store.ListCurrentSelfModelEntries()
	if err != nil || len(after) != 1 || after[0].ID != preexistingID ||
		after[0].Revision != before[0].Revision {
		t.Fatalf("typed Self after=%+v err=%v", after, err)
	}
}

func TestReconcileShadowTaskLearningCapabilityProfileNeedsOnlyReceipt(
	t *testing.T,
) {
	store := newTestStore(t)
	projection := testShadowTaskLearningProjection()
	projection.Candidates[0].TargetKind =
		ShadowTaskLearningTargetCapabilityProfile
	run := persistShadowTaskLearningMaterializationTestRun(
		t,
		store,
		projection,
		1_000,
	)
	app := &App{activity: &activityMonitorState{store: store}}
	first, err := app.reconcileShadowTaskLearningMaterializations(run.ID, 2_000)
	if err != nil || !first.Resolution.Complete ||
		first.Resolution.MaterializedCount != 1 {
		t.Fatalf("capability recovery=%+v err=%v", first, err)
	}
	second, err := app.reconcileShadowTaskLearningMaterializations(run.ID, 3_000)
	if err != nil || !second.Resolution.Complete ||
		len(second.AttemptedRevisionIDs) != 0 {
		t.Fatalf("capability replay=%+v err=%v", second, err)
	}
	receipts, err := store.ListShadowTaskLearningMaterializationsForRun(run.ID)
	if err != nil || len(receipts) != 1 ||
		receipts[0].TargetID != projection.Candidates[0].RevisionID {
		t.Fatalf("capability receipts=%+v err=%v", receipts, err)
	}
}
