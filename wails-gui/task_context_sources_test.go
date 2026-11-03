package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func taskContextSourceTestTime(hour int) time.Time {
	return time.Date(2026, time.July, 28, hour, 0, 0, 0, time.UTC)
}

func writeTaskContextSourceTestFile(
	t *testing.T,
	path, content string,
	modTime time.Time,
) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func taskContextSourceCandidatesByKind(
	items []TaskContextCandidate,
	sourceKind string,
) []TaskContextCandidate {
	result := make([]TaskContextCandidate, 0)
	for _, item := range items {
		if item.SourceKind == sourceKind {
			result = append(result, item)
		}
	}
	return result
}

func taskContextSourceHasExclusion(
	items []TaskContextSourceExclusion,
	sourceKind, reason string,
) bool {
	for _, item := range items {
		if item.SourceKind == sourceKind && item.Reason == reason {
			return true
		}
	}
	return false
}

func TestGatherTaskContextSourcesScansAllHistoricalMarkdownWithoutCountCap(
	t *testing.T,
) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	before := taskContextSourceTestTime(9)
	cutoff := taskContextSourceTestTime(10).UnixMilli()
	after := taskContextSourceTestTime(11)
	memoryDirPath := filepath.Join(home, configDirName, "memory")
	knowledgeDirPath := filepath.Join(home, configDirName, "knowledge")

	const memoryCount = 137
	for index := 0; index < memoryCount; index++ {
		id := fmt.Sprintf("memory-%03d", index)
		content := buildOKFMemoryMarkdown(
			id,
			fmt.Sprintf("Release fact %03d", index),
			before,
			memorySource("activity"),
			"topic_highlight",
			[]string{fmt.Sprintf(
				"Release evidence item %03d is source backed.",
				index,
			)},
		)
		writeTaskContextSourceTestFile(
			t,
			filepath.Join(memoryDirPath, id+".md"),
			content,
			before,
		)
	}
	writeTaskContextSourceTestFile(
		t,
		filepath.Join(knowledgeDirPath, "knowledge-1.md"),
		buildOKFKnowledgeMarkdown(
			"knowledge-1",
			"Release checklist",
			"Observed release process",
			before,
			"activity",
			[]string{"Terminal"},
			[]string{"session-1"},
			[]string{"Run focused tests before the full suite."},
		),
		before,
	)

	// The live file is from after the task began, but its source-backed
	// pre-cutoff revision remains available in history.
	historicalID := "historical-memory"
	writeTaskContextSourceTestFile(
		t,
		filepath.Join(memoryDirPath, historicalID+".md"),
		buildOKFMemoryMarkdown(
			historicalID,
			"Future replacement",
			after,
			memorySource("activity"),
			"topic_highlight",
			[]string{"This replacement did not exist at task start."},
		),
		after,
	)
	writeTaskContextSourceTestFile(
		t,
		filepath.Join(
			memoryDirPath,
			memoryHistoryDirName,
			historicalID,
			fmt.Sprintf("%d.md", after.UnixMilli()),
		),
		buildOKFMemoryMarkdown(
			historicalID,
			"Historical release rule",
			before,
			memorySource("activity"),
			"topic_highlight",
			[]string{"This was the valid rule when the task began."},
		),
		after,
	)

	writeTaskContextSourceTestFile(
		t,
		filepath.Join(memoryDirPath, "future-only.md"),
		buildOKFMemoryMarkdown(
			"future-only",
			"Future only",
			after,
			memorySource("activity"),
			"topic_highlight",
			[]string{"Unavailable at task start."},
		),
		after,
	)
	writeTaskContextSourceTestFile(
		t,
		filepath.Join(memoryDirPath, "stale-timestamp-current.md"),
		buildOKFMemoryMarkdown(
			"stale-timestamp-current",
			"Possibly edited current content",
			before,
			memorySource("activity"),
			"topic_highlight",
			[]string{"The metadata is old but the current file was touched later."},
		),
		after,
	)
	writeTaskContextSourceTestFile(
		t,
		filepath.Join(memoryDirPath, "hidden-shadow.md"),
		buildOKFMemoryMarkdown(
			"hidden-shadow",
			"Private mirror output",
			before,
			memorySource("shadow_task_execution"),
			"topic_highlight",
			[]string{"A private shadow worker guessed this."},
		),
		before,
	)

	request := TaskContextSourceRequest{
		CutoffAt: cutoff,
		Query:    "release evidence tests",
		RecentActivity: []TaskContextCandidate{
			{
				SourceKind:      "activity_snapshot",
				SourceID:        "snapshot-before",
				ObservedAt:      before.UnixMilli(),
				Content:         "The user opened the release checklist.",
				EvidenceClass:   TaskContextEvidenceObservedUser,
				PrivacyDecision: TaskContextPrivacyRedact,
			},
			{
				SourceKind:      "activity_snapshot",
				SourceID:        "snapshot-after",
				ObservedAt:      after.UnixMilli(),
				Content:         "The user finished after task start.",
				EvidenceClass:   TaskContextEvidenceObservedUser,
				PrivacyDecision: TaskContextPrivacyRedact,
			},
			{
				SourceKind:      "shadow_task_execution",
				SourceID:        "private-worker",
				ObservedAt:      before.UnixMilli(),
				Content:         "Private worker output",
				EvidenceClass:   TaskContextEvidenceHiddenShadow,
				PrivacyDecision: TaskContextPrivacyRedact,
			},
		},
	}
	first, err := GatherTaskContextSources(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GatherTaskContextSources(request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("source gathering is not deterministic\nfirst=%+v\nsecond=%+v", first, second)
	}

	memories := taskContextSourceCandidatesByKind(
		first.Candidates,
		taskContextSourceMemoryKind,
	)
	if len(memories) != memoryCount+1 {
		t.Fatalf(
			"memory candidates=%d, want %d (full scan plus historical revision)",
			len(memories),
			memoryCount+1,
		)
	}
	if len(memories) <= 100 {
		t.Fatalf("memory candidates unexpectedly capped: %d", len(memories))
	}
	historicalFound := false
	for _, item := range memories {
		if item.SourceID == historicalID {
			historicalFound = strings.Contains(
				item.Content,
				"valid rule when the task began",
			)
		}
		if item.ObservedAt > cutoff {
			t.Fatalf("candidate after cutoff: %+v", item)
		}
	}
	if !historicalFound {
		t.Fatal("pre-cutoff historical memory revision was not selected")
	}
	if got := len(taskContextSourceCandidatesByKind(
		first.Candidates,
		taskContextSourceKnowledgeKind,
	)); got != 1 {
		t.Fatalf("knowledge candidates=%d, want 1", got)
	}
	if got := len(taskContextSourceCandidatesByKind(
		first.Candidates,
		"activity_snapshot",
	)); got != 1 {
		t.Fatalf("recent activity candidates=%d, want 1", got)
	}
	if !taskContextSourceHasExclusion(
		first.Exclusions,
		taskContextSourceMemoryKind,
		TaskContextSourceReasonFuture,
	) {
		t.Fatalf("missing future markdown exclusion: %+v", first.Exclusions)
	}
	if !taskContextSourceHasExclusion(
		first.Exclusions,
		taskContextSourceMemoryKind,
		TaskContextSourceReasonUnverifiable,
	) {
		t.Fatalf("missing unverifiable current-file exclusion: %+v", first.Exclusions)
	}
	if !taskContextSourceHasExclusion(
		first.Exclusions,
		taskContextSourceMemoryKind,
		TaskContextSourceReasonHiddenShadow,
	) {
		t.Fatalf("missing hidden shadow markdown exclusion: %+v", first.Exclusions)
	}
	if !taskContextSourceHasExclusion(
		first.Exclusions,
		"activity_snapshot",
		TaskContextSourceReasonFuture,
	) {
		t.Fatalf("missing future activity exclusion: %+v", first.Exclusions)
	}
	if !taskContextSourceHasExclusion(
		first.Exclusions,
		"shadow_task_execution",
		TaskContextSourceReasonHiddenShadow,
	) {
		t.Fatalf("missing hidden activity exclusion: %+v", first.Exclusions)
	}

	sectionBudgets := make(map[string]int, len(taskContextSectionOrder))
	for _, section := range taskContextSectionOrder {
		sectionBudgets[section] = 2_000_000
	}
	assembled, err := AssembleTaskContext(TaskContextAssemblyInput{
		EpisodeID:     "episode-full-source-scan",
		CutoffAt:      cutoff,
		CreatedAt:     cutoff + 1,
		TotalBudget:   4_000_000,
		SectionBudget: sectionBudgets,
		Candidates:    first.Candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	included := 0
	for _, entry := range assembled.Manifest {
		if entry.Included {
			included++
		}
	}
	if len(assembled.Manifest) != len(first.Candidates) ||
		included != len(first.Candidates) {
		t.Fatalf(
			"budget assembly dropped source candidates: manifest=%d included=%d candidates=%d",
			len(assembled.Manifest),
			included,
			len(first.Candidates),
		)
	}
}

func addTaskContextSourceSelfEntry(
	t *testing.T,
	store *ActivityStore,
	item SelfModelEntry,
) SelfModelEntry {
	t.Helper()
	persisted, added, err := store.AddSelfModelEntry(item)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatalf("Self entry was not added: %+v", item)
	}
	return persisted
}

func TestGatherTaskContextSourcesUsesAsOfCutoffSelfAuthorityAndOutcomes(
	t *testing.T,
) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	before := taskContextSourceTestTime(9).UnixMilli()
	cutoff := taskContextSourceTestTime(10).UnixMilli()
	after := taskContextSourceTestTime(11).UnixMilli()

	const boundaryKey = "release-boundary"
	oldBoundary := addTaskContextSourceSelfEntry(t, store, SelfModelEntry{
		EntryKey:        boundaryKey,
		Kind:            selfModelKindBoundary,
		Scope:           "release",
		Statement:       "Never publish without passing the release checks.",
		Status:          selfModelStatusActive,
		Confidence:      1,
		ReasoningStatus: "explicit_user_control",
		Evidence: []SelfModelEvidence{{
			SourceKind: "user_correction",
			SourceID:   "correction-1",
			Confidence: 1,
			OccurredAt: before,
		}},
		CreatedAt: before,
	})
	addTaskContextSourceSelfEntry(t, store, SelfModelEntry{
		EntryKey:        boundaryKey,
		Kind:            selfModelKindBoundary,
		Scope:           "release",
		Statement:       "Never publish without passing the release checks.",
		Status:          selfModelStatusRetracted,
		Confidence:      1,
		ReasoningStatus: "explicit_user_control",
		Contradictions: []SelfModelEvidence{{
			SourceKind: "user_correction",
			SourceID:   "correction-2",
			Confidence: 1,
			OccurredAt: after,
		}},
		CreatedAt: after,
	})
	addTaskContextSourceSelfEntry(t, store, SelfModelEntry{
		EntryKey:        "release-preference",
		Kind:            selfModelKindPreference,
		Scope:           "release",
		Statement:       "Run focused tests before broad tests.",
		Status:          selfModelStatusActive,
		Confidence:      0.9,
		ReasoningStatus: "source_backed",
		Evidence: []SelfModelEvidence{{
			SourceKind: "activity",
			SourceID:   "activity-1",
			Confidence: 0.9,
			OccurredAt: before,
		}},
		CreatedAt: before + 1,
	})
	provisional := addTaskContextSourceSelfEntry(t, store, SelfModelEntry{
		EntryKey:        "unverified-preference",
		Kind:            selfModelKindPreference,
		Scope:           "release",
		Statement:       "An unevaluated agent guess.",
		Status:          selfModelStatusProvisional,
		Confidence:      0.6,
		ReasoningStatus: "behavior_derived",
		Evidence: []SelfModelEvidence{{
			SourceKind: "shadow_qualification",
			SourceID:   "shadow-1",
			Confidence: 0.6,
			OccurredAt: before,
		}},
		CreatedAt: before + 2,
	})

	legacyInitiative := Initiative{
		ID:           "approved-initiative",
		Kind:         "automation",
		Status:       InitiativeStatusApproved,
		AutonomyMode: InitiativeModeApproval,
		SourceKind:   "activity",
		SourceID:     "activity-release",
		Title:        "Verify release",
		Description:  "Run release checks",
		Goal:         "A safe release",
		UserIntent:   "Verify before publishing",
		MemoryState:  InitiativeMemoryEligible,
		CreatedAt:    before,
		UpdatedAt:    before,
	}
	if err := store.SaveInitiative(legacyInitiative); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO shadow_capability_qualifications (
			id, qualification_key, policy_version, profile_id,
			profile_evidence_hash, capability_key,
			representative_shadow_run_id, candidate_plan_hash,
			runtime_evidence_hash, risk_tier, status,
			proposal_initiative_id, qualification_json, created_at
		) VALUES (
			'qualification-1', 'qualification-key-1', 'test-v1',
			'profile-1', 'profile-hash', 'release.verify', '',
			'plan-hash', 'runtime-hash', 'local_write', 'qualified',
			?, '{}', ?
		)`,
		legacyInitiative.ID,
		before,
	); err != nil {
		t.Fatal(err)
	}
	manifest := CapabilityManifest{
		ID:              "manifest-1",
		InitiativeID:    legacyInitiative.ID,
		QualificationID: "qualification-1",
		PolicyVersion:   "test-v1",
		PlanHash:        "plan-hash",
		RiskTier:        "local_write",
		Grantable:       true,
		GrantMode:       "scoped_expiring",
		CreatedAt:       before,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO capability_manifests (
			id, initiative_id, qualification_id, policy_version, plan_hash,
			manifest_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		manifest.ID,
		manifest.InitiativeID,
		manifest.QualificationID,
		manifest.PolicyVersion,
		manifest.PlanHash,
		string(manifestJSON),
		before,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO authority_grants (
			id, initiative_id, manifest_id, capability, resource_prefix,
			effect, expires_at, created_at
		) VALUES (
			'grant-1', ?, ?, 'repository.write', '/something/',
			'local_write', ?, ?
		)`,
		legacyInitiative.ID,
		manifest.ID,
		after+10_000,
		before,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO authority_grant_events (
			id, grant_id, event_type, reason, created_at
		) VALUES (
			'grant-event-issued-1', 'grant-1', 'issued',
			'user approved bounded release work', ?
		)`,
		before+50,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO initiative_outcomes (
			id, initiative_id, source_kind, source_id, observed_action,
			observed_result, assessment, assessment_reason, confidence,
			evidence_json, created_at
		) VALUES (
			'outcome-1', ?, 'activity_session', 'session-result',
			'Ran release checks', 'All release checks passed', 'matched',
			'evaluator prose must not enter context', 0.95, '[]', ?
		)`,
		legacyInitiative.ID,
		before+300,
	); err != nil {
		t.Fatal(err)
	}

	result, err := GatherTaskContextSources(TaskContextSourceRequest{
		Store:    store,
		CutoffAt: cutoff,
		Query:    "verify release checks",
	})
	if err != nil {
		t.Fatal(err)
	}
	var (
		foundBoundary, foundPreference bool
		foundAuthority                 bool
		foundObservedOutcome           bool
	)
	for _, candidate := range result.Candidates {
		if candidate.ObservedAt > cutoff {
			t.Fatalf("candidate after cutoff: %+v", candidate)
		}
		switch candidate.SourceKind {
		case taskContextSourceSelfKind:
			switch candidate.SourceID {
			case oldBoundary.ID:
				foundBoundary = candidate.Section == TaskContextSectionBoundaries &&
					candidate.Required &&
					candidate.EvidenceClass == TaskContextEvidenceExplicitUser
			default:
				if strings.Contains(candidate.Content, "focused tests") {
					foundPreference = true
				}
			}
		case taskContextSourceAuthorityKind:
			foundAuthority = candidate.SourceID == "grant-1" &&
				candidate.Required &&
				strings.Contains(
					candidate.Content,
					"Authority status at task start: active",
				)
		case taskContextSourceInitiativeOutcomeKind:
			foundObservedOutcome = strings.Contains(
				candidate.Content,
				"All release checks passed",
			) && !strings.Contains(candidate.Content, "evaluator prose")
		}
	}
	if !foundBoundary || !foundPreference {
		t.Fatalf(
			"typed Self sources missing: boundary=%v preference=%v candidates=%+v",
			foundBoundary,
			foundPreference,
			result.Candidates,
		)
	}
	if !foundAuthority {
		t.Fatalf("authority missing: candidates=%+v", result.Candidates)
	}
	if !foundObservedOutcome {
		t.Fatalf("observed outcome missing: candidates=%+v", result.Candidates)
	}
	provisionalExcluded := false
	for _, exclusion := range result.Exclusions {
		if exclusion.SourceID == provisional.ID &&
			exclusion.Reason == TaskContextSourceReasonUnsupportedAgent {
			provisionalExcluded = true
		}
	}
	if !provisionalExcluded {
		t.Fatalf("provisional Self claim was not excluded: %+v", result.Exclusions)
	}

	budgets := make(map[string]int, len(taskContextSectionOrder))
	for _, section := range taskContextSectionOrder {
		budgets[section] = 100_000
	}
	assembled, err := AssembleTaskContext(TaskContextAssemblyInput{
		EpisodeID:     "episode-source-integration",
		CutoffAt:      cutoff,
		CreatedAt:     cutoff + 1,
		TotalBudget:   500_000,
		SectionBudget: budgets,
		Candidates:    result.Candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Payload.IncludedChars == 0 ||
		len(assembled.Manifest) != len(result.Candidates) {
		t.Fatalf("assembled source context=%+v", assembled)
	}
}

func TestGatherTaskContextSourcesToleratesMissingOptionalSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	db := newTestDB(t)
	result, err := GatherTaskContextSources(TaskContextSourceRequest{
		Store:    &ActivityStore{db: db},
		CutoffAt: taskContextSourceTestTime(10).UnixMilli(),
		Query:    "nothing persisted yet",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 0 || len(result.Exclusions) != 0 {
		t.Fatalf("missing optional sources produced data: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(home, configDirName)); !os.IsNotExist(err) {
		t.Fatalf("gatherer created config storage while reading: err=%v", err)
	}
}
