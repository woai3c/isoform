package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

type initiativeAutomationSpec struct {
	name, description, prompt, scheduleKind, rationale, scheduleEvidence string
	scheduleConfig                                                       json.RawMessage
	confidence                                                           float64
}

func (s initiativeAutomationSpec) GetAutomationName() string         { return s.name }
func (s initiativeAutomationSpec) GetAutomationDescription() string  { return s.description }
func (s initiativeAutomationSpec) GetAutomationPrompt() string       { return s.prompt }
func (s initiativeAutomationSpec) GetAutomationScheduleKind() string { return s.scheduleKind }
func (s initiativeAutomationSpec) GetAutomationScheduleConfig() json.RawMessage {
	return s.scheduleConfig
}
func (s initiativeAutomationSpec) GetAutomationScheduleEvidence() string { return s.scheduleEvidence }
func (s initiativeAutomationSpec) GetAutomationRationale() string        { return s.rationale }
func (s initiativeAutomationSpec) GetAutomationConfidence() float64      { return s.confidence }

func newInitiativeFixture(id, sourceID string, status InitiativeStatus) Initiative {
	now := time.Now().UnixMilli()
	return Initiative{
		ID:              id,
		Kind:            "automation",
		Status:          status,
		AutonomyMode:    InitiativeModeShadow,
		SourceKind:      "activity",
		SourceID:        sourceID,
		Title:           "Follow-up watcher",
		Description:     "Draft a follow-up when a commitment stalls.",
		Goal:            "Keep commitments moving.",
		UserIntent:      "following up on open commitments",
		PredictedAction: "Draft a concise follow-up after three business days.",
		Rationale:       "The same workflow occurred repeatedly.",
		Confidence:      0.78,
		RiskLevel:       "low",
		Reversibility:   "reversible",
		Evidence: []InitiativeEvidence{{
			Kind: "source", SourceKind: "activity", SourceID: sourceID, Summary: "Repeated follow-up workflow",
		}},
		ExecutionPlan: InitiativeExecutionPlan{Kind: "automation", Automation: &AutomationExecutionPlan{
			Name: "Follow-up watcher", Prompt: "Draft the follow-up.", ScheduleKind: "manual", ScheduleConfig: json.RawMessage(`{}`),
		}},
		Fingerprint: "fp-" + id,
		MemoryState: InitiativeMemoryUnverified,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestInitiativeRoundTripPreservesEvidenceAndExecutionPlan(t *testing.T) {
	s := newTestStore(t)
	want := newInitiativeFixture("initiative-1", "session-1", InitiativeStatusShadowed)
	if err := s.SaveInitiative(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInitiative(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != InitiativeStatusShadowed || got.MemoryState != InitiativeMemoryUnverified {
		t.Fatalf("unexpected lifecycle state: status=%q memory=%q", got.Status, got.MemoryState)
	}
	if got.ObservationCount != 1 {
		t.Fatalf("observation count=%d, want 1", got.ObservationCount)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].SourceID != "session-1" {
		t.Fatalf("evidence did not round-trip: %+v", got.Evidence)
	}
	if got.ExecutionPlan.Automation == nil || got.ExecutionPlan.Automation.Prompt != "Draft the follow-up." {
		t.Fatalf("execution plan did not round-trip: %+v", got.ExecutionPlan)
	}
}

func TestShadowInitiativeContextIsNotMemoryOrAuthority(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-context", "session-context", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}

	a := &App{activity: &activityMonitorState{store: s}}
	prompt := a.injectInitiativeContext("user prompt")
	for _, want := range []string{"separate from durable memory", "not user instructions or permission to act", "user prompt"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("chat initiative context missing %q:\n%s", want, prompt)
		}
	}
}

func TestLegacyInitiativeBrowserRPCMethodsAreRetired(t *testing.T) {
	for _, method := range []string{"ListInitiatives", "ReviewInitiative", "ApproveInitiative"} {
		if browserRPCAllowlist[method] {
			t.Errorf("%s must not remain a browser product API", method)
		}
	}
}

func TestLegacyPredictionProposalMigratesToInitiative(t *testing.T) {
	s := newTestStore(t)
	createdAt := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := s.db.Exec(`INSERT INTO prediction_proposals (
		id, source_kind, source_id, status, user_intent, predicted_next,
		tool_proposal, app_name, description, features, confidence,
		chat_session_id, dismiss_reason, kind, cron_job_id,
		handoff_prompt, handoff_proposal_id, created_at, resolved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-proposal", "activity", "legacy-source", "accepted", "prepare a daily brief",
		"Prepare the next daily brief", "Summarize fresh activity", "Daily brief",
		"Prepare a fresh daily brief", "[]", 0.75, "legacy-chat", "", "automation",
		"legacy-cron", "", "", createdAt, createdAt,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateInitiatives(tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetInitiative("legacy-proposal")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != InitiativeStatusApproved || got.AutonomyMode != InitiativeModeApproval {
		t.Fatalf("legacy lifecycle = %q/%q", got.Status, got.AutonomyMode)
	}
	if got.ExecutionRef != "legacy-cron" || got.MemoryState != InitiativeMemoryLegacy {
		t.Fatalf("legacy linkage not preserved: %+v", got)
	}
}

func TestCreateShadowInitiativeDoesNotRequireCronStorage(t *testing.T) {
	s := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: s}}
	spec := initiativeAutomationSpec{
		name: "Daily brief", description: "Prepare a fresh daily brief.", prompt: "Prepare today's brief.",
		scheduleKind: "manual", scheduleConfig: json.RawMessage(`{}`),
		rationale: "The user explicitly committed to assembling this brief.", confidence: 0.92,
	}
	result := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "session-shadow", "", "assembling a daily brief")
	if result.Status != "shadowed" || result.InitiativeID == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	got, err := s.GetInitiative(result.InitiativeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExecutionRef != "" {
		t.Fatalf("shadow initiative must not have an execution ref, got %q", got.ExecutionRef)
	}
	if got.Confidence != 0.92 {
		t.Fatalf("confidence=%v, want 0.92", got.Confidence)
	}
	if got.ObservationCount != 1 {
		t.Fatalf("observation count=%d, want 1", got.ObservationCount)
	}
}

func TestRoutineInitiativeStaysObservedUntilIndependentEvidenceAgrees(t *testing.T) {
	s := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: s}}
	spec := initiativeAutomationSpec{
		name: "Daily brief", description: "Prepare a fresh daily brief.", prompt: "Prepare today's brief.",
		scheduleKind: "manual", scheduleConfig: json.RawMessage(`{}`),
		rationale: "The user assembled the same brief again.", confidence: 0.80,
	}

	first := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "session-observation-1", "", "assembling a daily brief")
	if first.Status != "observed" || first.InitiativeID == "" {
		t.Fatalf("first result: %+v", first)
	}
	shadowed, err := s.ListInitiativesByStatus("shadowed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(shadowed) != 0 {
		t.Fatalf("routine first observation must stay hidden: %+v", shadowed)
	}

	second := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "session-observation-2", "", "assembling a daily brief")
	if second.Status != "shadowed" || second.InitiativeID != first.InitiativeID {
		t.Fatalf("second result: %+v, first=%+v", second, first)
	}
	got, err := s.GetInitiative(first.InitiativeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationCount != 2 || got.Status != InitiativeStatusShadowed {
		t.Fatalf("promoted initiative: status=%q observations=%d", got.Status, got.ObservationCount)
	}
	if got.Confidence != 0.80 {
		t.Fatalf("aggregate confidence=%v, want 0.80", got.Confidence)
	}
}

func TestActionableInitiativesExcludeInternalShadowStates(t *testing.T) {
	s := newTestStore(t)
	observed := newInitiativeFixture("initiative-observed", "source-observed", InitiativeStatusObserved)
	shadowed := newInitiativeFixture("initiative-shadowed", "source-shadowed", InitiativeStatusShadowed)
	proposed := newInitiativeFixture("initiative-proposed", "source-proposed", InitiativeStatusProposed)
	proposed.MemoryState = InitiativeMemoryEligible
	unverifiedProposal := newInitiativeFixture("initiative-unverified-proposal", "source-unverified-proposal", InitiativeStatusProposed)

	for _, item := range []Initiative{observed, shadowed, proposed, unverifiedProposal} {
		if err := s.SaveInitiative(item); err != nil {
			t.Fatal(err)
		}
	}

	actionable, err := s.ListInitiativesByStatus("actionable", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(actionable) != 1 || actionable[0].ID != proposed.ID {
		t.Fatalf("actionable initiatives=%+v, want only %q", actionable, proposed.ID)
	}
}

func TestAppReviewInitiativeRejectsInternalShadowPrediction(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-internal-shadow", "source-internal-shadow", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}

	app := &App{activity: &activityMonitorState{store: s}}
	if _, err := app.ReviewInitiative(ReviewInitiativeInput{
		InitiativeID: item.ID,
		Decision:     "correct",
	}); err == nil || !strings.Contains(err.Error(), "not user-reviewable") {
		t.Fatalf("shadow review error=%v, want containment rejection", err)
	}

	unchanged, err := s.GetInitiative(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != InitiativeStatusShadowed || unchanged.MemoryState != InitiativeMemoryUnverified {
		t.Fatalf("shadow review changed internal state: %+v", unchanged)
	}
	if _, err := s.GetLatestInitiativeFeedback(item.ID); err == nil {
		t.Fatal("rejected shadow review must not persist user feedback")
	}
}

func TestLowConfidenceInitiativeRequiresThreeObservations(t *testing.T) {
	s := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: s}}
	spec := initiativeAutomationSpec{
		name: "Weekly cleanup", description: "Clean up stale project notes.", prompt: "Review and clean stale project notes.",
		scheduleKind: "manual", scheduleConfig: json.RawMessage(`{}`),
		rationale: "The workflow may be recurring.", confidence: 0.60,
	}

	for i, sourceID := range []string{"cleanup-1", "cleanup-2", "cleanup-3"} {
		result := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", sourceID, "", "cleaning project notes")
		wantStatus := "observed"
		if i == 2 {
			wantStatus = "shadowed"
		}
		if result.Status != wantStatus {
			t.Fatalf("observation %d status=%q, want %q (%+v)", i+1, result.Status, wantStatus, result)
		}
		got, err := s.GetInitiative(result.InitiativeID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ObservationCount != i+1 {
			t.Fatalf("observation %d count=%d", i+1, got.ObservationCount)
		}
	}
}

func TestDuplicateInitiativeSourceDoesNotIncreaseEvidenceCount(t *testing.T) {
	s := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: s}}
	spec := initiativeAutomationSpec{
		name: "Daily brief", description: "Prepare a fresh daily brief.", prompt: "Prepare today's brief.",
		scheduleKind: "manual", scheduleConfig: json.RawMessage(`{}`),
		rationale: "The workflow may be recurring.", confidence: 0.80,
	}

	first := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "same-source", "", "assembling a daily brief")
	duplicate := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "same-source", "", "assembling a daily brief")
	if first.Status != "observed" || duplicate.Status != "skipped" {
		t.Fatalf("first=%+v duplicate=%+v", first, duplicate)
	}
	got, err := s.GetInitiative(first.InitiativeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ObservationCount != 1 {
		t.Fatalf("duplicate source raised observation count to %d", got.ObservationCount)
	}
}

func TestExplicitScheduleShadowsInitiativeImmediately(t *testing.T) {
	s := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: s}}
	spec := initiativeAutomationSpec{
		name: "Monday brief", description: "Prepare the Monday brief.", prompt: "Prepare the Monday brief.",
		scheduleKind: "cron", scheduleConfig: json.RawMessage(`{"expr":"0 9 * * 1","tz":"UTC"}`),
		scheduleEvidence: "Prepare this every Monday at 9am.",
		rationale:        "The user stated the schedule explicitly.",
		confidence:       0.60,
	}
	result := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "scheduled-source", "", "preparing Monday briefs")
	if result.Status != "shadowed" {
		t.Fatalf("explicit schedule result: %+v", result)
	}
}

func TestLaterObservationKeepsSourceLevelMemoryProvenance(t *testing.T) {
	s := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: s}}
	spec := initiativeAutomationSpec{
		name: "Daily brief", description: "Prepare a fresh daily brief.", prompt: "Prepare today's brief.",
		scheduleKind: "manual", scheduleConfig: json.RawMessage(`{}`),
		rationale: "The user assembled the same brief again.", confidence: 0.80,
	}
	first := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "memory-source-1", "", "assembling a daily brief")
	second := app.createShadowInitiativeFromTriage(context.Background(), spec, "activity", "memory-source-2", "", "assembling a daily brief")
	if first.InitiativeID == "" || second.InitiativeID != first.InitiativeID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}

	bySecondSource, err := s.GetInitiativeBySource("activity", "memory-source-2")
	if err != nil {
		t.Fatal(err)
	}
	if bySecondSource.ID != first.InitiativeID {
		t.Fatalf("second source resolved to %q, want %q", bySecondSource.ID, first.InitiativeID)
	}
	if err := s.LinkInitiativeArtifactBySource("activity", "memory-source-2", "memory", "memory-from-second-source", "shared_evidence"); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM initiative_artifact_links
		WHERE initiative_id = ? AND artifact_id = ?`,
		first.InitiativeID, "memory-from-second-source").Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Fatalf("memory artifact links=%d, want 1", links)
	}
}

func TestReviewInitiativeSeparatesFeedbackFromExecution(t *testing.T) {
	tests := []struct {
		name       string
		decision   string
		correction string
		wantStatus InitiativeStatus
		wantMemory InitiativeMemoryState
	}{
		{name: "correct", decision: "correct", wantStatus: InitiativeStatusProposed, wantMemory: InitiativeMemoryEligible},
		{name: "edit", decision: "edit", correction: "Draft after five business days.", wantStatus: InitiativeStatusProposed, wantMemory: InitiativeMemoryEligible},
		{name: "wrong", decision: "wrong", wantStatus: InitiativeStatusDismissed, wantMemory: InitiativeMemoryBlocked},
		{name: "not now", decision: "not_now", wantStatus: InitiativeStatusDeferred, wantMemory: InitiativeMemoryUnverified},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			item := newInitiativeFixture("initiative-"+tc.decision, "session-"+tc.decision, InitiativeStatusShadowed)
			if err := s.SaveInitiative(item); err != nil {
				t.Fatal(err)
			}
			got, err := s.ReviewInitiative(ReviewInitiativeInput{
				InitiativeID: item.ID, Decision: tc.decision, CorrectedAction: tc.correction, Reason: "test feedback",
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.wantStatus || got.MemoryState != tc.wantMemory {
				t.Fatalf("status=%q memory=%q, want %q/%q", got.Status, got.MemoryState, tc.wantStatus, tc.wantMemory)
			}
			if got.ExecutionRef != "" {
				t.Fatalf("review must not execute initiative, executionRef=%q", got.ExecutionRef)
			}
			if got.LatestFeedback == nil || got.LatestFeedback.Decision != tc.decision {
				t.Fatalf("feedback missing: %+v", got.LatestFeedback)
			}
			if tc.decision == "edit" {
				if got.PredictedAction != tc.correction {
					t.Fatalf("predicted action=%q, want correction %q", got.PredictedAction, tc.correction)
				}
				if got.ExecutionPlan.Automation == nil || got.ExecutionPlan.Automation.Prompt != tc.correction {
					t.Fatalf("automation plan was not corrected: %+v", got.ExecutionPlan)
				}
			}
		})
	}
}

func TestReviewInitiativeEditRequiresCorrection(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-edit", "session-edit", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitiative(ReviewInitiativeInput{InitiativeID: item.ID, Decision: "edit"}); err == nil {
		t.Fatal("expected edit without corrected action to fail")
	}
}

func TestProposedInitiativeCanBeDeferredBeforeApproval(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-reviewed-once", "session-reviewed-once", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitiative(ReviewInitiativeInput{InitiativeID: item.ID, Decision: "correct"}); err != nil {
		t.Fatal(err)
	}
	deferred, err := s.ReviewInitiative(ReviewInitiativeInput{InitiativeID: item.ID, Decision: "not_now"})
	if err != nil {
		t.Fatal(err)
	}
	if deferred.Status != InitiativeStatusDeferred || deferred.ExecutionRef != "" {
		t.Fatalf("deferred proposal: %+v", deferred)
	}
	feedback, err := s.GetLatestInitiativeFeedback(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if feedback.Decision != "not_now" {
		t.Fatalf("feedback decision = %q, want not_now", feedback.Decision)
	}
}

func TestBlockedInitiativeIsNotAnOpenDedupCandidate(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-blocked", "session-blocked", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitiative(ReviewInitiativeInput{InitiativeID: item.ID, Decision: "wrong"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindOpenInitiativeByFingerprint(item.Fingerprint); err == nil {
		t.Fatal("blocked initiative must not remain an open dedup candidate")
	}
	blocked, err := s.ListRecentlyBlockedInitiatives(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0].ID != item.ID {
		t.Fatalf("unexpected blocked initiatives: %+v", blocked)
	}
}

