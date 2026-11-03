package main

import (
	"strings"
	"testing"
	"time"
)

func purposeTestEvidence(
	label string,
	evidenceClass string,
	stance string,
	observedAt int64,
) PurposeEvidenceInput {
	return PurposeEvidenceInput{
		SourceKind:       "test_source",
		SourceID:         "source-" + label,
		SourceVersion:    "version-" + label,
		SourceObservedAt: observedAt,
		SourceHash:       agencyPayloadHash([]byte("source:" + label)),
		EvidenceClass:    evidenceClass,
		Stance:           stance,
		IndependenceKey:  "independent-" + label,
		ExcerptHash:      agencyPayloadHash([]byte("excerpt:" + label)),
		PrivacyDecision:  TaskContextPrivacyRedact,
	}
}

func purposeTestCreateInput(
	key string,
	kind string,
	label string,
	evidence []PurposeEvidenceInput,
	occurredAt int64,
) CreatePurposeOpenLoopInput {
	return CreatePurposeOpenLoopInput{
		IdempotencyKey:  key,
		CanonicalKey:    "customer / " + label,
		Kind:            kind,
		Title:           "Move " + label + " forward",
		DesiredOutcome:  "The supported outcome for " + label + " is complete.",
		Owner:           PurposeOwnerUser,
		Counterparty:    "Customer",
		Scope:           "Current customer work",
		DueKind:         PurposeDueUnknown,
		ReasoningStatus: PurposeReasoningUnknown,
		Confidence:      0.65,
		OccurredAt:      occurredAt,
		Evidence:        evidence,
	}
}

func purposeTestAppendInput(
	detail PurposeOpenLoopDetail,
	key string,
	eventType string,
	stance string,
	evidenceClass string,
	label string,
	occurredAt int64,
) AppendPurposeOpenLoopEventInput {
	return AppendPurposeOpenLoopEventInput{
		OpenLoopID:      detail.OpenLoop.ID,
		ExpectedEventID: detail.Current.ID,
		IdempotencyKey:  key,
		EventType:       eventType,
		Title:           detail.Current.Title,
		DesiredOutcome:  detail.Current.DesiredOutcome,
		Owner:           detail.Current.Owner,
		Counterparty:    detail.Current.Counterparty,
		Scope:           detail.Current.Scope,
		DueKind:         detail.Current.DueKind,
		DueValue:        detail.Current.DueValue,
		ReasoningStatus: detail.Current.ReasoningStatus,
		Rationale:       detail.Current.Rationale,
		Confidence:      0.78,
		OccurredAt:      occurredAt,
		Evidence: []PurposeEvidenceInput{
			purposeTestEvidence(label, evidenceClass, stance, occurredAt),
		},
	}
}

func purposeLedgerSideEffectCounts(t *testing.T, store *ActivityStore) string {
	t.Helper()
	var counts string
	err := store.db.QueryRow(`
		SELECT printf('%d|%d|%d|%d|%d',
			(SELECT COUNT(*) FROM shadow_runs),
			(SELECT COUNT(*) FROM task_episodes),
			(SELECT COUNT(*) FROM agency_origination_permission_requests_v1),
			(SELECT COUNT(*) FROM self_model_entries),
			(SELECT COUNT(*) FROM background_understanding_materializations_v1)
		)`).Scan(&counts)
	if err != nil {
		t.Fatalf("read Purpose containment counts: %v", err)
	}
	return counts
}

