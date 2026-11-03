package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func cachedOutcomeMeasurementEvidence(t *testing.T, store *ActivityStore, runID string) []ActivityOutcomeEvidence {
	t.Helper()
	var raw string
	if err := store.db.QueryRow(`SELECT measurement_evidence FROM activity_run_evidence_state WHERE run_id=?`, runID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var evidence []ActivityOutcomeEvidence
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func saveOutcomeMeasurementSeries(t *testing.T, store *ActivityStore, run *ActivityTimelineRunRow, text func(int) string) []ActivitySnapshotRow {
	t.Helper()
	appendOutcomeObservationEvent(t, store, run, run.StartMs+999, "", "Refresh", "")
	snapshots := make([]ActivitySnapshotRow, 1000)
	for i := range snapshots {
		snapshots[i] = ActivitySnapshotRow{SessionID: run.SessionIDs[0], Timestamp: run.StartMs + int64(i), App: run.AppName, AXText: text(i)}
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := store.SaveSnapshots(tx, snapshots); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(run); err != nil {
		t.Fatal(err)
	}
	snapshots, err = store.GetSessionSnapshots(run.SessionIDs[0])
	if err != nil || len(snapshots) != 1000 {
		t.Fatalf("snapshots=%d err=%v", len(snapshots), err)
	}
	return snapshots
}

func TestActivityOutcomeMeasurementRepeatedCapturesKeepFirstAndLast(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "repeated", "Editor", "com.example.editor", 1000, "~/.sample-agent/records", "")
	snapshots := saveOutcomeMeasurementSeries(t, store, &run, func(int) string {
		// Repeated AX lines within a capture must not create more evidence.
		return "~/.sample-agent/records 60 GB\n~/.sample-agent/records 60 GB"
	})
	evidence := cachedOutcomeMeasurementEvidence(t, store, run.ID)
	if len(evidence) != 2 {
		t.Fatalf("1000 identical captures stored %d measurements, want first and last only", len(evidence))
	}
	want := map[string]int64{
		fmt.Sprint(snapshots[0].ID):   snapshots[0].Timestamp,
		fmt.Sprint(snapshots[999].ID): snapshots[999].Timestamp,
	}
	for _, item := range evidence {
		at, exists := want[item.SourceID]
		if !exists || item.ObservedAt != at || !evidenceTokenExists(item.Tokens, "path", "~/.sample-agent/records") || !evidenceTokenExists(item.Tokens, "figure", "60 gb") {
			t.Fatalf("lost first/last measurement provenance: %+v", evidence)
		}
		delete(want, item.SourceID)
	}
	if len(want) != 0 {
		t.Fatalf("missing boundary sources: %+v", want)
	}
}

func TestActivityOutcomeMeasurementDistinctPairsStayWithinTokenLimit(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "distinct", "Editor", "com.example.editor", 1000, "~/.sample-agent/records", "")
	snapshots := saveOutcomeMeasurementSeries(t, store, &run, func(i int) string {
		return fmt.Sprintf("~/.sample-agent/records %d GB", i+1)
	})
	evidence := cachedOutcomeMeasurementEvidence(t, store, run.ID)
	if len(evidence) != activityEvidenceTokenLimit {
		t.Fatalf("1000 distinct pairs stored %d measurements, want %d", len(evidence), activityEvidenceTokenLimit)
	}
	foundLatest := false
	for _, item := range evidence {
		if item.SourceID == fmt.Sprint(snapshots[999].ID) {
			foundLatest = true
		}
	}
	if !foundLatest {
		t.Fatal("bounded measurements dropped the newest observed pair")
	}
	observations, err := store.activityOutcomeMeasurements(run)
	if err != nil || len(observations) != activityEvidenceTokenLimit {
		t.Fatalf("decoded observations=%d err=%v", len(observations), err)
	}
}

func TestActivityOutcomeMeasurementSkipsMembersWithoutStrongPath(t *testing.T) {
	for _, text := range []string{"75%", "/tmp 60 GB", "workspace/records 60 GB"} {
		t.Run(text, func(t *testing.T) {
			store := newActivityEvidenceTestStore(t)
			baseline := outcomeTestRun(t, store, "weak", "iTerm2", "com.googlecode.iterm2", 1000, text, "")
			outcomeTestLink(t, store, "task", baseline)
			current := outcomeTestRun(t, store, "current", "Editor", "com.example.editor", 3000, "~/.sample-agent/records", "")
			outcomeTestSnapshot(t, store, current, "~/.sample-agent/records 4 GB")
			outcomeTestLink(t, store, "task", current)
			// A malformed cache makes an unnecessary measurement read visible;
			// this member has no path that could support a fixed receipt.
			if _, err := store.db.Exec(`UPDATE activity_run_evidence_state SET measurement_evidence='invalid' WHERE run_id=?`, baseline.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
				t.Fatalf("read measurement cache for member without strong path: %v", err)
			}
		})
	}
}