func TestProposedInitiativeKeepsCorrectedJudgment(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-reviewed", "session-reviewed", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitiative(ReviewInitiativeInput{
		InitiativeID: item.ID, Decision: "edit", CorrectedAction: "Wait five days, then draft a follow-up.",
	}); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListRecentlyReviewedInitiatives(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].LatestFeedback == nil {
		t.Fatalf("expected one reviewed initiative with feedback, got %#v", items)
	}
	if got := items[0].LatestFeedback.CorrectedAction; got != "Wait five days, then draft a follow-up." {
		t.Fatalf("corrected action = %q", got)
	}
}

func TestApproveInitiativeRequiresSeparateAuthorityStepAndIsIdempotent(t *testing.T) {
	setTestHome(t)
	activityStore := newTestStore(t)
	stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()

	scheduler := NewCronScheduler(
		stateStore,
		func(string, any) {},
		func(string, string) {},
		nil,
	)
	app := &App{
		store:         stateStore,
		cronScheduler: scheduler,
		activity:      &activityMonitorState{store: activityStore},
	}
	item := newInitiativeFixture("initiative-approval", "session-approval", InitiativeStatusProposed)
	item.MemoryState = InitiativeMemoryEligible
	if err := activityStore.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}

	jobs, err := stateStore.ListCronJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("judgment confirmation created %d automations", len(jobs))
	}

	approved, err := app.ApproveInitiative(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != InitiativeStatusApproved || approved.ExecutionRef == "" {
		t.Fatalf("approved initiative: %+v", approved)
	}
	jobs, err = stateStore.ListCronJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != approved.ExecutionRef {
		t.Fatalf("compiled automations=%+v, initiative=%+v", jobs, approved)
	}
	if jobs[0].ScheduleKind != "manual" || jobs[0].SourcePlatform != "activity" {
		t.Fatalf("compiled automation lost plan provenance: %+v", jobs[0])
	}

	retried, err := app.ApproveInitiative(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ExecutionRef != approved.ExecutionRef {
		t.Fatalf("approval retry execution ref=%q, want %q", retried.ExecutionRef, approved.ExecutionRef)
	}
	jobs, err = stateStore.ListCronJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("approval retry created %d automations", len(jobs))
	}
}

