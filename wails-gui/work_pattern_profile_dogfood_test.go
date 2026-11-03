package main

import (
	"strings"
	"testing"
)

func TestWorkPatternProfileDogfoodScenarios(t *testing.T) {
	t.Run("rapid switching", func(t *testing.T) {
		store := newTestStore(t)
		cutoff := int64(0)
		for session := 0; session < 3; session++ {
			cutoff = saveWorkPatternSession(
				t,
				store,
				"dogfood-rapid-"+string(rune('a'+session)),
				"done",
				10_000+int64(session)*200_000,
				[]string{
					"Safari",
					"ChatGPT",
					"Terminal",
					"Safari",
					"ChatGPT",
					"Terminal",
				},
			)
		}
		snapshot, _, err := store.EnsureWorkPatternProfileSnapshot(
			cutoff,
			cutoff+1,
		)
		if err != nil {
			t.Fatal(err)
		}
		global := projectionByScope(
			t,
			snapshot,
			WorkPatternScopeGlobal,
			"all",
		)
		if snapshot.Status != WorkPatternProfileStatusQualified ||
			metricByName(
				t,
				global,
				"switches_per_active_hour",
			).Value <= 0 {
			t.Fatalf("rapid profile=%+v", snapshot)
		}
	})

	t.Run("focused single app", func(t *testing.T) {
		store := newTestStore(t)
		cutoff := int64(0)
		for session := 0; session < 12; session++ {
			cutoff = saveWorkPatternSession(
				t,
				store,
				"dogfood-focused-"+string(rune('a'+session)),
				"done",
				10_000+int64(session)*30_000,
				[]string{"Editor"},
			)
		}
		snapshot, _, err := store.EnsureWorkPatternProfileSnapshot(
			cutoff,
			cutoff+1,
		)
		if err != nil {
			t.Fatal(err)
		}
		global := projectionByScope(
			t,
			snapshot,
			WorkPatternScopeGlobal,
			"all",
		)
		switches := metricByName(
			t,
			global,
			"switches_per_active_hour",
		)
		if snapshot.Status != WorkPatternProfileStatusQualified ||
			switches.Value != 0 {
			t.Fatalf(
				"focused profile status=%s switches=%+v",
				snapshot.Status,
				switches,
			)
		}
	})

	t.Run("interrupted and resumed", func(t *testing.T) {
		store := newTestStore(t)
		cutoff := int64(0)
		for session := 0; session < 4; session++ {
			cutoff = saveWorkPatternSession(
				t,
				store,
				"dogfood-resume-"+string(rune('a'+session)),
				"done",
				10_000+int64(session)*100_000,
				[]string{"Editor", "Mail", "Editor"},
			)
		}
		snapshot, _, err := store.EnsureWorkPatternProfileSnapshot(
			cutoff,
			cutoff+1,
		)
		if err != nil {
			t.Fatal(err)
		}
		global := projectionByScope(
			t,
			snapshot,
			WorkPatternScopeGlobal,
			"all",
		)
		returnTime := metricByName(
			t,
			global,
			"median_return_seconds",
		)
		resumeRate := metricByName(
			t,
			global,
			"resume_within_15m_rate",
		)
		if snapshot.Status != WorkPatternProfileStatusQualified ||
			returnTime.SampleCount < 4 ||
			resumeRate.Value != 1 {
			t.Fatalf(
				"resume profile return=%+v rate=%+v",
				returnTime,
				resumeRate,
			)
		}
	})

	t.Run("insufficient evidence stays neutral", func(t *testing.T) {
		store := newTestStore(t)
		cutoff := saveWorkPatternSession(
			t,
			store,
			"dogfood-insufficient",
			"done",
			10_000,
			[]string{"Editor", "Terminal"},
		)
		snapshot, _, err := store.EnsureWorkPatternProfileSnapshot(
			cutoff,
			cutoff+1,
		)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := buildWorkPatternProfileContext(
			store,
			snapshot,
			TaskContextSourceResult{},
			"prepare a release",
			[]string{"test.editor", "test.terminal"},
			[]string{"test.editor->test.terminal"},
			WorkPatternInfluenceProfile,
		)
		if err != nil {
			t.Fatal(err)
		}
		if workPatternProfileCanInfluence(profile) ||
			snapshot.Status != WorkPatternProfileStatusInsufficient {
			t.Fatalf(
				"insufficient profile became influential: %+v",
				profile,
			)
		}
	})
}

func TestWorkPatternProfileDoesNotMergeExplicitMultiProjectStarts(
	t *testing.T,
) {
	profile := WorkPatternProfileContext{
		SnapshotID:     "multi-project-profile",
		SnapshotSHA256: strings.Repeat("a", 64),
		PolicyVersion:  WorkPatternProfilePolicyVersion,
		SourceCutoffAt: 999,
		Status:         WorkPatternProfileStatusQualified,
		InfluenceMode:  WorkPatternInfluenceProfile,
		SelectedProjections: []WorkPatternProjection{{
			ScopeKind: WorkPatternScopeApp,
			ScopeKey:  "test.editor",
			Status:    WorkPatternProfileStatusQualified,
			Metrics: []WorkPatternMetric{{
				Name:  "median_dwell_seconds",
				Value: 600,
			}},
		}},
	}
	influenceRef := "metric:app:test.editor:median_dwell_seconds"
	input := TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    "multi-project",
		WindowStart: 1_000,
		CutoffAt:    2_000,
		WorkProfile: &profile,
		Evidence: []TaskOnsetEvidence{
			{
				Ref:        "event:alpha",
				SourceKind: "activity_event",
				SourceID:   "alpha",
				OccurredAt: 1_100,
				AppKey:     "test.editor",
				Summary:    "Opened the Project Alpha release brief",
			},
			{
				Ref:        "event:beta",
				SourceKind: "activity_event",
				SourceID:   "beta",
				OccurredAt: 1_200,
				AppKey:     "test.editor",
				Summary:    "Opened the separate Project Beta incident note",
			},
		},
	}
	response := normalizeTaskOnsetResponse(input, TaskOnsetResponse{
		Episodes: []TaskOnsetPrediction{
			{
				LocalKey:             "alpha",
				Task:                 "Prepare Project Alpha release",
				DesiredResult:        "Alpha release brief is ready",
				InferredStartedAt:    1_100,
				TaskConfidence:       0.8,
				BoundaryConfidence:   0.7,
				ResultConfidence:     0.7,
				EvidenceRefs:         []string{"event:alpha"},
				ProfileInfluenceRefs: []string{influenceRef},
			},
			{
				LocalKey:             "beta",
				Task:                 "Investigate Project Beta incident",
				DesiredResult:        "Beta incident cause is identified",
				InferredStartedAt:    1_200,
				TaskConfidence:       0.8,
				BoundaryConfidence:   0.7,
				ResultConfidence:     0.7,
				EvidenceRefs:         []string{"event:beta"},
				ProfileInfluenceRefs: []string{influenceRef},
			},
		},
	})
	if len(response.Episodes) != 2 ||
		response.Episodes[0].LocalKey == response.Episodes[1].LocalKey {
		t.Fatalf("multi-project starts were collapsed: %+v", response)
	}
}
