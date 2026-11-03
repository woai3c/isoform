package main

import (
	"fmt"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func composeCorrectionFixture(t *testing.T) (*ActivityStore, *ActivityTriager, ActivityTimelineRunRow, ActivityTimelineRunRow) {
	t.Helper()
	store := newActivityEvidenceTestStore(t)
	tr := &ActivityTriager{}
	first := testRun("correction-terminal", "Terminal", 1000, 11000, 10000, 2)
	second := testRun("correction-chat", "Claude", 20000, 30000, 10000, 2)
	for i, run := range []ActivityTimelineRunRow{first, second} {
		insertTestRun(t, store, run.ID, int64(i+1), run.StartMs, "done", 2)
		tokens := []ActivityEvidenceToken{{Kind: "path", Value: "~/.sample-agent/records", LastSeenMs: run.EndMs}}
		if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET app_key=?,app_name=?,end_ms=?,active_ms=?,evidence_tokens=? WHERE id=?`, run.AppKey, run.AppName, run.EndMs, run.ActiveMs, composeJSON(tokens), run.ID); err != nil {
			t.Fatal(err)
		}
		run.EvidenceTokens = tokens
		response := composerResponse{Route: "new", Bucket: composerBucketUpdate{Title: "Inspect sample records", Status: "active"}}
		if i > 0 {
			response.Route, response.JoinBucketID = "join", "act_"+first.ID
		}
		if _, err := tr.composeApply(store, run, ActivityTimelineGroupSummary{}, response, run.EndMs); err != nil {
			t.Fatal(err)
		}
	}
	first.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/.sample-agent/records", LastSeenMs: first.EndMs}}
	second.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/.sample-agent/records", LastSeenMs: second.EndMs}}
	return store, tr, first, second
}

func TestActivityCorrectionUnlinksAndPreventsRejoin(t *testing.T) {
	store, tr, first, second := composeCorrectionFixture(t)
	activityID := "act_" + first.ID
	reason, err := store.activityRunLinkReason(activityID, second.ID)
	if err != nil || reason == nil || reason.ReasonKind != "shared_path" || reason.Token != "~/.sample-agent/records" {
		t.Fatalf("link explanation = %+v, %v", reason, err)
	}
	app := &App{activity: &activityMonitorState{store: store, triager: tr}}
	if err := app.UnlinkActivityRun(activityID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.UnlinkActivityRun(activityID, second.ID); err != nil {
		t.Fatalf("retry must be idempotent: %v", err)
	}
	activity, _ := store.GetActivityByID(activityID)
	if activity.RunIDs != composeJSON([]string{first.ID}) || activity.EndMs != first.EndMs || activity.ActiveMsByApp != `{"Terminal":10000}` {
		t.Fatalf("correction did not repair membership/accounting: %+v", activity)
	}
	if activity.Title != "Terminal" || activity.Description != "" || activity.LatestChange != "" || activity.Entities != "[]" {
		t.Fatalf("rejected run left stale composed identity: %+v", activity)
	}
	remainingLink, err := store.activityRunLinkReason(activityID, first.ID)
	if err != nil || remainingLink == nil || remainingLink.ReasonKind != "task_started" || remainingLink.Token != "" {
		t.Fatalf("remaining link claimed a removed basis: %+v, %v", remainingLink, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_run_corrections WHERE activity_id=? AND run_id=? AND reason='not_related'`, activityID, second.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("correction receipt = %d, %v", count, err)
	}
	if ready, err := store.GetComposerReadyRuns(20, 0, 0); err != nil || len(ready) != 0 {
		t.Fatalf("unchanged detached run must not immediately recompose: %+v, %v", ready, err)
	}
	// New evidence may make the run ready, but neither candidate retrieval nor
	// a stale model reply can restore its rejected assignment.
	if candidates, err := tr.activityAssignmentCandidates(store, second, ActivityTimelineGroupSummary{}); err != nil || len(candidates) != 0 {
		t.Fatalf("rejected task remained a candidate: %+v, %v", candidates, err)
	}
	if _, err := tr.composeApply(store, second, ActivityTimelineGroupSummary{}, composerResponse{Route: "join", JoinBucketID: activityID}, second.EndMs); err != nil {
		t.Fatal(err)
	}
	if err := tr.mergeBuckets(store, "act_"+second.ID, activityID, second.EndMs); err == nil {
		t.Fatal("merge restored a user-rejected link")
	}
	activity, _ = store.GetActivityByID(activityID)
	if composeContains(composeParseStrings(activity.RunIDs), second.ID) {
		t.Fatal("stale reply restored correction")
	}
}

func TestActivityCorrectionOfStartingRunKeepsOtherRuns(t *testing.T) {
	store, tr, first, second := composeCorrectionFixture(t)
	activityID := "act_" + first.ID
	if err := store.UnlinkActivityRun(activityID, first.ID, second.EndMs); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.composeApply(store, first, ActivityTimelineGroupSummary{}, composerResponse{Route: "new"}, second.EndMs); err != nil {
		t.Fatalf("recomposing corrected starting run: %v", err)
	}
	activity, _ := store.GetActivityByID(activityID)
	if activity.RunIDs != composeJSON([]string{second.ID}) {
		t.Fatalf("original task overwritten: %+v", activity)
	}
}

func TestActivityCorrectionRejectsWrongMembershipAndKeepsEmptyTask(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	activityID := "act_" + first.ID
	if err := store.UnlinkActivityRun("wrong", first.ID, second.EndMs); err == nil {
		t.Fatal("unrelated activity accepted correction")
	}
	for _, run := range []ActivityTimelineRunRow{first, second} {
		if err := store.UnlinkActivityRun(activityID, run.ID, second.EndMs); err != nil {
			t.Fatal(err)
		}
		if preserved, err := store.GetTimelineRunByID(run.ID); err != nil || preserved == nil {
			t.Fatalf("correction removed source run: %v", err)
		}
	}
	activity, err := store.GetActivityByID(activityID)
	if err != nil || activity == nil || activity.RunIDs != "[]" || activity.ActiveMsByApp != "{}" || activity.Status != activityStatusPaused {
		t.Fatalf("empty task was deleted or kept stale evidence: %+v, %v", activity, err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	views, err := app.GetActivitiesInRange(0, second.EndMs)
	if err != nil || len(views) != 1 || views[0].RunCount != 0 || views[0].ActiveMs != 0 || views[0].LatestLink != nil || views[0].LatestOutcome != nil || views[0].InProgress {
		t.Fatalf("empty task not retained in the view: %+v, %v", views, err)
	}
}

func TestActivityInProgressRequiresOpenRecentEvidence(t *testing.T) {
	const now = int64(100 * time.Hour / time.Millisecond)
	for _, tc := range []struct {
		name, status string
		end          int64
		want         bool
	}{
		{"recent", activityStatusActive, now - 1000, true},
		{"boundary", activityStatusIdle, now - activityIdleAfterMs, true},
		{"old", activityStatusActive, now - activityIdleAfterMs - 1, false},
		{"paused", activityStatusPaused, now - activityPauseAfterMs, false},
		{"done", activityStatusSettled, now, false},
		{"abandoned", activityStatusAbandoned, now, false},
		{"future", activityStatusActive, now + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := activityInProgress(tc.status, tc.end, now); got != tc.want {
				t.Fatalf("in progress=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestActivityComposerRefreshPreservesLinkAndTime(t *testing.T) {
	store, tr, first, second := composeCorrectionFixture(t)
	second.ActiveMs += 1000
	if _, err := tr.composeApply(store, second, ActivityTimelineGroupSummary{}, composerResponse{Route: "new"}, second.EndMs); err != nil {
		t.Fatal(err)
	}
	activity, _ := store.GetActivityByID("act_" + first.ID)
	if activity.ActiveMsByApp != `{"Claude":11000,"Terminal":10000}` || len(composeParseStrings(activity.RunIDs)) != 2 {
		t.Fatalf("refresh duplicated time or moved run: %+v", activity)
	}
	link, _ := store.activityRunLinkReason(activity.ID, second.ID)
	if link == nil || link.ReasonKind != "shared_path" {
		t.Fatalf("refresh lost link reason: %+v", link)
	}
}

func TestActivityPendingInitiativeDoesNotClaimLiveTask(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	handoffs, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saveLifecycleHandoff(t, handoffs, handoff.Entry{ID: "work-1", Source: handoff.SourceTriage, SourceID: "initiative-1", Status: handoff.StatusInProgress})
	app := &App{activity: &activityMonitorState{store: store}, handoffStore: handoffs}
	views, err := app.GetActivitiesInRange(first.StartMs, second.EndMs)
	if err != nil || len(views) != 1 || views[0].InProgress {
		t.Fatalf("initiative handoff without task provenance marked an old task live: %+v, %v", views, err)
	}
}

func TestActivityLinksRecheckSessionPrivacy(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	if err := store.SaveSession(ActivitySessionRow{ID: "protected-later", StartTime: second.StartMs, EndTime: second.EndMs, TriageStatus: "protected"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids = '["protected-later"]' WHERE id = ?`, second.ID); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	runs, err := app.GetActivityRuns("act_" + first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.ID == second.ID && run.Link != nil {
			t.Fatalf("protected source exposed link tokens: %+v", run.Link)
		}
	}
}

func TestActivityLinkReasonRejectsCommonTokensOnWriteAndRead(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	for i := range 3 {
		id := fmt.Sprintf("common-link-%d", i)
		insertTestRun(t, store, id, int64(10+i), first.StartMs, "done", 1)
		if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens=? WHERE id=?`, composeJSON(first.EvidenceTokens), id); err != nil {
			t.Fatal(err)
		}
		run, err := store.GetTimelineRunByID(id)
		if err != nil {
			t.Fatal(err)
		}
		outcomeTestLink(t, store, "task-"+id, *run)
	}
	activityID := "act_" + first.ID
	// The stored reason predates these other tasks; the read boundary must
	// not continue showing a now-common identifier as a strong explanation.
	link, err := store.activityRunLinkReason(activityID, second.ID)
	if err != nil || link == nil || link.ReasonKind != "related_context" || link.Token != "" {
		t.Fatalf("common token explained link on read: %+v, %v", link, err)
	}
	// These runs are far outside today's damping window. Both historical UI
	// bindings must use their observation window, not the current wall clock.
	app := &App{activity: &activityMonitorState{store: store}}
	views, err := app.GetActivitiesInRange(first.StartMs, second.EndMs)
	if err != nil {
		t.Fatal(err)
	}
	foundCard := false
	for _, view := range views {
		if view.ID == activityID {
			foundCard = true
			if view.LatestLink == nil || view.LatestLink.ReasonKind != "related_context" || view.LatestLink.Token != "" {
				t.Fatalf("historical card revived a common token: %+v", view.LatestLink)
			}
		}
	}
	if !foundCard {
		t.Fatal("historical activity missing from binding")
	}
	runs, err := app.GetActivityRuns(activityID)
	if err != nil {
		t.Fatal(err)
	}
	foundRun := false
	for _, run := range runs {
		if run.ID == second.ID {
			foundRun = true
			if run.Link == nil || run.Link.ReasonKind != "related_context" || run.Link.Token != "" {
				t.Fatalf("historical run revived a common token: %+v", run.Link)
			}
		}
	}
	if !foundRun {
		t.Fatal("historical run missing from binding")
	}
	if err := store.recordActivityLinkReason(activityID, second); err != nil {
		t.Fatal(err)
	}
	var reason, token string
	if err := store.db.QueryRow(`SELECT link_reason,link_token_value FROM activity_run_links WHERE run_id=?`, second.ID).Scan(&reason, &token); err != nil {
		t.Fatal(err)
	}
	if reason != "related_context" || token != "" {
		t.Fatalf("common token explained stored link: %q %q", reason, token)
	}
}

func TestActivityCorrectionRecomputesSurvivingTokenReasons(t *testing.T) {
	store, tr, first, second := composeCorrectionFixture(t)
	third := testRun("correction-third", "Terminal", 40000, 50000, 10000, 2)
	insertTestRun(t, store, third.ID, 3, third.StartMs, "done", 2)
	third.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/.sample-agent/records", LastSeenMs: third.EndMs}}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens=?,end_ms=? WHERE id=?`, composeJSON(third.EvidenceTokens), third.EndMs, third.ID); err != nil {
		t.Fatal(err)
	}
	activityID := "act_" + first.ID
	if _, err := tr.composeApply(store, third, ActivityTimelineGroupSummary{}, composerResponse{Route: "join", JoinBucketID: activityID}, third.EndMs); err != nil {
		t.Fatal(err)
	}
	if err := store.UnlinkActivityRun(activityID, second.ID, third.EndMs); err != nil {
		t.Fatal(err)
	}
	link, err := store.activityRunLinkReason(activityID, third.ID)
	if err != nil || link == nil || link.ReasonKind != "shared_path" || link.Token != "~/.sample-agent/records" {
		t.Fatalf("correction erased surviving shared evidence: %+v, %v", link, err)
	}
}

func TestActivityLinkReasonsRecomputeInsideMigrationTransaction(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	if _, err := store.db.Exec(`UPDATE activity_run_links SET link_reason='',link_token_kind='',link_token_value=''`); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := migrateActivityEvidenceReceiptPolicy(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	link, err := store.activityRunLinkReason("act_"+first.ID, second.ID)
	if err != nil || link == nil || link.ReasonKind != "shared_path" || link.Token != "~/.sample-agent/records" {
		t.Fatalf("migration left surviving identifiers unexplained: %+v, %v", link, err)
	}
}

func TestActivityCorrectionExcludesProtectedSurvivingRunDetails(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	if err := store.SaveSession(ActivitySessionRow{ID: "protected-survivor", StartTime: first.StartMs, EndTime: first.EndMs, TriageStatus: "protected"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids='["protected-survivor"]' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTimelineGroupSummary(timelineRunToGroup(first, nil), ActivityTimelineGroupSummary{Title: "Protected task", Description: "Protected source details", Entities: []string{"protected-project"}}, "test", "", ""); err != nil {
		t.Fatal(err)
	}
	activityID := "act_" + first.ID
	if err := store.UnlinkActivityRun(activityID, second.ID, second.EndMs); err != nil {
		t.Fatal(err)
	}
	activity, err := store.GetActivityByID(activityID)
	if err != nil || activity == nil || activity.RunIDs != composeJSON([]string{first.ID}) {
		t.Fatalf("protected survivor membership changed: %+v, %v", activity, err)
	}
	if activity.ActiveMsByApp != "{}" || activity.Title != "" || activity.Description != "" || activity.LatestChange != "" || activity.Entities != "[]" {
		t.Fatalf("correction persisted protected survivor details: %+v", activity)
	}
	link, err := store.activityRunLinkReason(activityID, first.ID)
	if err != nil || link == nil || link.Token != "" || link.ReasonKind != "related_context" {
		t.Fatalf("protected survivor retained a link explanation: %+v, %v", link, err)
	}
}

func TestActivityViewTotalsExcludeProtectedOnlyMembers(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1`); err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	views, err := app.GetActivitiesInRange(first.StartMs, second.EndMs)
	if err != nil || len(views) != 1 || views[0].ActiveMs != 0 || len(views[0].Apps) != 0 {
		t.Fatalf("protected-only dwell remained visible: %+v, %v", views, err)
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=0 WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	views, err = app.GetActivitiesInRange(first.StartMs, second.EndMs)
	if err != nil || len(views) != 1 || views[0].ActiveMs != first.ActiveMs || len(views[0].Apps) != 1 || views[0].Apps[0].AppName != first.AppName {
		t.Fatalf("mixed task dwell included protected member: %+v, %v", views, err)
	}
}
