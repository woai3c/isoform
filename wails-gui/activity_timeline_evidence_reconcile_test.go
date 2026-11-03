package main

import (
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestTimelineReconcileSkipsUnchangedAnchorEvidenceCollection(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	first := outcomeTestRun(t, store, "editor", "Editor", "com.example.editor", 1000, "/tmp/example/first.go", "")
	outcomeTestRun(t, store, "terminal", "iTerm2", "com.googlecode.iterm2", 2000, "/tmp/example/second.go", "")
	// Late AX content on the unchanged closed anchor must not be re-read
	// synchronously when a different, still-open app run grows.
	saveEvidenceSnapshotWithoutRefresh(t, store, first, "/tmp/example/first.go 42GB")
	saveTimelineTestSession(t, store, ActivitySessionRow{ID: "terminal-growth", StartTime: 3000, EndTime: 3100, AppNames: `["iTerm2"]`}, []activitymon.ActivityEvent{{Kind: activitymon.EventKeyboard, Timestamp: 3000, AppName: "iTerm2", AppBundleID: "com.googlecode.iterm2", TypedText: "/tmp/example/second.go"}})
	unchanged, err := store.GetTimelineRunByID(first.ID)
	if err != nil || unchanged == nil {
		t.Fatalf("unchanged anchor=%+v err=%v", unchanged, err)
	}
	if evidenceTokenExists(unchanged.EvidenceTokens, "figure", "42 gb") {
		t.Fatalf("unchanged anchor was unnecessarily collected: %+v", unchanged.EvidenceTokens)
	}
	if _, err := store.RefreshTimelineRunEvidence(unchanged); err != nil {
		t.Fatal(err)
	}
	if !evidenceTokenExists(unchanged.EvidenceTokens, "figure", "42 gb") {
		t.Fatalf("explicit refresh lost late AX evidence: %+v", unchanged.EvidenceTokens)
	}
}

func TestTimelineDerivationWritePreservesNewerPrivacyExclusion(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "run", "Editor", "com.example.editor", 1000, "/tmp/example/private.go", "")
	if len(run.EvidenceTokens) == 0 {
		t.Fatal("expected initial evidence")
	}
	stale := run
	// Model the interleaving: reconciliation read an allowed row, while a
	// composer privacy refresh excluded it before the reconcile write.
	run.EvidenceExcluded = true
	run.EvidenceTokens = []ActivityEvidenceToken{}
	if err := persistTimelineRunEvidence(store.db, run); err != nil {
		t.Fatal(err)
	}
	stale.EventCount++
	stale.EndMs++
	if err := updateTimelineRunDerivation(store.db, stale, 2000); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetTimelineRunByID(run.ID)
	if err != nil || saved == nil {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	if !saved.EvidenceExcluded || len(saved.EvidenceTokens) != 0 {
		t.Fatalf("stale derivation revived excluded tokens: %+v", saved)
	}
	if saved.EventCount != stale.EventCount {
		t.Fatalf("derivation did not update: %+v", saved)
	}
	// The legacy collision path delegates to the same sticky write.
	if err := insertTimelineRun(store.db, stale, 3000); err != nil {
		t.Fatal(err)
	}
	saved, err = store.GetTimelineRunByID(run.ID)
	if err != nil || !saved.EvidenceExcluded || len(saved.EvidenceTokens) != 0 {
		t.Fatalf("collision revived excluded tokens: %+v err=%v", saved, err)
	}
}

func TestTimelineRunReadSanitizesLegacyTokensAndExcludedEvidence(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "run", "Editor", "com.example.editor", 1000, "/tmp/example/file.go", "")
	unsafe := []ActivityEvidenceToken{{Kind: "path", Value: "/compact", LastSeenMs: 1000}, {Kind: "error", Value: "Explain why this ordinary sentence was captured", LastSeenMs: 1000}, {Kind: "path", Value: "/tmp/example/file.go", LastSeenMs: 1000}}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens=? WHERE id=?`, composeJSON(unsafe), run.ID); err != nil {
		t.Fatal(err)
	}
	read, err := store.GetTimelineRunByID(run.ID)
	if err != nil || read == nil || len(read.EvidenceTokens) != 1 || read.EvidenceTokens[0].Value != "/tmp/example/file.go" {
		t.Fatalf("unsafe legacy tokens escaped read boundary: %+v err=%v", read, err)
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1 WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	read, err = store.GetTimelineRunByID(run.ID)
	if err != nil || read == nil || len(read.EvidenceTokens) != 0 {
		t.Fatalf("excluded evidence escaped read boundary: %+v err=%v", read, err)
	}
}