func TestRejectedInitiativePreservesArtifactProvenance(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-rejected", "session-rejected", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkInitiativeArtifactBySource(item.SourceKind, item.SourceID, "memory", "memory-1", "shared_evidence"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitiative(ReviewInitiativeInput{
		InitiativeID: item.ID, Decision: "wrong", Reason: "This was a one-off task.",
	}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM initiative_artifact_links WHERE initiative_id = ?`, item.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("artifact links after rejecting initiative = %d, want 1", count)
	}
	blocked, err := s.ListRecentlyBlockedInitiatives(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0].LatestFeedback == nil || blocked[0].LatestFeedback.Reason == "" {
		t.Fatalf("expected rejected correction to remain available, got %#v", blocked)
	}
}

func TestAutomationDedupUsesReviewedAndRejectedJudgments(t *testing.T) {
	s := newTestStore(t)
	reviewed := newInitiativeFixture("initiative-reviewed-prompt", "session-reviewed-prompt", InitiativeStatusShadowed)
	if err := s.SaveInitiative(reviewed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitiative(ReviewInitiativeInput{
		InitiativeID: reviewed.ID, Decision: "edit", CorrectedAction: "Draft a follow-up after five days.",
	}); err != nil {
		t.Fatal(err)
	}
	rejected := newInitiativeFixture("initiative-rejected-prompt", "session-rejected-prompt", InitiativeStatusShadowed)
	rejected.Fingerprint = "different-fingerprint"
	if err := s.SaveInitiative(rejected); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitiative(ReviewInitiativeInput{
		InitiativeID: rejected.ID, Decision: "wrong", Reason: "This was a one-off task.",
	}); err != nil {
		t.Fatal(err)
	}

	a := &App{activity: &activityMonitorState{store: s}}
	prompt := a.buildAutomationDedupSection("")
	for _, want := range []string{
		"guidance, never authority",
		"Draft a follow-up after five days.",
		"Rejected initiative inferences",
		"This was a one-off task.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("dedup prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestInitiativeArtifactLinkUsesSharedSourceEvidence(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-link", "session-link", InitiativeStatusShadowed)
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkInitiativeArtifactBySource("activity", "session-link", "memory", "memory-1", "shared_evidence"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM initiative_artifact_links WHERE initiative_id = ? AND artifact_kind = 'memory' AND artifact_id = 'memory-1'`, item.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("artifact link count=%d, want 1", count)
	}
}

func TestRecoverInterruptedInitiativesReturnsToApproved(t *testing.T) {
	s := newTestStore(t)
	item := newInitiativeFixture("initiative-executing", "session-executing", InitiativeStatusExecuting)
	item.UpdatedAt = time.Now().Add(-2 * time.Minute).UnixMilli()
	if err := s.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterruptedInitiatives(time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInitiative(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != InitiativeStatusApproved {
		t.Fatalf("status=%q, want approved", got.Status)
	}
}

func TestUpdateHandoffStatus(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveSession(ActivitySessionRow{
		ID: "sess-1", StartTime: time.Now().UnixMilli(), EndTime: time.Now().UnixMilli(),
		TriggerKind: "test", AppNames: "[]", EventCount: 1,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHandoffStatus("sess-1", "created"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSessionByID("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.HandoffStatus != "created" {
		t.Fatalf("handoff_status=%q, want created", got.HandoffStatus)
	}
}
