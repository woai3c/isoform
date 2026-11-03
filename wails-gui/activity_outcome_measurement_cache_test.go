package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestActivityOutcomeMeasurementSurvivesGeneralTokenCap(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	path := "~/.sample-agent/records"
	baseline := outcomeTestRun(t, store, "before", "iTerm2", "com.googlecode.iterm2", 1000, path, "Refresh")
	var later strings.Builder
	for i := 0; i < activityEvidenceTokenLimit+10; i++ {
		fmt.Fprintf(&later, "/tmp/measurement-noise/item-%02d.txt\n", i)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{
		{SessionID: "before", Timestamp: 1000, App: baseline.AppName, AXText: path + " 60.3 GB"},
		{SessionID: "before", Timestamp: 1050, App: baseline.AppName, AXText: later.String()},
	}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&baseline); err != nil {
		t.Fatal(err)
	}
	if len(baseline.EvidenceTokens) != activityEvidenceTokenLimit || evidenceTokenExists(baseline.EvidenceTokens, "path", path) || evidenceTokenExists(baseline.EvidenceTokens, "figure", "60.3 gb") {
		t.Fatalf("fixture did not evict the early measurement from general tokens: %+v", baseline.EvidenceTokens)
	}
	measurements, err := store.activityOutcomeMeasurements(baseline)
	if err != nil || len(measurements) != 1 || !evidenceTokenExists(measurements[0].evidence.Tokens, "path", path) || !evidenceTokenExists(measurements[0].evidence.Tokens, "figure", "60.3 gb") {
		t.Fatalf("early AX measurement was lost: %+v, %v", measurements, err)
	}
	outcomeTestLink(t, store, "task", baseline)
	current := outcomeTestRun(t, store, "after", "Editor", "com.example.editor", 3000, path, "")
	outcomeTestSnapshot(t, store, current, path+" 4 GB")
	outcomeTestLink(t, store, "task", current)
	if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeFixed || len(latest.Evidence) != 2 {
		t.Fatalf("capped baseline did not support the observed change: %+v, %v", latest, err)
	}
	if latest.Evidence[0].RunID != baseline.ID || latest.Evidence[1].RunID != current.ID {
		t.Fatalf("measurement provenance changed: %+v", latest.Evidence)
	}
}

func TestActivityOutcomeMeasurementCacheUsesEachMembersFingerprint(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	path := "~/.sample-agent/records"
	baseline := outcomeTestRun(t, store, "before", "iTerm2", "com.googlecode.iterm2", 1000, path, "")
	outcomeTestSnapshot(t, store, baseline, path+" 60 GB")
	outcomeTestLink(t, store, "task", baseline)
	current := outcomeTestRun(t, store, "after", "Editor", "com.example.editor", 3000, path, "")
	outcomeTestSnapshot(t, store, current, path+" 60 GB")
	outcomeTestLink(t, store, "task", current)
	if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
		t.Fatal(err)
	}
	var baselineFingerprint, baselineEvidence string
	if err := store.db.QueryRow(`SELECT source_fingerprint,measurement_evidence FROM activity_run_evidence_state WHERE run_id=?`, baseline.ID).Scan(&baselineFingerprint, &baselineEvidence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_snapshots SET ax_text=? WHERE session_id='after'`, path+" 4 GB"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&current); err != nil {
		t.Fatal(err)
	}
	// Current evidence changed, invalidating the aggregate observation. Keeping
	// raw snapshots unavailable proves that unchanged members use their own
	// measurement cache instead of being re-collected with the current run.
	if _, err := store.db.Exec(`ALTER TABLE activity_snapshots RENAME TO saved_activity_snapshots`); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
		t.Fatalf("changed current run re-read historical snapshots: %v", err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeFixed {
		t.Fatalf("cached baseline did not produce fixed: %+v, %v", latest, err)
	}
	var fingerprint, evidence string
	if err := store.db.QueryRow(`SELECT source_fingerprint,measurement_evidence FROM activity_run_evidence_state WHERE run_id=?`, baseline.ID).Scan(&fingerprint, &evidence); err != nil {
		t.Fatal(err)
	}
	if fingerprint != baselineFingerprint || evidence != baselineEvidence {
		t.Fatal("unchanged historical measurement cache was replaced")
	}
}

func TestActivityOutcomeMeasurementCacheRejectsAndClearsProtectedSource(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "source", "Editor", "com.example.editor", 1000, "~/.sample-agent/records", "")
	outcomeTestSnapshot(t, store, run, "~/.sample-agent/records 60 GB")
	measurements, err := store.activityOutcomeMeasurements(run)
	if err != nil || len(measurements) != 1 {
		t.Fatalf("fixture measurements=%+v, %v", measurements, err)
	}
	if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	measurements, err = store.activityOutcomeMeasurements(run)
	if err != nil || len(measurements) != 0 {
		t.Fatalf("protected cached evidence remained readable: %+v, %v", measurements, err)
	}
	if allowed, err := store.RefreshTimelineRunEvidence(&run); err != nil || allowed {
		t.Fatalf("protected refresh allowed=%v, %v", allowed, err)
	}
	var raw string
	if err := store.db.QueryRow(`SELECT measurement_evidence FROM activity_run_evidence_state WHERE run_id=?`, run.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cached []ActivityOutcomeEvidence
	if err := json.Unmarshal([]byte(raw), &cached); err != nil || len(cached) != 0 {
		t.Fatalf("protected cache retained identifiers: %q, %v", raw, err)
	}
}

func TestActivityOutcomeMeasurementCacheStoresOnlyPairedAXIdentifiers(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "source", "iTerm2", "com.googlecode.iterm2", 1000, "~/.sample-agent/records", "")
	outcomeTestSnapshot(t, store, run, "Current disk use ~/.sample-agent/records 60 GB. Personal note: do not retain these words.")
	snapshots, err := store.GetSessionSnapshots("source")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%+v, %v", snapshots, err)
	}
	if err := store.SaveOCRFrame(OCRFrameRow{SessionID: "source", SegmentID: snapshots[0].ID, Text: "~/.sample-agent/ocr-only 4 GB\nE_SYNC_42: private diagnostic explanation", Confidence: 0.99}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := store.db.QueryRow(`SELECT measurement_evidence FROM activity_run_evidence_state WHERE run_id=?`, run.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Current disk", "Personal note", "retain these words", "ocr-only", "E_SYNC_42", "private diagnostic"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("cache retained %q: %s", forbidden, raw)
		}
	}
	var cached []ActivityOutcomeEvidence
	if err := json.Unmarshal([]byte(raw), &cached); err != nil || len(cached) != 1 {
		t.Fatalf("cached=%+v, %v", cached, err)
	}
	item := cached[0]
	if item.SourceKind != "activity_snapshot" || item.SourceID != fmt.Sprint(snapshots[0].ID) || item.RunID != run.ID || item.ObservedAt != run.StartMs || len(item.Tokens) != 2 || !evidenceTokenExists(item.Tokens, "path", "~/.sample-agent/records") || !evidenceTokenExists(item.Tokens, "figure", "60 gb") {
		t.Fatalf("unexpected cached measurement: %+v", item)
	}
}
