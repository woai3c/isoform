package main

import (
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func taskShadowReleaseProvenanceTestDetail() TaskShadowDebugDetail {
	return TaskShadowDebugDetail{
		ProductionRollout: TaskShadowDebugProductionRollout{
			Workflow: TaskWorkflowComposeText,
			Eligible: true,
		},
		Episode: TaskShadowDebugEpisodeDetail{
			Episode: TaskShadowDebugEpisode{
				ID:            "episode-a",
				CurrentStatus: TaskEpisodeStatusCompleted,
				FrozenContext: TaskShadowDebugFrozenContext{
					SHA256:                strings.Repeat("a", 64),
					ManifestEntryCount:    3,
					ManifestIncludedCount: 2,
				},
			},
		},
		Runs: []TaskShadowDebugRun{{
			Run: ShadowRun{
				ID:            "run-a",
				TaskEpisodeID: "episode-a",
				CapabilityKey: TaskWorkflowComposeText,
				PromptVersion: shadowTaskPromptVersion,
			},
		}},
	}
}

func taskShadowReleaseProvenanceTestCorpus(
	t *testing.T,
	admission string,
	mutatePayload func(*TaskShadowObserverTraceEventPayload),
) taskShadowReleaseObserverCorpus {
	return taskShadowReleaseProvenanceTestCorpusWithStatus(
		t,
		admission,
		TaskShadowObserverEventCompleted,
		mutatePayload,
	)
}

func taskShadowReleaseProvenanceTestCorpusWithStatus(
	t *testing.T,
	admission, terminalStatus string,
	mutatePayload func(*TaskShadowObserverTraceEventPayload),
) taskShadowReleaseObserverCorpus {
	t.Helper()
	store := newTaskShadowDebugTestStore(t)
	trace, _ := taskShadowObserverCoverageTestTrace(
		t,
		store,
		"release-origin-trace",
		42,
		"release-origin-group",
		admission,
		4_000,
	)
	payload := taskShadowObserverTerminalPayload()
	if mutatePayload != nil {
		mutatePayload(&payload)
	}
	if _, added, err := store.CompleteTaskShadowObserverTrace(
		trace.ID,
		terminalStatus,
		payload,
		5_000,
	); err != nil || !added {
		t.Fatalf("complete release observer trace: added=%t err=%v", added, err)
	}
	trace, err := store.GetTaskShadowObserverTrace(trace.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListTaskShadowObserverTraceEvents(trace.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := taskShadowObserverCoverageTestSource(
		42,
		trace.GroupID,
		admission,
		TaskShadowObserverDispatchRecorded,
		trace.ID,
		5_000,
	)
	return taskShadowReleaseObserverCorpus{
		Sources:                 []taskShadowObserverCoverageSource{source},
		Traces:                  []TaskShadowObserverTrace{trace},
		EventsByTrace:           map[string][]TaskShadowObserverTraceEvent{trace.ID: events},
		TotalCurrentTraceCount:  1,
		LoadedCurrentTraceCount: 1,
		AttemptLookup: func(id string) (TaskAgentAttempt, error) {
			return TaskAgentAttempt{
				ID:            id,
				Phase:         TaskAgentAttemptPhaseOnset,
				PromptVersion: taskOnsetPromptVersion,
				Status:        TaskAgentAttemptStatusSucceeded,
			}, nil
		},
	}
}

func TestTaskShadowReleaseObserverProvenanceRequiresExactProductionOrigin(
	t *testing.T,
) {
	detail := taskShadowReleaseProvenanceTestDetail()
	corpus := taskShadowReleaseProvenanceTestCorpus(
		t,
		TaskShadowObserverAdmissionEligible,
		nil,
	)
	result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
	if result.Version != TaskShadowReleaseObserverProvenanceVersion ||
		result.State != TaskShadowReleaseObserverOriginVerified ||
		!result.SourceComplete || !result.OriginComplete ||
		!result.IntegrityValid || result.PipelineRunID != 42 ||
		result.TraceID != "release-origin-trace" ||
		result.CoverageState != TaskShadowObserverCoverageCovered ||
		result.ProductionOnsetAttemptID != "production-attempt" ||
		result.TaskEpisodeID != "episode-a" ||
		result.ShadowRunID != "run-a" ||
		result.CandidateTraceCount != 1 ||
		result.CandidateDecisionCount != 1 {
		t.Fatalf("exact release provenance=%+v", result)
	}
}

func TestTaskShadowReleaseGateCompletesOnlyWithResolvedObserverOrigin(t *testing.T) {
	detail := taskShadowReleaseProvenanceTestDetail()
	detail.Runs[0].PairReadiness = ShadowTaskPairReadiness{
		Workflow:                    TaskWorkflowComposeText,
		State:                       ShadowTaskPairStateEvaluated,
		CurrentGeneration:           true,
		TaskEpisodeStatus:           TaskEpisodeStatusCompleted,
		ShadowAttemptStatus:         "completed",
		UserResultMaterialized:      true,
		UserResultSHA256:            strings.Repeat("b", 64),
		UserResultDestinationSHA256: strings.Repeat("c", 64),
		ShadowResultMaterialized:    true,
		ShadowResultSHA256:          strings.Repeat("d", 64),
		CurrentEvaluationIDs:        []string{"release-evaluation"},
	}
	detail.Runs[0].ExecutionAttempts = []TaskShadowDebugExecutionAttempt{{
		Attempt: ShadowExecutionAttempt{
			ID:     "release-shadow-attempt",
			Status: "completed",
		},
	}}
	detail.Learning = TaskShadowDebugLearning{Runs: []ShadowTaskLearningRun{{
		ID:            "release-learning",
		PolicyVersion: ShadowTaskLearningPolicyVersion,
		Projection: ShadowTaskLearningProjection{
			PolicyVersion: ShadowTaskLearningPolicyVersion,
			Candidates: []ShadowTaskLearningCandidate{{
				Dimensions: []ShadowTaskLearningDimensionEvidence{{
					EvaluationID: "release-evaluation",
				}},
			}},
		},
	}}}
	corpus := taskShadowReleaseProvenanceTestCorpus(
		t,
		TaskShadowObserverAdmissionEligible,
		nil,
	)
	detail.releaseObserverProvenance =
		buildTaskShadowReleaseObserverProvenance(&detail, corpus)
	detail.releaseLearningRunID = "release-learning"
	detail.releaseLearningResolution = &ShadowTaskLearningMaterializationResolution{
		Version:             ShadowTaskLearningMaterializationVersion,
		RunID:               "release-learning",
		Complete:            true,
		CandidateCount:      1,
		TerminalCount:       1,
		MaterializedCount:   1,
		PendingRevisionIDs:  []string{},
		ErrorRevisionIDs:    []string{},
		TerminalRevisionIDs: []string{"release-learning-revision"},
	}
	gate := taskShadowReleaseGateFromDetail(
		&detail,
		false,
		activitymon.AppInfo{},
	)
	if !gate.Complete || gate.Stage != "complete" ||
		gate.Version != taskShadowReleaseGateVersion || len(gate.Steps) != 9 ||
		!gate.ObserverProvenance.OriginComplete {
		t.Fatalf("source-bound complete gate=%+v", gate)
	}
	for _, step := range gate.Steps {
		if !step.Complete || step.EvidenceID == "" {
			t.Fatalf("source-bound gate missing step=%+v", step)
		}
	}
}

func TestTaskShadowReleaseObserverProvenanceFailsClosed(t *testing.T) {
	t.Run("bounded scan truncated", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			nil,
		)
		corpus.TotalCurrentTraceCount++
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverScanTruncated ||
			result.SourceComplete || result.OriginComplete {
			t.Fatalf("truncated provenance=%+v", result)
		}
	})

	t.Run("source pruned", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			nil,
		)
		corpus.Sources = nil
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverSourcePruned ||
			result.SourceComplete || result.OriginComplete ||
			!result.IntegrityValid {
			t.Fatalf("pruned provenance=%+v", result)
		}
	})

	t.Run("root only receipt", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			nil,
		)
		corpus.Sources[0].TraceID = ""
		corpus.Sources[0].DispatchStatus = TaskShadowObserverDispatchPending
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverSourceNotCovered ||
			result.CoverageState != TaskShadowObserverCoverageCoveredRootOnly ||
			result.SourceComplete || result.OriginComplete {
			t.Fatalf("root-only provenance=%+v", result)
		}
	})

	t.Run("continuation is not onset", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionCooldown,
			nil,
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverBoundaryMismatch ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("continuation provenance=%+v", result)
		}
	})

	t.Run("reconciliation is not origin", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.WorkflowDecisions[0].Source =
					TaskShadowWorkflowDecisionReconciliation
			},
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverOriginMissing ||
			result.OriginComplete {
			t.Fatalf("reconciliation provenance=%+v", result)
		}
	})

	t.Run("production onset attempt required", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.ProductionOnsetAttemptIDs = nil
			},
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverProductionOnsetMissing ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("missing production onset=%+v", result)
		}
	})

	t.Run("captured onset attempt must be listed", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.WorkflowDecisions[0].ProductionOnsetAttemptID =
					"unlisted-production-attempt"
			},
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverProductionOnsetMissing ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("unlisted production onset=%+v", result)
		}
	})

	t.Run("captured onset selects its exact sibling attempt", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.ProductionOnsetAttemptIDs = []string{
					"aaa-failed-sibling",
					"production-attempt",
				}
			},
		)
		corpus.AttemptLookup = func(id string) (TaskAgentAttempt, error) {
			status := TaskAgentAttemptStatusFailed
			if id == "production-attempt" {
				status = TaskAgentAttemptStatusSucceeded
			}
			return TaskAgentAttempt{
				ID:            id,
				Phase:         TaskAgentAttemptPhaseOnset,
				PromptVersion: taskOnsetPromptVersion,
				Status:        status,
			}, nil
		}
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverOriginVerified ||
			result.ProductionOnsetAttemptID != "production-attempt" ||
			!result.OriginComplete {
			t.Fatalf("exact sibling production onset=%+v", result)
		}
	})

	t.Run("production onset attempt must resolve", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			nil,
		)
		corpus.AttemptLookup = nil
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverProductionOnsetMissing ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("orphaned production onset=%+v", result)
		}
	})

	t.Run("terminal receipt required", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpusWithStatus(
			t,
			TaskShadowObserverAdmissionEligible,
			TaskShadowObserverEventCancelled,
			nil,
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverTerminalIncomplete ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("nonterminal provenance=%+v", result)
		}
	})

	t.Run("First Run exclusion cannot qualify", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.FirstRunDecision = TaskShadowObserverFirstRunExcluded
			},
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverBoundaryMismatch ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("First Run provenance=%+v", result)
		}
	})

	t.Run("frozen context must match", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		detail.Episode.Episode.FrozenContext.SHA256 = strings.Repeat("b", 64)
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			nil,
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverContextBindingMismatch ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("context mismatch=%+v", result)
		}
	})

	t.Run("created run must match", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.WorkflowDecisions[0].State =
					TaskShadowWorkflowStateExistingRun
			},
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverRunBindingMismatch ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("existing run provenance=%+v", result)
		}
	})

	t.Run("workflow must match", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.WorkflowDecisions[0].Workflow = TaskWorkflowAnalyzeText
			},
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverWorkflowBindingMismatch ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("workflow mismatch=%+v", result)
		}
	})

	t.Run("terminal episode list must match", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			func(payload *TaskShadowObserverTraceEventPayload) {
				payload.TaskEpisodeIDs = []string{"another-episode"}
			},
		)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverEpisodeBindingMismatch ||
			!result.SourceComplete || result.OriginComplete {
			t.Fatalf("episode mismatch=%+v", result)
		}
	})

	t.Run("corrupt trace cannot qualify", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			nil,
		)
		corpus.Traces[0].RootHash = strings.Repeat("f", 64)
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverTraceCorrupt ||
			result.SourceComplete || result.OriginComplete {
			t.Fatalf("corrupt provenance=%+v", result)
		}
	})

	t.Run("legacy origin cannot qualify", func(t *testing.T) {
		detail := taskShadowReleaseProvenanceTestDetail()
		corpus := taskShadowReleaseProvenanceTestCorpus(
			t,
			TaskShadowObserverAdmissionEligible,
			nil,
		)
		corpus.Traces[0].ContractVersion =
			TaskShadowObserverTraceContractVersionV1
		corpus.TotalCurrentTraceCount = 0
		corpus.LoadedCurrentTraceCount = 0
		result := buildTaskShadowReleaseObserverProvenance(&detail, corpus)
		if result.State != TaskShadowReleaseObserverLegacyOrigin ||
			result.SourceComplete || result.OriginComplete {
			t.Fatalf("legacy provenance=%+v", result)
		}
	})
}

