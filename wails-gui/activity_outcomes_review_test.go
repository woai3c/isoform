package main

import (
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestActivityOutcomeCommonIdentifiersCannotCompleteOrValidateReceipt(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	path := "~/.sample-agent/settings.json"
	var first ActivityTimelineRunRow
	for i, id := range []string{"one", "two", "three", "four"} {
		app, bundle := "Editor", "com.example.editor"
		if i%2 == 1 {
			app, bundle = "iTerm2", "com.googlecode.iterm2"
		}
		run := outcomeTestRun(t, store, id, app, bundle, int64(1000+i*1000), path, "")
		outcomeTestLink(t, store, id, run)
		if i == 0 {
			first = run
		}
	}
	current := outcomeTestRun(t, store, "submit", "Chrome", "com.google.Chrome", 6000, path, "Submit")
	outcomeTestLink(t, store, "one", current)
	common, err := store.commonComposerEvidence(current.EndMs)
	if err != nil {
		t.Fatal(err)
	}
	if !common[activityEvidenceKey(first.EvidenceTokens[0])] {
		t.Fatalf("fixture path was not common: %+v", common)
	}
	if err := store.ObserveActivityRunOutcome("one", current, current.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("one")
	if err != nil || latest != nil {
		t.Fatalf("common path produced outcome: %+v err=%v", latest, err)
	}
	evidence := []ActivityOutcomeEvidence{{RunID: current.ID, SourceKind: "activity_event", SourceID: "submit", ObservedAt: current.EndMs, Tokens: extractActivityIdentifierTokens(path, current.EndMs)}}
	item := ActivityOutcome{ActivityID: "one", RunID: current.ID, Status: activityOutcomeDone, Reason: "completion_click", OccurredAt: current.EndMs, Evidence: evidence}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := appendActivityOutcomeTx(tx, item, current.EndMs); err == nil {
		tx.Rollback()
		t.Fatal("append bypassed common-token damping")
	}
	tx.Rollback()
	if _, err := store.db.Exec(`INSERT INTO activity_outcomes(id,activity_id,run_id,status,reason,occurred_at,target,evidence,created_at) VALUES('legacy-common','one',?,'done','completion_click',?,'',?,?)`, current.ID, current.EndMs, composeJSON(evidence), current.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err = store.GetLatestActivityOutcome("one")
	if err != nil || latest != nil {
		t.Fatalf("read boundary accepted common-token receipt: %+v err=%v", latest, err)
	}
	before := activityOutcomeObservation{evidence: ActivityOutcomeEvidence{SourceKind: "activity_snapshot", ObservedAt: 1000, Tokens: extractActivityIdentifierTokens(path+" 60GB", 1000)}}
	after := activityOutcomeObservation{evidence: ActivityOutcomeEvidence{SourceKind: "activity_snapshot", ObservedAt: 6000, Tokens: extractActivityIdentifierTokens(path+" 4GB", 6000)}}
	if got := inferActivityChangedTarget("one", current, []activityOutcomeObservation{before}, []activityOutcomeObservation{after}, common); len(got) != 0 {
		t.Fatalf("common path produced fixed: %+v", got)
	}
}

func TestActivityOutcomeTargetNeverPersistsRawClickLabel(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "submit", "Editor", "com.example.editor", 1000, "~/.sample-agent/records", "Submit synthetic.person@example.com")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil {
		t.Fatalf("outcome=%+v err=%v", latest, err)
	}
	if latest.Target != "~/.sample-agent/records" {
		t.Fatalf("target=%q", latest.Target)
	}
	var target string
	if err := store.db.QueryRow(`SELECT target FROM activity_outcomes WHERE id=?`, latest.ID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target, "Submit") || strings.Contains(target, "@") {
		t.Fatalf("raw click label stored: %q", target)
	}
}

func TestActivityOutcomeOCRCannotSettleTask(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	baseline := outcomeTestRun(t, store, "before", "iTerm2", "com.googlecode.iterm2", 1000, "~/.sample-agent/records", "")
	outcomeTestSnapshot(t, store, baseline, "~/.sample-agent/records 60.3 GB")
	outcomeTestLink(t, store, "task", baseline)
	chat := outcomeTestRun(t, store, "chat", "Claude", "com.anthropic.claudefordesktop", 2000, "~/.sample-agent/records", "Send")
	outcomeTestLink(t, store, "task", chat)
	current := outcomeTestRun(t, store, "after", "iTerm2", "com.googlecode.iterm2", 3000, "~/.sample-agent/records", "")
	outcomeTestSnapshot(t, store, current, "~/.sample-agent/records")
	outcomeTestLink(t, store, "task", current)
	snapshots, err := store.GetSessionSnapshots(current.SessionIDs[0])
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%+v err=%v", snapshots, err)
	}
	if err := store.SaveOCRFrame(OCRFrameRow{SessionID: current.SessionIDs[0], SegmentID: snapshots[0].ID, Text: "~/.sample-agent/records 50.3 GB", Confidence: 0.99}); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest != nil {
		t.Fatalf("OCR manufactured fixed: %+v err=%v", latest, err)
	}
	a, _ := store.GetActivityByID("task")
	if !activityStatusResumable(a.Status) {
		t.Fatalf("OCR settled task: %+v", a)
	}
	before := ActivityOutcomeEvidence{RunID: baseline.ID, SourceKind: "activity_snapshot", SourceID: "before", ObservedAt: 1000, Tokens: extractActivityIdentifierTokens("~/.sample-agent/records 60.3 GB", 1000)}
	after := ActivityOutcomeEvidence{RunID: current.ID, SourceKind: "activity_ocr_frame", SourceID: "after", ObservedAt: 3000, Tokens: extractActivityIdentifierTokens("~/.sample-agent/records 50.3 GB", 3000)}
	unsafe := ActivityOutcome{ActivityID: "task", RunID: current.ID, Status: activityOutcomeFixed, Reason: "target_changed", OccurredAt: 3000, Evidence: []ActivityOutcomeEvidence{before, after}}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := appendActivityOutcomeTx(tx, unsafe, 3100); err == nil {
		tx.Rollback()
		t.Fatal("direct OCR fixed accepted")
	}
	tx.Rollback()
	if _, err := store.db.Exec(`INSERT INTO activity_outcomes(id,activity_id,run_id,status,reason,occurred_at,target,evidence,created_at) VALUES('old-ocr','task',?,'fixed','target_changed',3000,'',?,3100)`, current.ID, composeJSON(unsafe.Evidence)); err != nil {
		t.Fatal(err)
	}
	latest, err = store.GetLatestActivityOutcome("task")
	if err != nil || latest != nil {
		t.Fatalf("legacy OCR fixed exposed: %+v err=%v", latest, err)
	}
	outcomeTestSnapshot(t, store, current, "~/.sample-agent/records 4 GB")
	if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err = store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeFixed {
		t.Fatalf("late AX did not invalidate cached observation: %+v err=%v", latest, err)
	}
}

func TestActivityOutcomeObservationCacheSkipsUnchangedRawCapture(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "chat", "Claude", "com.anthropic.claudefordesktop", 1000, "~/.sample-agent/records", "Send")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	var cutoff int64
	if err := store.db.QueryRow(`SELECT source_fingerprint,observed_until_ms FROM activity_outcome_observations WHERE activity_id='task' AND run_id=?`, run.ID).Scan(&fingerprint, &cutoff); err != nil || fingerprint == "" || cutoff != run.EndMs {
		t.Fatalf("cache fingerprint=%q cutoff=%d err=%v", fingerprint, cutoff, err)
	}
	// Renaming the raw table leaves revision counters unchanged. An identical
	// pass can only succeed if it does not try to re-read capture events.
	if _, err := store.db.Exec(`ALTER TABLE activity_events RENAME TO saved_activity_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs+1000); err != nil {
		t.Fatalf("unchanged observation re-read raw capture: %v", err)
	}
}

func TestActivityOutcomeMeasurementGateRequiresPathAndBytes(t *testing.T) {
	for _, text := range []string{"~/.sample-agent/records", "60GB", "~/.sample-agent/records 10ms", "/tmp 60GB"} {
		if activityOutcomeHasMeasurementTokens(extractActivityIdentifierTokens(text, 1000), nil) {
			t.Fatalf("unexpected measurement gate for %q", text)
		}
	}
	if !activityOutcomeHasMeasurementTokens(extractActivityIdentifierTokens("~/.sample-agent/records 60GB", 1000), nil) {
		t.Fatal("specific path and storage figure should pass")
	}
}

func TestActivityOutcomeBrowserChatUsesCapturedHost(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	events := []activitymon.ActivityEvent{{Kind: activitymon.EventKeyboard, Timestamp: 1000, AppName: "Chrome", AppBundleID: "com.google.Chrome", WindowTitle: "Claude", TypedText: "~/.sample-agent/records"}, {Kind: activitymon.EventClick, Timestamp: 1100, AppName: "Chrome", AppBundleID: "com.google.Chrome", WindowTitle: "Claude", ClickLabel: "Send"}}
	saveTimelineTestSession(t, store, ActivitySessionRow{ID: "browser", StartTime: 1000, EndTime: 1100, AppNames: `["Chrome"]`}, events)
	runs, err := store.GetTimelineRuns(1, 0, 1000, 1100)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	run := runs[0]
	outcomeTestLink(t, store, "task", run)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: "browser", Timestamp: 1000, App: "Chrome", BrowserURL: "https://claude.ai/chat/synthetic", AXText: "~/.sample-agent/records"}}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveActivityRunOutcome("task", run, 1100); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeReported || latest.Target != "Claude" {
		t.Fatalf("captured chat=%+v err=%v", latest, err)
	}
}

func TestActivityOutcomeBrowserChatRejectsOtherSnapshotContexts(t *testing.T) {
	for _, tc := range []struct {
		name, app, parsed string
	}{
		{name: "different app", app: "Safari"},
		{name: "different bundle", app: "Chrome", parsed: `{"metadata":{"bundleId":"com.apple.Safari"}}`},
		{name: "different window", app: "Chrome", parsed: `{"title":"Unrelated Claude conversation"}`},
		{name: "unidentified app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newActivityEvidenceTestStore(t)
			events := []activitymon.ActivityEvent{
				{Kind: activitymon.EventKeyboard, Timestamp: 1000, AppName: "Chrome", AppBundleID: "com.google.Chrome", WindowTitle: "Support form - Google Chrome", TypedText: "~/.sample-agent/records"},
				{Kind: activitymon.EventClick, Timestamp: 1100, AppName: "Chrome", AppBundleID: "com.google.Chrome", WindowTitle: "Support form - Google Chrome", ClickLabel: "Send"},
			}
			saveTimelineTestSession(t, store, ActivitySessionRow{ID: "browser", StartTime: 1000, EndTime: 1100, AppNames: `["Chrome"]`}, events)
			runs, err := store.GetTimelineRuns(1, 0, 1000, 1100)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs=%+v err=%v", runs, err)
			}
			run := runs[0]
			outcomeTestLink(t, store, "task", run)
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: "browser", Timestamp: 1000, App: tc.app, BrowserURL: "https://claude.ai/chat/synthetic", ParsedJSON: tc.parsed}}); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := store.ObserveActivityRunOutcome("task", run, 1100); err != nil {
				t.Fatal(err)
			}
			latest, err := store.GetLatestActivityOutcome("task")
			if err != nil || latest != nil {
				t.Fatalf("unrelated snapshot attributed chat send: %+v err=%v", latest, err)
			}
		})
	}
}

func TestActivityOutcomeLatePriorMeasurementInvalidatesObservation(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	baseline := outcomeTestRun(t, store, "before", "iTerm2", "com.googlecode.iterm2", 1000, "~/.sample-agent/records", "")
	outcomeTestLink(t, store, "task", baseline)
	current := outcomeTestRun(t, store, "after", "Editor", "com.example.editor", 3000, "~/.sample-agent/records", "")
	outcomeTestSnapshot(t, store, current, "~/.sample-agent/records 4 GB")
	outcomeTestLink(t, store, "task", current)
	if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest != nil {
		t.Fatalf("missing baseline settled task: %+v err=%v", latest, err)
	}
	// Save late AX without refreshing the prior run, as happens when a worker
	// finishes after that run has left the current lifecycle window.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: baseline.SessionIDs[0], Timestamp: baseline.StartMs, App: baseline.AppName, AXText: "~/.sample-agent/records 60 GB"}}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveActivityRunOutcome("task", current, current.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err = store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeFixed {
		t.Fatalf("late prior AX did not invalidate observation: %+v err=%v", latest, err)
	}
}