func TestPurposeMapLedgerQualifiesIndependentEvidenceAndRequiresVerifiedResolution(
	t *testing.T,
) {
	activity := newTestStore(t)
	store := NewPurposeMapStore(activity.db)
	base := time.Now().Add(-10 * time.Minute).UnixMilli()
	before := purposeLedgerSideEffectCounts(t, activity)

	createInput := purposeTestCreateInput(
		"purpose-create-1",
		PurposeKindCommitment,
		"customer follow-up",
		[]PurposeEvidenceInput{
			purposeTestEvidence(
				"observed-a",
				PurposeEvidenceObservedUser,
				PurposeStanceSupport,
				base,
			),
		},
		base,
	)
	created, added, err := store.Create(createInput)
	if err != nil {
		t.Fatalf("create Purpose open loop: %v", err)
	}
	if !added {
		t.Fatal("new Purpose root was not added")
	}
	if created.Event.LifecycleState != PurposeStateCandidate ||
		created.Event.SupportState != PurposeSupportHypothesis {
		t.Fatalf("one observed source must remain a hypothesis: %+v", created.Event)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatalf("read Purpose detail: %v", err)
	}
	if detail.Eligible {
		t.Fatal("hypothesis-only Purpose root became Agency eligible")
	}

	replayed, replayAdded, err := store.Create(createInput)
	if err != nil || replayAdded || replayed.Event.ID != created.Event.ID {
		t.Fatalf("Purpose creation is not idempotent: added=%t result=%+v err=%v", replayAdded, replayed, err)
	}
	conflict := createInput
	conflict.Title = "Conflicting retry"
	if _, _, err := store.Create(conflict); err == nil ||
		!strings.Contains(err.Error(), "idempotency conflict") {
		t.Fatalf("conflicting Purpose retry was not rejected: %v", err)
	}

	reinforce := purposeTestAppendInput(
		detail,
		"purpose-reinforce-2",
		PurposeEventReinforced,
		PurposeStanceSupport,
		PurposeEvidenceObservedUser,
		"observed-b",
		base+1_000,
	)
	reinforced, added, err := store.AppendEvent(reinforce)
	if err != nil {
		t.Fatalf("reinforce Purpose root: %v", err)
	}
	if !added || reinforced.Event.LifecycleState != PurposeStateActive ||
		reinforced.Event.SupportState != PurposeSupportSupported {
		t.Fatalf("independent evidence did not activate Purpose root: %+v", reinforced.Event)
	}
	detail, err = store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatalf("read qualified Purpose detail: %v", err)
	}
	if !detail.Eligible {
		t.Fatal("supported active Purpose root is not Agency eligible")
	}

	unverifiedResolution := purposeTestAppendInput(
		detail,
		"purpose-resolve-unverified",
		PurposeEventResolved,
		PurposeStanceResolve,
		PurposeEvidenceObservedUser,
		"observed-resolution",
		base+2_000,
	)
	if _, _, err := store.AppendEvent(unverifiedResolution); err == nil ||
		!strings.Contains(err.Error(), "requires verified or explicit evidence") {
		t.Fatalf("unverified Purpose resolution was not rejected: %v", err)
	}

	verifiedResolution := purposeTestAppendInput(
		detail,
		"purpose-resolve-verified",
		PurposeEventResolved,
		PurposeStanceResolve,
		PurposeEvidenceVerifiedOutcome,
		"verified-outcome",
		base+3_000,
	)
	resolved, added, err := store.AppendEvent(verifiedResolution)
	if err != nil {
		t.Fatalf("resolve Purpose root: %v", err)
	}
	if !added || resolved.Event.LifecycleState != PurposeStateResolved {
		t.Fatalf("verified Purpose outcome did not resolve root: %+v", resolved.Event)
	}
	detail, err = store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatalf("read resolved Purpose detail: %v", err)
	}
	if detail.Eligible {
		t.Fatal("resolved Purpose root remains Agency eligible")
	}
	if len(detail.Events) != 3 || len(detail.Evidence) != 3 {
		t.Fatalf("Purpose history was not preserved: events=%d evidence=%d", len(detail.Events), len(detail.Evidence))
	}
	if after := purposeLedgerSideEffectCounts(t, activity); after != before {
		t.Fatalf("Purpose ledger mutated unrelated systems: before=%s after=%s", before, after)
	}
}