func TestLoadTaskShadowReleaseObserverCorpusIsReadOnlyAndAgeBound(t *testing.T) {
	store := newTaskShadowDebugTestStore(t)
	now := time.Now().UnixMilli()
	group := sessionGroup{Sessions: []ActivitySessionRow{{
		ID:         "release-corpus-session",
		StartTime:  now - 2_000,
		EndTime:    now - 1_000,
		EventCount: 1,
		CreatedAt:  now - 3_000,
	}}}
	oldSource := newGroupPipelineRun(group, time.UnixMilli(now-1_000))
	oldSource.ShadowObserverExpected = true
	oldSource.ShadowObserverAdmission = TaskShadowObserverAdmissionEligible
	oldSource.ShadowObserverDispatchStatus = TaskShadowObserverDispatchPending
	oldSourceID, err := store.SaveActivityPipelineRun(oldSource)
	if err != nil {
		t.Fatal(err)
	}
	currentSource := oldSource
	currentSource.ID = 0
	currentSource.GroupID = "release-current-group"
	currentSource.StartedAt = now - 500
	currentSource.CompletedAt = now - 250
	currentSourceID, err := store.SaveActivityPipelineRun(currentSource)
	if err != nil {
		t.Fatal(err)
	}
	oldAt := now - activityPipelineMaxAge.Milliseconds() - 1
	if _, err := store.db.Exec(`
		UPDATE activity_pipeline_runs
		SET started_at = ?, completed_at = ?
		WHERE id = ?`, oldAt, oldAt, oldSourceID); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_pipeline_runs`).
		Scan(&before); err != nil {
		t.Fatal(err)
	}
	corpus, err := loadTaskShadowReleaseObserverCorpus(store, now)
	if err != nil {
		t.Fatal(err)
	}
	var after int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_pipeline_runs`).
		Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != 2 || after != before || len(corpus.Sources) != 1 ||
		corpus.Sources[0].ID != currentSourceID || len(corpus.Traces) != 0 ||
		corpus.TotalCurrentTraceCount != 0 || corpus.AttemptLookup == nil {
		t.Fatalf(
			"read-only corpus before=%d after=%d old=%d current=%d corpus=%+v",
			before,
			after,
			oldSourceID,
			currentSourceID,
			corpus,
		)
	}
}

func TestTaskShadowReleaseObserverProvenanceRejectsDuplicateOrigins(t *testing.T) {
	detail := taskShadowReleaseProvenanceTestDetail()
	first := taskShadowReleaseProvenanceTestCorpus(
		t,
		TaskShadowObserverAdmissionEligible,
		nil,
	)
	second := first.Traces[0]
	second.ID = "second-release-origin-trace"
	second.SourcePipelineRunID = 43
	second.LatestEvent.TraceID = second.ID
	secondSource := first.Sources[0]
	secondSource.ID = 43
	secondSource.TraceID = second.ID
	first.Traces = append(first.Traces, second)
	first.Sources = append(first.Sources, secondSource)
	first.EventsByTrace[second.ID] = first.EventsByTrace[first.Traces[0].ID]
	first.TotalCurrentTraceCount = 2
	first.LoadedCurrentTraceCount = 2
	result := buildTaskShadowReleaseObserverProvenance(&detail, first)
	if result.State != TaskShadowReleaseObserverDuplicateOrigin ||
		result.CandidateTraceCount != 2 ||
		result.SourceComplete || result.OriginComplete {
		t.Fatalf("duplicate origin provenance=%+v", result)
	}
}
