package main

import (
	"strings"
	"testing"
)

func TestTaskShadowPreOnsetActivityContextUsesExactPreCutoffFrames(
	t *testing.T,
) {
	store := newTestStore(t)
	const sessionID = "task-context-activity"
	session := ActivitySessionRow{
		ID: sessionID, StartTime: 1_000, EndTime: 4_000,
		TriggerKind: "idle_transition", AppNames: `["Mail"]`, EventCount: 3,
	}
	snapshots := []ActivitySnapshotRow{
		{SessionID: sessionID, Timestamp: 1_500, StorageTier: "hot"},
		{SessionID: sessionID, Timestamp: 2_000, StorageTier: "hot"},
		{SessionID: sessionID, Timestamp: 3_000, StorageTier: "hot"},
	}
	if err := store.SaveSessionWithSnapshots(session, snapshots); err != nil {
		t.Fatalf("SaveSessionWithSnapshots: %v", err)
	}
	stored, err := store.GetSessionSnapshots(sessionID)
	if err != nil || len(stored) != 3 {
		t.Fatalf("GetSessionSnapshots: rows=%d err=%v", len(stored), err)
	}
	if err := store.SaveOCRFrame(OCRFrameRow{
		SessionID: sessionID, SegmentID: stored[0].ID,
		Text: "Earlier email source", Confidence: 0.9,
	}); err != nil {
		t.Fatal(err)
	}
	// The middle snapshot intentionally has no OCR. The normal UI lookup could
	// borrow the post-onset row below; immutable onset context must not.
	if err := store.SaveOCRFrame(OCRFrameRow{
		SessionID: sessionID, SegmentID: stored[2].ID,
		Text: "Post-onset result must never leak", Confidence: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	candidates, err := taskShadowPreOnsetActivityContext(
		store,
		sessionGroup{Sessions: []ActivitySessionRow{session}},
		2_500,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates=%+v", candidates)
	}
	candidate := candidates[0]
	if candidate.ObservedAt != 1_500 ||
		candidate.SourceKind != taskContextSourceActivitySnapshotKind ||
		candidate.EvidenceClass != TaskContextEvidenceObservedContext ||
		candidate.PrivacyDecision != TaskContextPrivacyRedact {
		t.Fatalf("candidate=%+v", candidate)
	}
	if !strings.Contains(candidate.Content, "Earlier email source") ||
		strings.Contains(candidate.Content, "Post-onset") {
		t.Fatalf("unsafe context=%q", candidate.Content)
	}
}

func TestTaskShadowPreOnsetActivityContextExcludesProtectedSessions(
	t *testing.T,
) {
	store := newTestStore(t)
	session := ActivitySessionRow{
		ID: "protected-task-context", StartTime: 1_000, EndTime: 2_000,
		TriggerKind: "idle_transition", AppNames: `["Mail"]`, EventCount: 1,
		TriageStatus: "protected",
	}
	if err := store.SaveSessionWithSnapshots(session, []ActivitySnapshotRow{{
		SessionID:   session.ID,
		Timestamp:   1_500,
		StorageTier: "hot",
		AXText:      "Protected personal content",
	}}); err != nil {
		t.Fatal(err)
	}
	candidates, err := taskShadowPreOnsetActivityContext(
		store,
		sessionGroup{Sessions: []ActivitySessionRow{session}},
		1_800,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("protected candidates=%+v", candidates)
	}
}
