package main

import (
	"fmt"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestActivityOutcomeBrowserPredecessorUsesIndexedAppAndTime(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	const predecessorAt int64 = 1000
	events := []activitymon.ActivityEvent{{Kind: activitymon.EventKeyboard, Timestamp: predecessorAt, AppName: "Chrome", AppBundleID: "com.google.Chrome", WindowTitle: "Task conversation"}}
	for i := 0; i < 1000; i++ {
		events = append(events, activitymon.ActivityEvent{Kind: activitymon.EventAppSwitch, Timestamp: int64(2000 + i), AppName: "Other", AppBundleID: "com.example.other", WindowTitle: "Other app"})
	}
	events = append(events, activitymon.ActivityEvent{Kind: activitymon.EventClick, Timestamp: 501000, AppName: "Chrome", AppBundleID: "com.google.Chrome", WindowTitle: "Task conversation", ClickLabel: "Send"})
	saveTimelineTestSession(t, store, ActivitySessionRow{ID: "browser", StartTime: 1000, EndTime: 501000, AppNames: `["Chrome"]`}, events)
	// A run can span low-signal other-app blips. Neither those 1,000 rows nor
	// older matching rows should be decoded to find one predecessor.
	run := ActivityTimelineRunRow{AppKey: "com.google.Chrome", AppName: "Chrome", StartMs: 1000, EndMs: 501000, SessionIDs: []string{"browser"}}
	if err := store.db.QueryRow(`SELECT MIN(id),MAX(id) FROM activity_events WHERE session_id='browser'`).Scan(&run.FirstEventRowID, &run.LastEventRowID); err != nil {
		t.Fatal(err)
	}
	args := []any{"browser", run.FirstEventRowID, run.LastEventRowID, run.StartMs, int64(500000), run.AppKey}
	plan := strings.Join(explainQueryPlan(t, store.db, activityTimelineRunPredecessorSQL, args...), "\n")
	for _, want := range []string{"USING INDEX idx_activity_events_context", "session_id=?", "<expr>=?", "timestamp>?", "timestamp<?"} {
		if !strings.Contains(plan, want) {
			t.Errorf("browser predecessor does not seek %q: %s", want, plan)
		}
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("browser predecessor sorts historical rows: %s", plan)
	}
	got, err := store.loadTimelineRunEventsBetween(run, 500000, 501000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Timestamp != predecessorAt || got[0].WindowTitle != "Task conversation" || got[1].Timestamp != 501000 {
		t.Fatalf("bounded events lost the latest matching browser context: %+v", got)
	}
	// Negative control proves this plan assertion detects the missing index,
	// rather than merely checking that the query returns the right event.
	if _, err := store.db.Exec(`DROP INDEX idx_activity_events_context`); err != nil {
		t.Fatal(err)
	}
	withoutIndex := strings.Join(explainQueryPlan(t, store.db, activityTimelineRunPredecessorSQL, args...), "\n")
	if !strings.Contains(withoutIndex, "TEMP B-TREE") {
		t.Fatalf("missing-index control did not require a historical sort: %s", withoutIndex)
	}
}

func TestActivityOutcomeEventContextIndexMigrationReconciles(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	if _, err := store.db.Exec(`DROP INDEX idx_activity_events_context; DELETE FROM migration_log WHERE name='activity_event_context_index'`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	if found, err := store.hasIndex("idx_activity_events_context"); err != nil || !found {
		t.Fatalf("context index was not restored: found=%v err=%v", found, err)
	}
}

func TestActivityOutcomeMessageFingerprintIgnoresUnrelatedCommonTokens(t *testing.T) {
	task := extractActivityIdentifierTokens("~/.sample-agent/records", 1000)
	relevant := activityEvidenceKey(task[0])
	unrelated := activityEvidenceKey(ActivityEvidenceToken{Kind: "path", Value: "~/.other-agent/settings.json"})
	before := activityOutcomeMessageContextFingerprint(task, `["run"]`, nil)
	after := activityOutcomeMessageContextFingerprint(task, `["run"]`, map[string]bool{unrelated: true})
	if before != after {
		t.Fatal("unrelated task evidence invalidated the message observation cutoff")
	}
	if got := activityOutcomeMessageContextFingerprint(task, `["run"]`, map[string]bool{relevant: true}); got == before {
		t.Fatal("a newly common task identifier did not invalidate message observation")
	}
	figureTask := extractActivityIdentifierTokens("60 GB", 1000)
	figureBefore := activityOutcomeMessageContextFingerprint(figureTask, `["run"]`, nil)
	alias := activityEvidenceKey(ActivityEvidenceToken{Kind: "figure", Value: "60000 mb"})
	if got := activityOutcomeMessageContextFingerprint(figureTask, `["run"]`, map[string]bool{alias: true}); got == figureBefore {
		t.Fatal("a common matching figure alias did not invalidate message observation")
	}
	unrelatedFigure := activityEvidenceKey(ActivityEvidenceToken{Kind: "figure", Value: "40 gb"})
	if got := activityOutcomeMessageContextFingerprint(figureTask, `["run"]`, map[string]bool{unrelatedFigure: true}); got != figureBefore {
		t.Fatal("an unrelated common figure invalidated message observation")
	}
}

func TestActivityOutcomeUnrelatedCommonTokensKeepEventReadBounded(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "chat", "Claude", "com.anthropic.claudefordesktop", 1000, "~/.sample-agent/records", "")
	appendOutcomeObservationEvent(t, store, &run, 500000, "", "Continue", "")
	outcomeTestLink(t, store, "task", run)
	if err := store.ObserveActivityRunOutcome("task", run, run.EndMs); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("unrelated-%d", i)
		member := testRun(id, "Editor", int64(400000+i*1000), int64(400500+i*1000), 500, 1)
		member.EvidenceTokens = extractActivityIdentifierTokens("~/.other-agent/settings.json", member.EndMs)
		insertComposableTimelineRun(t, store, member, ActivityTimelineGroupSummary{})
		outcomeTestLink(t, store, id, member)
	}
	appendOutcomeObservationEvent(t, store, &run, 501000, "~/.sample-agent/records", "", "")
	appendOutcomeObservationEvent(t, store, &run, 502000, "", "Send", "")
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	common, err := store.commonComposerEvidence(run.EndMs)
	if err != nil || !common[activityEvidenceKey(ActivityEvidenceToken{Kind: "path", Value: "~/.other-agent/settings.json"})] {
		t.Fatalf("unrelated fixture did not change common evidence: %+v, %v", common, err)
	}
	if _, err := store.db.Exec(`ALTER TABLE activity_events RENAME TO saved_activity_events;
		CREATE VIEW activity_events AS SELECT id,session_id,timestamp,kind,sort_order,
		CASE WHEN timestamp < 380000 THEN json_extract('invalid','$') ELSE data END AS data FROM saved_activity_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.observeActivityRunOutcomeWithEvidence("task", run, run.EndMs); err != nil {
		t.Fatalf("unrelated common evidence forced a historical event read: %v", err)
	}
	latest, err := store.GetLatestActivityOutcome("task")
	if err != nil || latest == nil || latest.Status != activityOutcomeReported || latest.OccurredAt != 502000 {
		t.Fatalf("bounded outcome=%+v err=%v", latest, err)
	}
}
