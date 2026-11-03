package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestActivityEvidenceSpecificPathsAndPercentages(t *testing.T) {
	for _, tc := range []struct {
		value  string
		strong bool
	}{
		{"/compact", false}, {"/or", false}, {"~/src", false}, {"~/src/repo", false},
		{"src/feature/file.go", false}, {"/tmp/project", false},
		{"~/.sample-agent/activity", true}, {"~/src/repo/file.go", true},
		{"/var/cache/project/records", true},
	} {
		if _, strong := activityEvidenceWeight(ActivityEvidenceToken{Kind: "path", Value: tc.value}, nil); strong != tc.strong {
			t.Errorf("strength(%q)=%v, want %v", tc.value, strong, tc.strong)
		}
	}
	if weight, _ := activityEvidenceWeight(ActivityEvidenceToken{Kind: "path", Value: "/compact"}, nil); weight != 0 {
		t.Fatalf("slash command remains join evidence: %d", weight)
	}
	if activityPathMatches("~/src/repo", "~/src/repo/docs/backlog.md") {
		t.Fatal("directory prefix manufactured a shared target")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !activityPathMatches("~/.sample-agent/activity", filepath.Join(home, ".sample-agent", "activity")) {
		t.Fatal("tilde and actual-home forms should identify the same target")
	}
	if activityFigureMatches("80 %", "81 %") || !activityFigureMatches("80 %", "80 %") {
		t.Fatal("percentages must match exactly")
	}
	for _, value := range []string{"80 %", "9 b", "1 s"} {
		if _, strong := activityEvidenceWeight(ActivityEvidenceToken{Kind: "figure", Value: value}, nil); strong {
			t.Errorf("generic figure %q is strong", value)
		}
	}
	if !activityFigureMatches("1,200 MB", "1.2 GB") {
		t.Fatal("thousands-separated figure did not normalize")
	}
}

func TestCommonActivityEvidenceCountsTasksInsteadOfRuns(t *testing.T) {
	store := newTimelineTestStore(t)
	const now int64 = 10_000_000
	token := ActivityEvidenceToken{Kind: "path", Value: "~/src/repo/file.go", LastSeenMs: now - 40*60_000}
	for i := 0; i < 8; i++ {
		run := testRun("dominant-"+string(rune('a'+i)), "Terminal", token.LastSeenMs-1000+int64(i), token.LastSeenMs+int64(i), 1000, 2)
		run.EvidenceTokens = []ActivityEvidenceToken{token}
		insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{})
		if err := store.UpsertActivityRunLink(run.ID, "one-task", 2, run.EndMs); err != nil {
			t.Fatal(err)
		}
	}
	common, err := store.commonComposerEvidence(now)
	if err != nil || common[activityEvidenceKey(token)] {
		t.Fatalf("repeated runs damped their own task: %v, %v", common, err)
	}
	current := []ActivityEvidenceToken{{Kind: token.Kind, Value: token.Value, LastSeenMs: now}}
	if score, strong := activityEvidenceScore(current, []ActivityEvidenceToken{token}, common, now); score == 0 || !strong {
		t.Fatalf("40-minute resumption lost dominant task identity: %d, %v", score, strong)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.db.Exec(`UPDATE activity_run_links SET activity_id = ? WHERE run_id = ?`, "unrelated-"+string(rune('a'+i)), "dominant-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	common, err = store.commonComposerEvidence(now)
	if err != nil || !common[activityEvidenceKey(token)] {
		t.Fatalf("four distinct tasks did not damp common identifier: %v, %v", common, err)
	}
	if hints := matchedActivityEvidence(current, []ActivityEvidenceToken{{Kind: token.Kind, Value: token.Value, LastSeenMs: now - 1000}}, now, common); len(hints) != 0 {
		t.Fatalf("common token manufactured strong restatement: %+v", hints)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE activity_timeline_groups SET evidence_excluded = 1 WHERE id = 'dominant-a'`); err != nil {
		t.Fatal(err)
	}
	common, err = commonComposerEvidence(tx, now)
	if err != nil || common[activityEvidenceKey(token)] {
		t.Fatalf("transaction commonness ignored changed privacy: %v, %v", common, err)
	}
}

func TestActivityBareErrorWordsCannotStronglyJoinTerminalRuns(t *testing.T) {
	// Guard the weight boundary directly: extraction strips this clause, so
	// extractor-only cases cannot detect accidental clause-based strength.
	if weight, strong := activityEvidenceWeight(ActivityEvidenceToken{Kind: "error", Value: "ENOENT: missing target"}, nil); strong || weight > 10 {
		t.Errorf("a clause-bearing value bypassed code-only strength: weight=%d strong=%v", weight, strong)
	}
	for _, source := range []string{"EXIT", "EDIT", "ENTER", "EMPTY", "ERRORS", "ENVIRONMENT", "Error", "Panic", "TypeError", "Exception", "ENOENT", "Error:", "ENOENT: missing target", "TypeError: unknown value"} {
		for _, token := range extractActivityEvidenceTokens(source, 1000) {
			if token.Kind != "error" {
				continue
			}
			if weight, strong := activityEvidenceWeight(token, nil); strong || weight > 10 {
				t.Errorf("extracted error from %q has weight %d, strong=%v", source, weight, strong)
			}
		}
	}
	for _, source := range []string{"E42", "ERR_DISK", "E42: missing target", "ERR_DISK: unknown value"} {
		tokens := extractActivityEvidenceTokens(source, 1000)
		if len(tokens) != 1 || tokens[0].Kind != "error" {
			t.Fatalf("specific error %q was not extracted: %+v", source, tokens)
		}
		if _, strong := activityEvidenceWeight(tokens[0], nil); !strong {
			t.Errorf("extracted code from %q lost strength", source)
		}
	}
	store := newTimelineTestStore(t)
	tr := &ActivityTriager{}
	older := testRun("exit-old", "Terminal", 1000, 2000, 1000, 2)
	older.EvidenceTokens = extractActivityEvidenceTokens("EXIT", older.EndMs)
	insertComposableTimelineRun(t, store, older, ActivityTimelineGroupSummary{})
	if err := store.InsertActivity(ActivityRow{ID: "exit-task", Status: activityStatusPaused, RunIDs: composeJSON([]string{older.ID}), StartMs: older.StartMs, EndMs: older.EndMs}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertActivityRunLink(older.ID, "exit-task", 2, older.EndMs); err != nil {
		t.Fatal(err)
	}
	current := testRun("exit-new", "Terminal", older.EndMs+10*60_000, older.EndMs+10*60_000+1000, 1000, 2)
	current.EvidenceTokens = extractActivityEvidenceTokens("EXIT", current.StartMs)
	if _, strong := activityEvidenceScore(current.EvidenceTokens, older.EvidenceTokens, nil, current.StartMs); strong {
		t.Fatal("two bare terminal EXIT lines became strong task evidence")
	}
	if matches := matchedActivityEvidence(current.EvidenceTokens, older.EvidenceTokens, current.StartMs); len(matches) != 0 {
		t.Fatalf("bare EXIT emitted a strong restatement: %+v", matches)
	}
	candidates, err := tr.activityAssignmentCandidates(store, current, ActivityTimelineGroupSummary{})
	if err != nil || len(candidates) != 0 {
		t.Fatalf("bare EXIT reopened a paused task: %+v, %v", candidates, err)
	}
	response := tr.guardComposerAssignment(composerResponse{Route: "join", JoinBucketID: "exit-task", Confidence: "high"}, candidates)
	if response.Route != "new" {
		t.Fatalf("bare EXIT passed the task join guard: %+v", response)
	}
}

func TestActivityEvidenceChatAppsUseExactIdentity(t *testing.T) {
	for _, tc := range []struct {
		key, name string
		want      bool
	}{
		{"com.anthropic.claudefordesktop", "Claude", true},
		{"app:Slack", "Slack", true},
		{"com.apple.iChat", "Messages", true},
		{"com.microsoft.teams2", "Microsoft Teams", true},
		{"com.google.Chrome", "Claude API pricing", false},
		{"com.apple.Safari", "Mail merge messages", false},
		{"app:Editor", "Signal processing notes", false},
		{"com.example.teams.docs", "Teams documentation", false},
	} {
		if got := activityEvidenceChatApp(tc.key, tc.name); got != tc.want {
			t.Errorf("chat(%q,%q)=%v, want %v", tc.key, tc.name, got, tc.want)
		}
	}
}

func TestComposerTransitionKeepsPrivatePredecessorGap(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTimelineTestStore(t)
	for _, run := range []ActivityTimelineRunRow{
		testRun("earlier-terminal", "Terminal", 60_000, 300_000, 240_000, 2),
		testRun("private-predecessor", "1Password", 360_000, 420_000, 60_000, 2),
	} {
		run.SessionIDs = []string{"source-" + run.ID}
		if err := store.SaveSession(ActivitySessionRow{ID: run.SessionIDs[0], StartTime: run.StartMs, EndTime: run.EndMs, AppNames: composeJSON([]string{run.AppName})}, nil, nil); err != nil {
			t.Fatal(err)
		}
		insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{})
	}
	current := testRun("following-chat", "Claude", 480_000, 490_000, 10_000, 2)
	tr := &ActivityTriager{app: &App{activity: &activityMonitorState{store: store}}}
	transition := tr.composeTransitionContext(store, current)
	if transition.PrevApp != "" || transition.GapMs != 60_000 {
		t.Fatalf("private predecessor replaced by older app: %+v", transition)
	}
	if transition.RecentSwitchCount != 1 {
		t.Fatalf("safe prior lost or private prior counted: %+v", transition)
	}
}

func TestActivityAppUsageAndComposedTotalsRespectChangedPrivacy(t *testing.T) {
	store := newTimelineTestStore(t)
	tr := &ActivityTriager{}
	const activityID = "act_visible-terminal"
	runs := []ActivityTimelineRunRow{
		testRun("visible-terminal", "Terminal", 1000, 2000, 1000, 2),
		testRun("protected-editor", "Editor", 3000, 5000, 2000, 2),
		testRun("excluded-browser", "Chrome", 6000, 9000, 3000, 2),
		testRun("password-manager", "1Password", 10000, 14000, 4000, 2),
	}
	for i, run := range runs {
		insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{})
		response := composerResponse{Route: "join", JoinBucketID: activityID}
		if i == 0 {
			response.Route = "new"
		}
		if _, err := tr.composeApply(store, run, ActivityTimelineGroupSummary{}, response, run.EndMs); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveSession(ActivitySessionRow{ID: "protected-session", StartTime: 3000, EndTime: 5000, TriageStatus: "protected"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids = '["protected-session"]' WHERE id = 'protected-editor'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded = 1 WHERE id = 'excluded-browser'`); err != nil {
		t.Fatal(err)
	}
	usage, err := store.GetActivityAppUsage([]string{activityID})
	if err != nil || len(usage[activityID]) != 1 || usage[activityID][0].AppName != "Terminal" || usage[activityID][0].ActiveMs != 1000 {
		t.Fatalf("private members survived app usage: %+v, %v", usage, err)
	}
	current := testRun("new-terminal", "Terminal", 15000, 15500, 500, 2)
	insertComposableTimelineRun(t, store, current, ActivityTimelineGroupSummary{})
	if _, err := tr.composeApply(store, current, ActivityTimelineGroupSummary{}, composerResponse{Route: "join", JoinBucketID: activityID}, current.EndMs); err != nil {
		t.Fatal(err)
	}
	activity, err := store.GetActivityByID(activityID)
	if err != nil || activity == nil || activity.ActiveMsByApp != `{"Terminal":1500}` {
		t.Fatalf("composition persisted private app totals: %+v, %v", activity, err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	views, err := app.GetActivitiesInRange(0, 0)
	if err != nil || len(views) != 1 || views[0].ActiveMs != 1500 || len(views[0].Apps) != 1 {
		t.Fatalf("activity view totals do not match permitted members: %+v, %v", views, err)
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded = 1 WHERE app_name = 'Terminal'`); err != nil {
		t.Fatal(err)
	}
	views, err = app.GetActivitiesInRange(0, 0)
	if err != nil || len(views) != 1 || views[0].ActiveMs != 0 || len(views[0].Apps) != 0 {
		t.Fatalf("all-private view fell back to stale stored totals: %+v, %v", views, err)
	}
}

func TestActivityCandidatesLateSummaryKeepsSurvivingMembers(t *testing.T) {
	store := newTimelineTestStore(t)
	tr := &ActivityTriager{}
	later := testRun("later-restatement", "Claude", 500_000, 510_000, 10_000, 2)
	later.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/.sample-agent/records", LastSeenMs: later.EndMs}}
	insertComposableTimelineRun(t, store, later, ActivityTimelineGroupSummary{Title: "Inspect sample storage"})
	if _, err := tr.composeApply(store, later, ActivityTimelineGroupSummary{}, composerResponse{Route: "new"}, later.EndMs); err != nil {
		t.Fatal(err)
	}
	excluded := testRun("excluded-member", "1Password", 520_000, 530_000, 10_000, 2)
	insertComposableTimelineRun(t, store, excluded, ActivityTimelineGroupSummary{})
	if _, err := store.db.Exec(`UPDATE activities SET run_ids = ?, active_ms_by_app = ?, title = 'Excluded source title' WHERE id = ?`, composeJSON([]string{"expired-missing-member", later.ID, excluded.ID}), `{"Claude":10000,"1Password":10000}`, "act_"+later.ID); err != nil {
		t.Fatal(err)
	}
	earlier := testRun("earlier-terminal", "Terminal", 100_000, 110_000, 10_000, 2)
	earlier.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/.sample-agent/records", LastSeenMs: earlier.EndMs}}
	candidates, err := tr.activityAssignmentCandidates(store, earlier, ActivityTimelineGroupSummary{})
	if err != nil || len(candidates) != 1 || candidates[0].ID != "act_"+later.ID {
		t.Fatalf("late earlier run lost surviving strong task evidence: %+v, %v", candidates, err)
	}
	if candidates[0].Title != "" || candidates[0].ActiveMsByApp != "{}" {
		t.Fatalf("candidate prompt retained excluded-member prose: %+v", candidates[0])
	}
	response := tr.guardComposerAssignment(composerResponse{
		Route: "join", JoinBucketID: candidates[0].ID, Confidence: "high", Reason: "shared_path",
		Admission: []composerAdmission{{BucketID: candidates[0].ID, Decision: "join", ObjectiveMatch: "medium", SubjectMatch: "strong", StateContinuity: "strong"}},
	}, candidates)
	if response.Route != "join" {
		t.Fatalf("late evidence failed admission: %+v", response)
	}
	insertComposableTimelineRun(t, store, earlier, ActivityTimelineGroupSummary{Title: "Inspect sample storage"})
	if changed, err := tr.composeApply(store, earlier, ActivityTimelineGroupSummary{}, response, later.EndMs); err != nil || !changed {
		t.Fatalf("late earlier run failed persistence: %v, %v", changed, err)
	}
	activities, err := store.GetActivitiesInRange(0, 0)
	if err != nil || len(activities) != 1 || !composeContains(composeParseStrings(activities[0].RunIDs), earlier.ID) || activities[0].StartMs != earlier.StartMs {
		t.Fatalf("late earlier summary fragmented task: %+v, %v", activities, err)
	}
}

func TestCommonComposerEvidenceBoundsRowsBeforeDecoding(t *testing.T) {
	store := newTimelineTestStore(t)
	const now int64 = 3 * activityStrongLinkMs
	for _, tc := range []struct {
		id  string
		end int64
	}{
		{"ancient", now - activityStrongLinkMs - 1000},
		{"recent", now - 1000},
	} {
		run := testRun(tc.id, "Terminal", tc.end-1000, tc.end, 1000, 2)
		run.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/src/repo/file.go", LastSeenMs: tc.end}}
		insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{})
		if err := store.UpsertActivityRunLink(run.ID, "task-"+tc.id, 2, tc.end); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens = 'invalid retained JSON' WHERE id = 'ancient'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.commonComposerEvidence(now); err != nil {
		t.Fatalf("ancient evidence was decoded outside the bounded SQL window: %v", err)
	}
}

func TestComposerGrownRunRepairsMissingLinkWithoutDuplicatingTime(t *testing.T) {
	store := newTimelineTestStore(t)
	tr := &ActivityTriager{}
	run := testRun("growing", "Terminal", 1000, 3000, 2000, 2)
	insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{Title: "Inspect storage"})
	if _, err := tr.composeApply(store, run, ActivityTimelineGroupSummary{}, composerResponse{Route: "new"}, run.EndMs); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM activity_run_links WHERE run_id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run.EndMs, run.ActiveMs, run.SummarizedEventCount = 7000, 6000, 6
	if _, err := tr.composeApply(store, run, ActivityTimelineGroupSummary{}, composerResponse{Route: "new"}, run.EndMs); err != nil {
		t.Fatalf("grown run tried to reinsert own bucket: %v", err)
	}
	activities, err := store.GetActivitiesInRange(0, 0)
	if err != nil || len(activities) != 1 || composeParseInt64Map(activities[0].ActiveMsByApp)["Terminal"] != 6000 {
		t.Fatalf("grown run duplicated activity or time: %+v, %v", activities, err)
	}
}

func TestComposerTransitionsCountAllSwitchesButBoundHints(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTimelineTestStore(t)
	const now int64 = 1_000_000
	for i := 0; i < 8; i++ {
		end := now - int64(i+1)*20_000
		run := testRun("switch-"+string(rune('a'+i)), "Terminal", end-10_000, end, 10_000, 2)
		run.SessionIDs = []string{"source-" + run.ID}
		if err := store.SaveSession(ActivitySessionRow{ID: run.SessionIDs[0], StartTime: run.StartMs, EndTime: run.EndMs, AppNames: `["Terminal"]`}, nil, nil); err != nil {
			t.Fatal(err)
		}
		run.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/src", LastSeenMs: end}}
		insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{})
	}
	// A run captured before its app became sensitive must not contribute a
	// switch or an app name, while all eight readable switches remain usable.
	private := testRun("private-switch", "Terminal", now-15_000, now-5_000, 10_000, 2)
	private.SessionIDs = []string{"private-switch-source"}
	if err := store.SaveSession(ActivitySessionRow{ID: private.SessionIDs[0], StartTime: private.StartMs, EndTime: private.EndMs, AppNames: `["Terminal","Private Notes"]`}, nil, nil); err != nil {
		t.Fatal(err)
	}
	insertComposableTimelineRun(t, store, private, ActivityTimelineGroupSummary{})
	current := testRun("current", "Claude", now, now+1000, 1000, 2)
	current.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/src", LastSeenMs: now}}
	tr := &ActivityTriager{app: &App{activity: &activityMonitorState{store: store}}}
	transition := tr.composeTransitionContext(store, current)
	if transition.RecentSwitchCount != 8 || len(transition.Restatements) != 0 {
		t.Fatalf("switch count capped or weak path treated as restatement: %+v", transition)
	}
	if transition.PrevApp != "" || transition.GapMs != 5_000 {
		t.Fatalf("protected predecessor leaked or changed the timing gap: %+v", transition)
	}
	tr.app = nil
	if unreadable := tr.composeTransitionContext(store, current); unreadable.RecentSwitchCount != 0 || unreadable.PrevApp != "" || len(unreadable.Restatements) != 0 || len(unreadable.TriageHints) != 0 {
		t.Fatalf("missing App privacy gate exposed transition evidence: %+v", unreadable)
	}
}

func TestActivityCandidateReadsPersistedEvidenceWithoutReextracting(t *testing.T) {
	store := newTimelineTestStore(t)
	tr := &ActivityTriager{}
	session := ActivitySessionRow{ID: "persisted-source", StartTime: 1000, EndTime: 2000, AppNames: `["Terminal"]`}
	saveTimelineTestSession(t, store, session, []activitymon.ActivityEvent{
		{Kind: activitymon.EventKeyboard, Timestamp: 1000, AppName: "Terminal", TypedText: "~/src/unrelated/capture.go"},
		{Kind: activitymon.EventKeyboard, Timestamp: 2000, AppName: "Terminal", TypedText: "~/src/unrelated/capture.go"},
	})
	runs, err := store.GetTimelineRuns(1, 0, 0, 3000)
	if err != nil || len(runs) != 1 {
		t.Fatalf("source runs=%+v err=%v", runs, err)
	}
	run := runs[0]
	run.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/src/repo/stored.go", LastSeenMs: 2000}}
	if err := persistTimelineRunEvidence(store.db, run); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertActivity(ActivityRow{ID: "task", Status: activityStatusActive, RunIDs: composeJSON([]string{run.ID}), StartMs: 1000, EndMs: 2000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertActivityRunLink(run.ID, "task", 2, 2000); err != nil {
		t.Fatal(err)
	}
	current := testRun("incoming", "Claude", 3000, 4000, 1000, 2)
	current.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/src/repo/stored.go", LastSeenMs: 3000}}
	if candidates, err := tr.activityAssignmentCandidates(store, current, ActivityTimelineGroupSummary{}); err != nil || len(candidates) != 1 {
		t.Fatalf("persisted evidence candidates=%+v err=%v", candidates, err)
	}
	stored, err := store.GetTimelineRunByID(run.ID)
	if err != nil || stored == nil || len(stored.EvidenceTokens) != 1 || strings.Contains(stored.EvidenceTokens[0].Value, "unrelated") {
		t.Fatalf("candidate read recollected raw historical capture: %+v, %v", stored, err)
	}
}
