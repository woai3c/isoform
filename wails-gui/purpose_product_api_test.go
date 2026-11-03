package main

import (
	"testing"
	"time"
)

func TestPurposeProductSnapshotListsCurrentPurposes(t *testing.T) {
	rig := newHandoffTestRig(t)
	base := time.Now().Add(-time.Minute).UnixMilli()
	purposeStore, err := rig.app.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	created, added, err := purposeStore.Create(purposeTestCreateInput(
		"purpose-product:snapshot", PurposeKindGoal, "snapshot",
		[]PurposeEvidenceInput{purposeTestEvidence(
			"purpose-product-snapshot", PurposeEvidenceExplicitUser,
			PurposeStanceSupport, base,
		)}, base,
	))
	if err != nil || !added {
		t.Fatalf("create purpose: added=%v err=%v", added, err)
	}
	details, err := purposeStore.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := agencyPurposeSnapshot(details)
	if snapshot.Counts.Total != 1 || snapshot.Counts.Current != 1 ||
		len(snapshot.Current) != 1 ||
		snapshot.Current[0].ID != created.OpenLoop.ID {
		t.Fatalf("Purpose snapshot=%+v", snapshot)
	}
}

// userPurposeForTest stores a purpose the way a user statement did: full
// confidence and one explicit-user evidence item.
func userPurposeForTest(
	t *testing.T,
	store *PurposeMapStore,
	key, kind, title, desiredOutcome, dueKind, dueValue string,
	occurredAt int64,
) PurposeOpenLoopMutationResult {
	t.Helper()
	input := purposeTestCreateInput(
		key, kind, key,
		[]PurposeEvidenceInput{purposeTestEvidence(
			key, PurposeEvidenceExplicitUser, PurposeStanceSupport, occurredAt,
		)}, occurredAt,
	)
	input.Title = title
	input.DesiredOutcome = desiredOutcome
	input.Counterparty = ""
	input.Scope = ""
	input.DueKind = dueKind
	input.DueValue = dueValue
	input.Confidence = 1
	result, added, err := store.Create(input)
	if err != nil || !added {
		t.Fatalf("create purpose %s: added=%v err=%v", key, added, err)
	}
	return result
}