func TestPurposeMapRelationsAreAppendOnlyNonDestructiveAndAcyclic(t *testing.T) {
	activity := newTestStore(t)
	store := NewPurposeMapStore(activity.db)
	base := time.Now().Add(-10 * time.Minute).UnixMilli()
	createExplicit := func(key, label string, offset int64) PurposeOpenLoopDetail {
		t.Helper()
		occurredAt := base + offset
		result, _, err := store.Create(purposeTestCreateInput(
			key,
			PurposeKindResponsibility,
			label,
			[]PurposeEvidenceInput{
				purposeTestEvidence(label, PurposeEvidenceExplicitUser, PurposeStanceSupport, occurredAt),
			},
			occurredAt,
		))
		if err != nil {
			t.Fatalf("create Purpose root %s: %v", label, err)
		}
		detail, err := store.GetDetail(result.OpenLoop.ID)
		if err != nil {
			t.Fatalf("read Purpose root %s: %v", label, err)
		}
		return detail
	}
	canonical := createExplicit("purpose-canonical", "canonical", 0)
	duplicate := createExplicit("purpose-duplicate", "duplicate", 1_000)
	third := createExplicit("purpose-third", "third", 2_000)

	relationInput := CreatePurposeRelationInput{
		IdempotencyKey:  "purpose-duplicate-relation",
		SubjectID:       duplicate.OpenLoop.ID,
		ObjectID:        canonical.OpenLoop.ID,
		RelationType:    PurposeRelationDuplicateOf,
		EvidenceEventID: duplicate.Current.ID,
		OccurredAt:      base + 3_000,
	}
	relation, added, err := store.CreateRelation(relationInput)
	if err != nil || !added {
		t.Fatalf("create Purpose duplicate relation: added=%t err=%v", added, err)
	}
	replayed, replayAdded, err := store.CreateRelation(relationInput)
	if err != nil || replayAdded || replayed.Event.ID != relation.Event.ID {
		t.Fatalf("Purpose relation is not idempotent: added=%t result=%+v err=%v", replayAdded, replayed, err)
	}
	duplicate, err = store.GetDetail(duplicate.OpenLoop.ID)
	if err != nil {
		t.Fatalf("read duplicate Purpose root: %v", err)
	}
	if duplicate.Eligible {
		t.Fatal("active duplicate Purpose root remained eligible")
	}
	if len(duplicate.Relations) != 1 || duplicate.Relations[0].Current.EventType != PurposeRelationEventAsserted {
		t.Fatalf("Purpose duplicate relation is missing: %+v", duplicate.Relations)
	}

	_, _, err = store.CreateRelation(CreatePurposeRelationInput{
		IdempotencyKey:  "purpose-duplicate-ambiguous",
		SubjectID:       duplicate.OpenLoop.ID,
		ObjectID:        third.OpenLoop.ID,
		RelationType:    PurposeRelationDuplicateOf,
		EvidenceEventID: duplicate.Current.ID,
		OccurredAt:      base + 4_000,
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous duplicate") {
		t.Fatalf("ambiguous Purpose canonical relation was not rejected: %v", err)
	}
	_, _, err = store.CreateRelation(CreatePurposeRelationInput{
		IdempotencyKey:  "purpose-duplicate-cycle",
		SubjectID:       canonical.OpenLoop.ID,
		ObjectID:        duplicate.OpenLoop.ID,
		RelationType:    PurposeRelationDuplicateOf,
		EvidenceEventID: canonical.Current.ID,
		OccurredAt:      base + 4_000,
	})
	if err == nil || !strings.Contains(err.Error(), "contains a cycle") {
		t.Fatalf("Purpose duplicate cycle was not rejected: %v", err)
	}

	correctionInput := purposeTestAppendInput(
		duplicate,
		"purpose-duplicate-correction",
		PurposeEventRevised,
		PurposeStanceUpdate,
		PurposeEvidenceCorrection,
		"duplicate-correction",
		base+5_000,
	)
	correctionInput.ReasoningStatus = PurposeReasoningExplicit
	correctionInput.Rationale = "The two outcomes are distinct."
	corrected, _, err := store.AppendEvent(correctionInput)
	if err != nil {
		t.Fatalf("append Purpose duplicate correction: %v", err)
	}
	retracted, added, err := store.RetractRelation(RetractPurposeRelationInput{
		RelationID:      relation.Relation.ID,
		ExpectedEventID: relation.Event.ID,
		IdempotencyKey:  "purpose-duplicate-retract",
		EvidenceEventID: corrected.Event.ID,
		Reason:          "Explicit correction distinguishes the outcomes.",
		OccurredAt:      base + 6_000,
	})
	if err != nil || !added || retracted.Event.EventType != PurposeRelationEventRetracted {
		t.Fatalf("retract Purpose duplicate relation: added=%t result=%+v err=%v", added, retracted, err)
	}
	duplicate, err = store.GetDetail(duplicate.OpenLoop.ID)
	if err != nil {
		t.Fatalf("read relation-retracted Purpose root: %v", err)
	}
	if !duplicate.Eligible {
		t.Fatal("relation-retracted supported Purpose root did not become eligible")
	}
	if len(duplicate.Relations) != 1 || len(duplicate.Relations[0].Events) != 2 {
		t.Fatalf("Purpose relation history was not retained: %+v", duplicate.Relations)
	}
	var relationCount int
	if err := activity.db.QueryRow(`SELECT COUNT(*) FROM purpose_open_loop_relations_v1`).Scan(&relationCount); err != nil {
		t.Fatalf("count Purpose relations: %v", err)
	}
	if relationCount != 1 {
		t.Fatalf("failed Purpose relation attempts leaked rows: %d", relationCount)
	}
}

func TestPurposeMapLedgerRejectsMutationAndCorruption(t *testing.T) {
	activity := newTestStore(t)
	store := NewPurposeMapStore(activity.db)
	base := time.Now().Add(-5 * time.Minute).UnixMilli()
	result, _, err := store.Create(purposeTestCreateInput(
		"purpose-immutable",
		PurposeKindGoal,
		"immutable goal",
		[]PurposeEvidenceInput{
			purposeTestEvidence("immutable", PurposeEvidenceExplicitUser, PurposeStanceSupport, base),
		},
		base,
	))
	if err != nil {
		t.Fatalf("create immutable Purpose root: %v", err)
	}
	if _, err := activity.db.Exec(`UPDATE purpose_open_loops_v1 SET canonical_key = 'changed' WHERE id = ?`, result.OpenLoop.ID); err == nil {
		t.Fatal("Purpose root SQL update was allowed")
	}
	if _, err := activity.db.Exec(`DELETE FROM purpose_open_loop_evidence_v1 WHERE event_id = ?`, result.Event.ID); err == nil {
		t.Fatal("Purpose evidence SQL delete was allowed")
	}

	if _, err := activity.db.Exec(`DROP TRIGGER purpose_open_loop_events_v1_no_update`); err != nil {
		t.Fatalf("drop Purpose test immutability trigger: %v", err)
	}
	if _, err := activity.db.Exec(`UPDATE purpose_open_loop_events_v1 SET payload_hash = 'corrupt' WHERE id = ?`, result.Event.ID); err != nil {
		t.Fatalf("corrupt Purpose event for fail-closed test: %v", err)
	}
	if _, err := store.ListDetailsAll(); err == nil || !strings.Contains(err.Error(), "payload is inconsistent") {
		t.Fatalf("corrupt Purpose event did not fail closed: %v", err)
	}
}

func TestPurposeMapHistoricalProjectionRejectsBackdatedFutureKnowledge(t *testing.T) {
	activity := newTestStore(t)
	store := NewPurposeMapStore(activity.db)
	base := time.Now().Add(-5 * time.Minute).UnixMilli()
	created, _, err := store.Create(purposeTestCreateInput(
		"purpose-historical",
		PurposeKindConcern,
		"historical concern",
		[]PurposeEvidenceInput{
			purposeTestEvidence("historical-a", PurposeEvidenceObservedUser, PurposeStanceSupport, base),
		},
		base,
	))
	if err != nil {
		t.Fatalf("create historical Purpose root: %v", err)
	}
	cutoff := created.Event.CreatedAt
	time.Sleep(2 * time.Millisecond)
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatalf("read historical Purpose root: %v", err)
	}
	_, _, err = store.AppendEvent(purposeTestAppendInput(
		detail,
		"purpose-historical-late-reinforcement",
		PurposeEventReinforced,
		PurposeStanceSupport,
		PurposeEvidenceObservedUser,
		"historical-b",
		base+1_000,
	))
	if err != nil {
		t.Fatalf("append backdated Purpose reinforcement: %v", err)
	}
	atCutoff, err := store.ListDetailsAt(cutoff)
	if err != nil {
		t.Fatalf("read Purpose historical cutoff: %v", err)
	}
	if len(atCutoff) != 1 || len(atCutoff[0].Events) != 1 ||
		atCutoff[0].Current.SupportState != PurposeSupportHypothesis ||
		atCutoff[0].Eligible {
		t.Fatalf("late-created Purpose evidence leaked into historical cutoff: %+v", atCutoff)
	}
	current, err := store.ListDetailsAt(time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("read current Purpose projection: %v", err)
	}
	if len(current) != 1 || current[0].Current.SupportState != PurposeSupportSupported ||
		!current[0].Eligible {
		t.Fatalf("current Purpose projection omitted valid reinforcement: %+v", current)
	}
}

func TestPurposeMapMigrationRequiresTransaction(t *testing.T) {
	if err := migratePurposeMap(nil); err == nil {
		t.Fatal("Purpose Map migration accepted a nil transaction")
	}
}
