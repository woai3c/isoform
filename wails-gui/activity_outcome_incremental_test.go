package main

import (
	"testing"

	"wails-gui/internal/platform/activitymon"
)

// Append capture rows while keeping the identity of an already observed run.
// Reconciliation is exercised elsewhere; these fixtures isolate observation.
func appendOutcomeObservationEvent(t *testing.T, store *ActivityStore, run *ActivityTimelineRunRow, at int64, text, click, window string) int64 {
	t.Helper()
	event := activitymon.ActivityEvent{Kind: activitymon.EventKeyboard, Timestamp: at, AppName: run.AppName, AppBundleID: run.AppKey, WindowTitle: window, TypedText: text}
	if click != "" {
		event.Kind = activitymon.EventClick
		event.ClickLabel = click
	}
	result, err := store.db.Exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) VALUES(?,?,?,?,?)`, run.SessionIDs[0], at, event.Kind, composeJSON(event), run.EventCount)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	run.LastEventRowID, run.EndMs = id, max(run.EndMs, at)
	run.EventCount++
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET last_event_row_id=?,end_ms=?,event_count=? WHERE id=?`, run.LastEventRowID, run.EndMs, run.EventCount, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_sessions SET end_time=? WHERE id=?`, run.EndMs, run.SessionIDs[0]); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestActivityOutcomeGrowingRunReadsOnlyUnobservedEvents(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "chat", "Claude", "com.anthropic.claudefordesktop", 1000, "~/.sample-agent/records", "")
	appendOutcomeObservationEvent(t, store, &run, 500000, "", "Continue", "")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	appendOutcomeObservationEvent(t, store, &run, 501000, "~/.sample-agent/records", "", "")
	appendOutcomeObservationEvent(t, store, &run, 502000, "", "Send", "")
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	// A raw read of the old body now fails in SQLite. The bounded observer
	// must read only the new interval and its two-minute commit overlap.
	if _, err := store.db.Exec(`ALTER TABLE activity_events RENAME TO saved_activity_events;
		CREATE VIEW activity_events AS SELECT id,session_id,timestamp,kind,sort_order,
		CASE WHEN timestamp < 380000 THEN json_extract('invalid','$') ELSE data END AS data FROM saved_activity_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.observeActivityRunOutcomeWithEvidence("task", run, run.EndMs); err != nil {
		t.Fatalf("growing run re-read observed event bodies: %v", err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeReported || latest.OccurredAt != 502000 {
		t.Fatalf("bounded message outcome=%+v err=%v", latest, err)
	}
}

func TestActivityOutcomeGrowingNativeRunKeepsBoundsAfterAX(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "editor", "Editor", "com.example.editor", 1000, "~/.sample-agent/records", "")
	appendOutcomeObservationEvent(t, store, &run, 500000, "", "Continue", "")
	outcomeTestSnapshot(t, store, run, "~/.sample-agent/records")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	// A routine native AX capture changes evidence provenance but adds no
	// browser destination or task identifier that could affect old messages.
	outcomeTestSnapshot(t, store, run, "~/.sample-agent/records")
	appendOutcomeObservationEvent(t, store, &run, 501000, "~/.sample-agent/records", "", "")
	appendOutcomeObservationEvent(t, store, &run, 502000, "", "Submit", "")
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`ALTER TABLE activity_events RENAME TO saved_activity_events;
		CREATE VIEW activity_events AS SELECT id,session_id,timestamp,kind,sort_order,
		CASE WHEN timestamp < 380000 THEN json_extract('invalid','$') ELSE data END AS data FROM saved_activity_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.observeActivityRunOutcomeWithEvidence("task", run, run.EndMs); err != nil {
		t.Fatalf("native AX capture reset the observed event cutoff: %v", err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeDone || latest.OccurredAt != 502000 {
		t.Fatalf("bounded native outcome=%+v err=%v", latest, err)
	}
}

func TestActivityOutcomeGrowingRunReplaysLateEventInsert(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "chat", "Claude", "com.anthropic.claudefordesktop", 1000, "~/.sample-agent/records", "")
	appendOutcomeObservationEvent(t, store, &run, 500000, "", "Continue", "")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	appendOutcomeObservationEvent(t, store, &run, 10000, "~/.sample-agent/records", "", "")
	appendOutcomeObservationEvent(t, store, &run, 10100, "", "Send", "")
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.OccurredAt != 10100 || latest.Status != activityOutcomeReported {
		t.Fatalf("late insert outcome=%+v err=%v", latest, err)
	}
}

func TestActivityOutcomeGrowingRunReplaysRewrittenEvent(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "chat", "Claude", "com.anthropic.claudefordesktop", 1000, "~/.sample-agent/records", "")
	draftID := appendOutcomeObservationEvent(t, store, &run, 10000, "Please inspect this", "", "")
	appendOutcomeObservationEvent(t, store, &run, 10100, "", "Send", "")
	appendOutcomeObservationEvent(t, store, &run, 500000, "", "Continue", "")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	if latest, err := store.GetLatestActivityOutcome("task"); err != nil || latest != nil {
		t.Fatalf("unmatched draft outcome=%+v err=%v", latest, err)
	}
	if _, err := store.db.Exec(`UPDATE activity_events SET data=json_set(data,'$.typedText','~/.sample-agent/records') WHERE id=?`, draftID); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.OccurredAt != 10100 || latest.Status != activityOutcomeReported {
		t.Fatalf("rewritten event outcome=%+v err=%v", latest, err)
	}
}

func saveOutcomeBrowserSnapshot(t *testing.T, store *ActivityStore, run ActivityTimelineRunRow, at int64, url string) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: run.SessionIDs[0], Timestamp: at, App: run.AppName, BrowserURL: url}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestActivityOutcomeGrowingBrowserKeepsPriorWindowContext(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "browser", "Chrome", "com.google.Chrome", 100000, "~/.sample-agent/records", "")
	if _, err := store.db.Exec(`UPDATE activity_events SET data=json_set(data,'$.windowTitle','Task conversation') WHERE id=?`, run.FirstEventRowID); err != nil {
		t.Fatal(err)
	}
	appendOutcomeObservationEvent(t, store, &run, 500000, "", "Continue", "Task conversation")
	saveOutcomeBrowserSnapshot(t, store, run, 390000, "https://claude.ai/chat/synthetic")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	appendOutcomeObservationEvent(t, store, &run, 501000, "~/.sample-agent/records", "", "Task conversation")
	appendOutcomeObservationEvent(t, store, &run, 502000, "", "Send", "Task conversation")
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeReported || latest.Target != "Claude" {
		t.Fatalf("bounded browser context outcome=%+v err=%v", latest, err)
	}
}

func TestActivityOutcomeReplaysLateBrowserContext(t *testing.T) {
	for _, change := range []string{"insert", "update"} {
		t.Run(change, func(t *testing.T) {
			store := newActivityEvidenceTestStore(t)
			run := outcomeTestRun(t, store, "browser", "Chrome", "com.google.Chrome", 100000, "~/.sample-agent/records", "")
			appendOutcomeObservationEvent(t, store, &run, 399000, "~/.sample-agent/records", "", "")
			appendOutcomeObservationEvent(t, store, &run, 400000, "", "Send", "")
			appendOutcomeObservationEvent(t, store, &run, 800000, "", "Continue", "")
			if change == "update" {
				saveOutcomeBrowserSnapshot(t, store, run, 390000, "https://example.com/form")
			}
			outcomeTestLink(t, store, "task", run)
			if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
				t.Fatal(err)
			}
			if latest, err := store.GetLatestActivityOutcome("task"); err != nil || latest != nil {
				t.Fatalf("unidentified browser outcome=%+v err=%v", latest, err)
			}
			switch change {
			case "insert":
				saveOutcomeBrowserSnapshot(t, store, run, 390000, "https://claude.ai/chat/synthetic")
			case "update":
				if _, err := store.db.Exec(`UPDATE activity_snapshots SET browser_url='https://claude.ai/chat/synthetic' WHERE session_id=?`, run.SessionIDs[0]); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
				t.Fatal(err)
			}
			latest, err := store.GetLatestActivityOutcome("task")
			if err != nil || latest == nil || latest.OccurredAt != 400000 || latest.Status != activityOutcomeReported {
				t.Fatalf("late browser context outcome=%+v err=%v", latest, err)
			}
		})
	}
}

func TestActivityOutcomeMeasurementCacheMigrationReconciles(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	if _, err := store.db.Exec(`DELETE FROM migration_log WHERE name='activity_outcome_measurement_cache'; DROP TRIGGER activity_event_rewrite_snapshot_UPDATE`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatalf("partial measurement cache migration: %v", err)
	}
	var triggers int
	if err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'activity_event_rewrite_%'`).Scan(&triggers); err != nil || triggers != 5 {
		t.Fatalf("replay triggers=%d err=%v", triggers, err)
	}
}
