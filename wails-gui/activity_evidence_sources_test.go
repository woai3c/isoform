package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func saveEvidenceSnapshotWithoutRefresh(t *testing.T, store *ActivityStore, run ActivityTimelineRunRow, text string) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: run.SessionIDs[0], Timestamp: run.StartMs, App: run.AppName, AXText: text}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineGrowingRunDefersEvidenceCollection(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "first", "Editor", "com.example.editor", 1000, "/tmp/example/first.go", "")
	saveEvidenceSnapshotWithoutRefresh(t, store, run, "~/.sample-agent/activity 42GB")
	for i := int64(2); i <= 4; i++ {
		saveTimelineTestSession(t, store, ActivitySessionRow{ID: fmt.Sprintf("growth-%d", i), StartTime: i * 1000, EndTime: i*1000 + 100, AppNames: `["Editor"]`}, []activitymon.ActivityEvent{{Kind: activitymon.EventKeyboard, Timestamp: i * 1000, AppName: "Editor", AppBundleID: "com.example.editor", TypedText: "continue"}})
	}
	grown, err := store.GetTimelineRunByID(run.ID)
	if err != nil || grown == nil || grown.EventCount <= run.EventCount {
		t.Fatalf("run did not grow: %+v err=%v", grown, err)
	}
	if evidenceTokenExists(grown.EvidenceTokens, "figure", "42 gb") {
		t.Fatal("session save collected late raw snapshots")
	}
	if _, err := store.RefreshTimelineRunEvidence(grown); err != nil {
		t.Fatal(err)
	}
	if !evidenceTokenExists(grown.EvidenceTokens, "figure", "42 gb") {
		t.Fatal("worker failed to collect deferred evidence")
	}
}

func TestTimelineEvidenceRevisionCacheAndLateSourceUpdates(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "source", "iTerm2", "com.googlecode.iterm2", 1000, "/tmp/example/file.go", "")
	outcomeTestSnapshot(t, store, run, "~/.sample-agent/activity 42GB")
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	// An UPDATE-only trigger fails any redundant evidence write. Unchanged
	// collection must return from the metadata cache before reading captures.
	if _, err := store.db.Exec(`CREATE TRIGGER reject_evidence_rewrite BEFORE UPDATE OF evidence_tokens ON activity_timeline_groups BEGIN SELECT RAISE(ABORT,'unchanged evidence rewritten'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatalf("unchanged refresh: %v", err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_evidence_rewrite`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_snapshots SET ax_text='~/.sample-agent/activity 43GB' WHERE session_id='source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	if !evidenceTokenExists(run.EvidenceTokens, "figure", "43 gb") {
		t.Fatal("in-place AX update did not invalidate collection")
	}
	snaps, _ := store.GetSessionSnapshots("source")
	if err := store.SaveOCRFrame(OCRFrameRow{SessionID: "source", SegmentID: snaps[0].ID, Text: "E_SYNC_42: the sync skipped the folder, then retry"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_ocr_frames SET text='E_SYNC_43: the sync skipped another folder' WHERE session_id='source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	if !evidenceTokenExists(run.EvidenceTokens, "error", "E_SYNC_43") {
		t.Fatalf("in-place OCR update missed: %+v", run.EvidenceTokens)
	}
	raw, _ := json.Marshal(run.EvidenceTokens)
	if strings.Contains(string(raw), "sync skipped") {
		t.Fatalf("terminal AI prose persisted: %s", raw)
	}
}

func TestTimelinePrivateExclusionReevaluatesCorrectedSource(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	event := activitymon.ActivityEvent{Kind: activitymon.EventKeyboard, Timestamp: 1000, AppName: "Chrome", AppBundleID: "com.google.Chrome", WindowTitle: "Page - Google Chrome (Incognito)", TypedText: "/tmp/example/document.txt"}
	saveTimelineTestSession(t, store, ActivitySessionRow{ID: "browser", StartTime: 1000, EndTime: 1100, AppNames: `["Chrome"]`}, []activitymon.ActivityEvent{event})
	runs, _ := store.GetLastTimelineRuns(1)
	run := runs[0]
	if allowed, err := store.RefreshTimelineRunEvidence(&run); err != nil || allowed {
		t.Fatalf("private source allowed=%v err=%v", allowed, err)
	}
	event.WindowTitle = "How to use Incognito - Google Chrome"
	if _, err := store.db.Exec(`UPDATE activity_events SET data=? WHERE session_id='browser'`, composeJSON(event)); err != nil {
		t.Fatal(err)
	}
	if allowed, err := store.RefreshTimelineRunEvidence(&run); err != nil || !allowed || run.EvidenceExcluded {
		t.Fatalf("corrected source stayed excluded: %+v err=%v", run, err)
	}
	if !evidenceTokenExists(run.EvidenceTokens, "path", "/tmp/example/document.txt") {
		t.Fatal("ordinary page evidence not restored")
	}
}

func TestTerminalOCRKeepsCodesWithoutAIChatProse(t *testing.T) {
	for _, text := range []string{"Error: the sync skipped the folder, then retry", "E_SYNC_42: this private explanation must not persist"} {
		tokens := extractActivityEvidenceTokens(text, 1000)
		if len(tokens) != 1 || strings.Contains(tokens[0].Value, ":") || strings.Contains(tokens[0].Value, " ") {
			t.Fatalf("terminal OCR retained prose: %+v", tokens)
		}
	}
}
