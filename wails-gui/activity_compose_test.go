package main

import "testing"

func testRun(id, app string, startMs, endMs, activeMs int64, summarized int) ActivityTimelineRunRow {
	return ActivityTimelineRunRow{
		ID: id, AppKey: "app:" + app, AppName: app,
		StartMs: startMs, EndMs: endMs, ActiveMs: activeMs, SummarizedEventCount: summarized,
	}
}

func TestComposeApply_NewBucket(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	run := testRun("tg_1", "Codex", 1000, 5000, 4000, 3)
	summary := ActivityTimelineGroupSummary{Title: "Debug auth", Description: "looked at auth", Entities: []string{"api repo"}}
	insertComposableTimelineRun(t, store, run, summary)
	resp := composerResponse{
		Route: "new",
		Bucket: composerBucketUpdate{
			Title: "Ship auth fix", Description: "Fixing the auth flow", Status: "active",
			LatestChange: "Started in Codex", Category: "build",
			Entities: []Entity{{Type: "repo", Name: "api"}},
		},
	}
	changed, err := tr.composeApply(store, run, summary, resp, 6000)
	if err != nil || !changed {
		t.Fatalf("composeApply new: changed=%v err=%v", changed, err)
	}

	acts, _ := store.GetActivitiesInRange(0, 0)
	if len(acts) != 1 {
		t.Fatalf("want 1 activity, got %d", len(acts))
	}
	a := acts[0]
	if a.ID != "act_tg_1" || a.Title != "Ship auth fix" || a.Category != "build" {
		t.Fatalf("bad bucket: %+v", a)
	}
	if a.CategoryColor != "#22c55e" {
		t.Fatalf("category color = %q, want build green", a.CategoryColor)
	}
	if a.RunIDs != `["tg_1"]` || a.ActiveMsByApp != `{"Codex":4000}` {
		t.Fatalf("runs/time = %s / %s", a.RunIDs, a.ActiveMsByApp)
	}
	if a.StartMs != 1000 || a.EndMs != 5000 {
		t.Fatalf("span = %d-%d, want 1000-5000", a.StartMs, a.EndMs)
	}
	if a.LatestChange != "Started in Codex" {
		t.Fatalf("latestChange = %q", a.LatestChange)
	}
}

func TestComposeApply_JoinAccumulates(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	// First run -> new bucket.
	r1 := testRun("tg_1", "Codex", 1000, 5000, 4000, 3)
	insertComposableTimelineRun(t, store, r1, ActivityTimelineGroupSummary{Title: "Debug auth"})
	_, _ = tr.composeApply(store, r1, ActivityTimelineGroupSummary{Title: "Debug auth"},
		composerResponse{Route: "new", Bucket: composerBucketUpdate{Title: "Auth fix", Category: "build", Status: "active"}}, 6000)

	// Second run (different app) joins the same bucket.
	r2 := testRun("tg_2", "Chrome", 6000, 9000, 3000, 2)
	insertComposableTimelineRun(t, store, r2, ActivityTimelineGroupSummary{Title: "Read auth docs"})
	changed, err := tr.composeApply(store, r2, ActivityTimelineGroupSummary{Title: "Read auth docs"},
		composerResponse{Route: "join", JoinBucketID: "act_tg_1",
			Bucket: composerBucketUpdate{Title: "Auth fix", Category: "build", Status: "active", LatestChange: "Checked docs"}}, 10000)
	if err != nil || !changed {
		t.Fatalf("join: changed=%v err=%v", changed, err)
	}

	acts, _ := store.GetActivitiesInRange(0, 0)
	if len(acts) != 1 {
		t.Fatalf("want 1 activity after join, got %d", len(acts))
	}
	a := acts[0]
	if a.RunIDs != `["tg_1","tg_2"]` {
		t.Fatalf("run ids = %s, want both", a.RunIDs)
	}
	if a.ActiveMsByApp != `{"Chrome":3000,"Codex":4000}` {
		t.Fatalf("time accounting = %s, want Codex+Chrome", a.ActiveMsByApp)
	}
	if a.StartMs != 1000 || a.EndMs != 9000 {
		t.Fatalf("span = %d-%d, want 1000-9000", a.StartMs, a.EndMs)
	}
}

func TestComposeApply_SkipCreatesNoActivity(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	run := testRun("tg_1", "WeChat", 1000, 3000, 2000, 1)
	changed, err := tr.composeApply(store, run, ActivityTimelineGroupSummary{Title: "Opened WeChat"},
		composerResponse{Route: "skip"}, 4000)
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if changed {
		t.Fatalf("skip must report no change")
	}
	if acts, _ := store.GetActivitiesInRange(0, 0); len(acts) != 0 {
		t.Fatalf("skip must create no activity, got %d", len(acts))
	}
	// The run is linked (composed) so it isn't retried, but to no bucket.
	var n int
	if err := store.db.QueryRow(`SELECT count(*) FROM activity_run_links WHERE run_id='tg_1' AND activity_id=''`).Scan(&n); err != nil {
		t.Fatalf("query link: %v", err)
	}
	if n != 1 {
		t.Fatalf("skipped run must be skip-marked (link with empty activity_id), got %d", n)
	}
}

func TestComposeApply_Merge(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	// Two separate persisted runs create separate buckets first.
	insertComposableTimelineRun(t, store, testRun("tg_1", "Codex", 1000, 2000, 1000, 1), ActivityTimelineGroupSummary{Title: "Research X"})
	insertComposableTimelineRun(t, store, testRun("tg_2", "Chrome", 3000, 4000, 1000, 1), ActivityTimelineGroupSummary{Title: "Debug Y"})
	_, _ = tr.composeApply(store, testRun("tg_1", "Codex", 1000, 2000, 1000, 1),
		ActivityTimelineGroupSummary{Title: "Research X"},
		composerResponse{Route: "new", Bucket: composerBucketUpdate{Title: "Research X", Category: "explore", Status: "active"}}, 3000)
	_, _ = tr.composeApply(store, testRun("tg_2", "Chrome", 3000, 4000, 1000, 1),
		ActivityTimelineGroupSummary{Title: "Debug Y"},
		composerResponse{Route: "new", Bucket: composerBucketUpdate{Title: "Debug Y", Category: "build", Status: "active"}}, 5000)

	if acts, _ := store.GetActivitiesInRange(0, 0); len(acts) != 2 {
		t.Fatalf("setup: want 2 buckets, got %d", len(acts))
	}

	// A third run reveals the two are one piece of work: merge act_tg_2 into
	// act_tg_1, and route the new run into the survivor.
	r3 := testRun("tg_3", "WezTerm", 6000, 8000, 2000, 1)
	insertComposableTimelineRun(t, store, r3, ActivityTimelineGroupSummary{Title: "Wire X and Y"})
	changed, err := tr.composeApply(store, r3, ActivityTimelineGroupSummary{Title: "Wire X and Y"},
		composerResponse{
			Route: "join", JoinBucketID: "act_tg_2",
			Merge:  []string{"act_tg_1", "act_tg_2"},
			Bucket: composerBucketUpdate{Title: "Unified X/Y work", Category: "build", Status: "active"},
		}, 9000)
	if err != nil || !changed {
		t.Fatalf("merge: changed=%v err=%v", changed, err)
	}

	acts, _ := store.GetActivitiesInRange(0, 0)
	if len(acts) != 1 {
		t.Fatalf("after merge want 1 bucket, got %d: %+v", len(acts), acts)
	}
	a := acts[0]
	if a.ID != "act_tg_1" {
		t.Fatalf("survivor = %s, want act_tg_1", a.ID)
	}
	if a.RunIDs != `["tg_1","tg_2","tg_3"]` {
		t.Fatalf("merged runs = %s, want all three", a.RunIDs)
	}
	if gone, _ := store.GetActivityByID("act_tg_2"); gone != nil {
		t.Fatalf("folded bucket act_tg_2 should be deleted")
	}
}

func TestComposeApplySegment_ContinuesOnlyLocalPreviousSegment(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	r1 := testRun("tg_101", "Codex", 1_000, 10_000, 9_000, 2)
	s1 := ActivityTimelineGroupSummary{GroupID: r1.ID, Title: "Debug image preview bug", ActivityType: "coding", Entities: []string{"isoform/yansu", "Image Preview Bug"}}
	insertComposableTimelineRun(t, store, r1, s1)
	changed, err := tr.composeApplySegment(store, nil, r1, s1, 11_000)
	if err != nil || !changed {
		t.Fatalf("first segment: changed=%v err=%v", changed, err)
	}

	r2 := testRun("tg_102", "Chrome", 20_000, 30_000, 10_000, 2)
	s2 := ActivityTimelineGroupSummary{GroupID: r2.ID, Title: "Review PR notes", ActivityType: "research", Entities: []string{"isoform/yansu", "PR #1969"}}
	insertComposableTimelineRun(t, store, r2, s2)
	prev, err := store.GetLatestOpenActivityBefore(startOfDayLocal(r2.StartMs), r2.StartMs)
	if err != nil || prev == nil {
		t.Fatalf("load previous segment: prev=%+v err=%v", prev, err)
	}
	if !tr.segmentContinuesPrevious(store, *prev, r2, s2) {
		t.Fatalf("same concrete repo within continuity window should continue previous segment")
	}
	if _, err := tr.composeApplySegment(store, prev, r2, s2, 31_000); err != nil {
		t.Fatalf("join segment: %v", err)
	}

	r3 := testRun("tg_103", "Synara", 45*60*1000, 45*60*1000+10_000, 10_000, 2)
	s3 := ActivityTimelineGroupSummary{GroupID: r3.ID, Title: "Debug image preview bug", ActivityType: "coding", Entities: []string{"Image Preview Bug"}}
	insertComposableTimelineRun(t, store, r3, s3)
	prev, err = store.GetLatestOpenActivityBefore(startOfDayLocal(r3.StartMs), r3.StartMs)
	if err != nil || prev == nil {
		t.Fatalf("load previous segment before long gap: prev=%+v err=%v", prev, err)
	}
	if tr.segmentContinuesPrevious(store, *prev, r3, s3) {
		t.Fatalf("loose topic after long gap should start a fresh segment")
	}
	if _, err := tr.composeApplySegment(store, nil, r3, s3, r3.EndMs+1_000); err != nil {
		t.Fatalf("new segment after long gap: %v", err)
	}

	acts, _ := store.GetActivitiesInRange(0, 0)
	if len(acts) != 2 {
		t.Fatalf("activities = %d, want 2 segments: %+v", len(acts), acts)
	}
}

func TestComposeApplySegment_RebuildsExistingRunActivity(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	run := testRun("tg_201", "Yansu", 1_000, 5_000, 4_000, 3)
	summary := ActivityTimelineGroupSummary{GroupID: run.ID, Title: "Review handoff report", ActivityType: "research"}
	insertComposableTimelineRun(t, store, run, summary)
	if _, err := tr.composeApplySegment(store, nil, run, summary, 6_000); err != nil {
		t.Fatalf("initial compose: %v", err)
	}

	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET summarized_event_count = 5 WHERE id = ?`, run.ID); err != nil {
		t.Fatalf("grow summarized count: %v", err)
	}
	run.SummarizedEventCount = 5
	updatedSummary := ActivityTimelineGroupSummary{GroupID: run.ID, Title: "Review updated handoff report", ActivityType: "research"}
	changed, err := tr.composeApplySegment(store, nil, run, updatedSummary, 7_000)
	if err != nil || !changed {
		t.Fatalf("recompose existing run activity: changed=%v err=%v", changed, err)
	}

	acts, _ := store.GetActivitiesInRange(0, 0)
	if len(acts) != 1 {
		t.Fatalf("activities = %d, want one rebuilt activity", len(acts))
	}
	if acts[0].Title != "Review updated handoff report" {
		t.Fatalf("title = %q, want updated summary title", acts[0].Title)
	}
	var composedCount int
	if err := store.db.QueryRow(`SELECT composed_event_count FROM activity_run_links WHERE run_id = ?`, run.ID).Scan(&composedCount); err != nil {
		t.Fatalf("load run link: %v", err)
	}
	if composedCount != 5 {
		t.Fatalf("composed count = %d, want 5", composedCount)
	}
}

func TestActivityAssignmentCandidates_DocumentAcrossApps(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	wechat := testRun("tg_doc_1", "WeChat", 1_000, 30_000, 29_000, 3)
	wechatSummary := ActivityTimelineGroupSummary{
		GroupID: wechat.ID, Title: "Opened file \"测试用例.docx\" in WeChat",
		Description: "The user opened a test case document shared in WeChat.",
		Entities:    []string{"测试用例.docx", "Maya Chen"},
	}
	insertComposableTimelineRun(t, store, wechat, wechatSummary)
	if _, err := tr.composeApplySegment(store, nil, wechat, wechatSummary, 31_000); err != nil {
		t.Fatalf("compose WeChat doc activity: %v", err)
	}

	wps := testRun("tg_doc_2", "WPS Office", 31_000, 64_000, 33_000, 4)
	wpsSummary := ActivityTimelineGroupSummary{
		GroupID: wps.ID, Title: "Viewed document \"[只读]测试用例.docx\"",
		Description: "The user reviewed test case procedures in the read-only document.",
		Entities:    []string{"[只读]测试用例.docx", "测试用例", "栏位功能是否正常测试"},
	}
	insertComposableTimelineRun(t, store, wps, wpsSummary)

	candidates, err := tr.activityAssignmentCandidates(store, wps, wpsSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if len(candidates) == 0 || candidates[0].ID != "act_tg_doc_1" {
		t.Fatalf("document run candidates = %+v, want existing WeChat document activity first", candidates)
	}
}

func TestActivityAssignmentCandidates_ReturnsEarlierInterleavedTask(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	sshRun := testRun("tg_ssh_1", "Codex", 1_000, 60_000, 59_000, 5)
	sshSummary := ActivityTimelineGroupSummary{
		GroupID: sshRun.ID, Title: "Debug SSH proxy on HALCYON",
		Description: "The user investigated SSH port forwarding and proxy setup on HALCYON.",
		Entities:    []string{"HALCYON", "dell", "ssh.exe", "127.0.0.1:7890"},
	}
	insertComposableTimelineRun(t, store, sshRun, sshSummary)
	if _, err := tr.composeApplySegment(store, nil, sshRun, sshSummary, 61_000); err != nil {
		t.Fatalf("compose ssh activity: %v", err)
	}

	chatRun := testRun("tg_chat_1", "WeChat", 62_000, 90_000, 28_000, 2)
	chatSummary := ActivityTimelineGroupSummary{
		GroupID: chatRun.ID, Title: "Reply to Maya Chen about test cases",
		Entities: []string{"Maya Chen", "测试用例.docx"},
	}
	insertComposableTimelineRun(t, store, chatRun, chatSummary)
	if _, err := tr.composeApplySegment(store, nil, chatRun, chatSummary, 91_000); err != nil {
		t.Fatalf("compose interleaved chat activity: %v", err)
	}

	todeskRun := testRun("tg_ssh_2", "ToDesk", 92_000, 110_000, 18_000, 3)
	todeskSummary := ActivityTimelineGroupSummary{
		GroupID: todeskRun.ID, Title: "Operate HALCYON remote desktop via ToDesk",
		Description: "The user checked the HALCYON Windows machine while debugging the proxy task.",
		Entities:    []string{"HALCYON", "ToDesk", "Windows PowerShell", "proxy.vbs", "ssh.exe"},
	}
	insertComposableTimelineRun(t, store, todeskRun, todeskSummary)

	candidates, err := tr.activityAssignmentCandidates(store, todeskRun, todeskSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if len(candidates) == 0 || candidates[0].ID != "act_tg_ssh_1" {
		t.Fatalf("interleaved task candidates = %+v, want earlier SSH activity first", candidates)
	}
}

func TestActivityAssignmentCandidates_KeepsNewestSameAppForModelReview(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	first := testRun("tg_codex_1", "Codex", 1_000, 30_000, 29_000, 3)
	firstSummary := ActivityTimelineGroupSummary{GroupID: first.ID, Title: "Debug auth bug", Entities: []string{"auth bug"}}
	insertComposableTimelineRun(t, store, first, firstSummary)
	if _, err := tr.composeApplySegment(store, nil, first, firstSummary, 31_000); err != nil {
		t.Fatalf("compose first Codex activity: %v", err)
	}

	second := testRun("tg_codex_2", "Codex", 32_000, 60_000, 28_000, 3)
	secondSummary := ActivityTimelineGroupSummary{GroupID: second.ID, Title: "Draft unrelated release notes", Entities: []string{"release notes"}}
	insertComposableTimelineRun(t, store, second, secondSummary)
	candidates, err := tr.activityAssignmentCandidates(store, second, secondSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != "act_tg_codex_1" {
		t.Fatalf("want latest open candidate for model review, got %+v", candidates)
	}
}

func TestActivityAssignmentCandidates_KeepsNewestPersonOverlapForModelReview(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	debugRun := testRun("tg_debug_1", "Codex", 1_000, 30_000, 29_000, 3)
	debugSummary := ActivityTimelineGroupSummary{
		GroupID: debugRun.ID, Title: "Debug stream disconnection in Synara AI chat",
		Entities: []string{"Synara", "stream disconnection", "Maya Chen"},
	}
	insertComposableTimelineRun(t, store, debugRun, debugSummary)
	if _, err := tr.composeApplySegment(store, nil, debugRun, debugSummary, 31_000); err != nil {
		t.Fatalf("compose debug activity: %v", err)
	}

	slackRun := testRun("tg_slack_1", "Slack", 32_000, 60_000, 28_000, 3)
	slackSummary := ActivityTimelineGroupSummary{
		GroupID: slackRun.ID, Title: "Checked Slack activity and navigated to DM with Maya Chen",
		Entities: []string{"Slack", "Isoform", "Maya Chen", "Priya"},
	}
	insertComposableTimelineRun(t, store, slackRun, slackSummary)
	candidates, err := tr.activityAssignmentCandidates(store, slackRun, slackSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != "act_tg_debug_1" {
		t.Fatalf("want latest open candidate for model review, got %+v", candidates)
	}
}

func TestActivityAssignmentCandidates_AllowsSharedRefundIntent(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	first := testRun("tg_refund_1", "Google Chrome", 1_000, 180_000, 179_000, 8)
	firstSummary := ActivityTimelineGroupSummary{
		GroupID: first.ID,
		Title:   "Request refund for Claude Max plan after Fable 5 discontinuation",
		Description: "The user opened Claude settings, attempted to downgrade from Max to Pro, " +
			"and requested a full refund because Claude Fable 5 is unavailable.",
		Entities: []string{"Claude", "Anthropic", "Claude Fable 5", "Claude Max plan", "Claude Pro plan", "Fin AI Agent"},
	}
	insertComposableTimelineRun(t, store, first, firstSummary)
	if _, err := tr.composeApplySegment(store, nil, first, firstSummary, 181_000); err != nil {
		t.Fatalf("compose refund activity: %v", err)
	}

	second := testRun("tg_refund_2", "Google Chrome", 182_000, 320_000, 138_000, 7)
	secondSummary := ActivityTimelineGroupSummary{
		GroupID: second.ID,
		Title:   "Request Claude Fable 5 refund and downgrade Max plan to Pro",
		Description: "The user continued resolving the Claude billing issue, sought a refund " +
			"for the Max plan, and scheduled a downgrade to Pro.",
		Entities: []string{"Claude", "Claude Fable 5", "Claude Max plan", "Claude Pro plan", "Priya"},
	}
	insertComposableTimelineRun(t, store, second, secondSummary)

	candidates, err := tr.activityAssignmentCandidates(store, second, secondSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if len(candidates) == 0 || candidates[0].ID != "act_tg_refund_1" {
		t.Fatalf("refund intent candidates = %+v, want existing refund activity first", candidates)
	}
}

func TestActivityAssignmentCandidates_ReturnsSplitRefundActivitiesForMerge(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	for _, activity := range []ActivityRow{
		{
			ID: "act_refund_1", Status: activityStatusActive,
			Title:       "Request refund for Claude Max plan after Fable 5 discontinuation",
			Description: "The user requested a refund for the Claude Max plan after Claude Fable 5 became unavailable.",
			RunIDs:      `["tg_refund_1"]`, ActiveMsByApp: `{}`, StartMs: 1_000, EndMs: 180_000, CreatedAt: 1, UpdatedAt: 1,
			Entities: composeJSON([]Entity{
				{Type: entityTopic, Name: "Claude Fable 5"},
				{Type: entityTopic, Name: "Claude Max plan"},
			}),
		},
		{
			ID: "act_refund_2", Status: activityStatusActive,
			Title:       "Request Claude Fable 5 refund and downgrade Max plan to Pro",
			Description: "The user sought a refund for Claude Max plan and scheduled a downgrade to Pro.",
			RunIDs:      `["tg_refund_2"]`, ActiveMsByApp: `{}`, StartMs: 181_000, EndMs: 320_000, CreatedAt: 2, UpdatedAt: 2,
			Entities: composeJSON([]Entity{
				{Type: entityTopic, Name: "Claude Fable 5"},
				{Type: entityTopic, Name: "Claude Max plan"},
				{Type: entityTopic, Name: "Claude Pro plan"},
			}),
		},
	} {
		if err := store.InsertActivity(activity); err != nil {
			t.Fatalf("insert activity %s: %v", activity.ID, err)
		}
		member := testRun(composeParseStrings(activity.RunIDs)[0], "Google Chrome", activity.StartMs, activity.EndMs, activity.EndMs-activity.StartMs, 2)
		insertComposableTimelineRun(t, store, member, ActivityTimelineGroupSummary{Title: activity.Title, Description: activity.Description})
	}

	third := testRun("tg_refund_3", "Google Chrome", 321_000, 373_000, 52_000, 4)
	thirdSummary := ActivityTimelineGroupSummary{
		GroupID: third.ID,
		Title:   "Requested refund via Claude's Fin AI Agent support chat",
		Description: "The user sent messages to the Fin AI Agent asking for a refund " +
			"for the Claude Max plan.",
		Entities: []string{"Claude", "Fin AI Agent", "Intercom Live Chat", "Priya", "Max plan"},
	}
	insertComposableTimelineRun(t, store, third, thirdSummary)

	candidates, err := tr.activityAssignmentCandidates(store, third, thirdSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if !hasActivityCandidate(candidates, "act_refund_1") || !hasActivityCandidate(candidates, "act_refund_2") {
		t.Fatalf("split refund candidates = %+v, want both existing refund activities", candidates)
	}
}

func TestActivityAssignmentCandidates_AllowsSameNamedSessionSubject(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	first := testRun("tg_standup_1", "Google Chrome", 1_000, 60_000, 59_000, 5)
	firstSummary := ActivityTimelineGroupSummary{
		GroupID:     first.ID,
		Title:       "Attend Halcyon refactor standup",
		Description: "The user joined a Zoom meeting titled Halcyon refactor standup and waited for the host.",
		Entities:    []string{"Zoom", "Google Chrome", "Halcyon refactor standup", "Priya"},
	}
	insertComposableTimelineRun(t, store, first, firstSummary)
	if _, err := tr.composeApplySegment(store, nil, first, firstSummary, 61_000); err != nil {
		t.Fatalf("compose standup activity: %v", err)
	}

	second := testRun("tg_standup_2", "Google Chrome", 62_000, 90_000, 28_000, 4)
	secondSummary := ActivityTimelineGroupSummary{
		GroupID:     second.ID,
		Title:       "Join Halcyon refactor standup",
		Description: "The user participated in the Halcyon refactor standup video call and adjusted meeting controls.",
		Entities:    []string{"Google Meet", "Halcyon refactor standup", "Alex"},
	}
	insertComposableTimelineRun(t, store, second, secondSummary)

	candidates, err := tr.activityAssignmentCandidates(store, second, secondSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if len(candidates) == 0 || candidates[0].ID != "act_tg_standup_1" {
		t.Fatalf("named session candidates = %+v, want existing standup activity first", candidates)
	}
}

func TestActivityAssignmentCandidates_KeepsNewestDifferentSubjectForModelReview(t *testing.T) {
	tr := &ActivityTriager{}
	store := newTimelineTestStore(t)

	first := testRun("tg_standup_1", "Google Chrome", 1_000, 60_000, 59_000, 5)
	firstSummary := ActivityTimelineGroupSummary{
		GroupID:     first.ID,
		Title:       "Attend Halcyon refactor standup",
		Description: "The user joined a Zoom meeting titled Halcyon refactor standup.",
		Entities:    []string{"Zoom", "Halcyon refactor standup"},
	}
	insertComposableTimelineRun(t, store, first, firstSummary)
	if _, err := tr.composeApplySegment(store, nil, first, firstSummary, 61_000); err != nil {
		t.Fatalf("compose standup activity: %v", err)
	}

	second := testRun("tg_sales_1", "Google Chrome", 62_000, 90_000, 28_000, 4)
	secondSummary := ActivityTimelineGroupSummary{
		GroupID:     second.ID,
		Title:       "Join sales planning sync",
		Description: "The user joined a Zoom meeting titled sales planning sync.",
		Entities:    []string{"Zoom", "sales planning sync"},
	}
	insertComposableTimelineRun(t, store, second, secondSummary)

	candidates, err := tr.activityAssignmentCandidates(store, second, secondSummary)
	if err != nil {
		t.Fatalf("assignment candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != "act_tg_standup_1" {
		t.Fatalf("want latest open candidate for model review, got %+v", candidates)
	}
}

func TestGuardComposerAssignment_RequiresAdmissionEvidence(t *testing.T) {
	tr := &ActivityTriager{}
	candidates := []ActivityRow{{ID: "act_1"}}
	resp := composerResponse{
		Route:        "join",
		JoinBucketID: "act_1",
		Confidence:   "high",
		Reason:       "same_task_intent",
	}
	got := tr.guardComposerAssignment(resp, candidates)
	if got.Route != "new" || got.JoinBucketID != "" {
		t.Fatalf("join without admission = %+v, want downgraded new", got)
	}
}

func TestGuardComposerAssignment_AcceptsMultiDimensionalAdmission(t *testing.T) {
	tr := &ActivityTriager{}
	candidates := []ActivityRow{{ID: "act_1"}}
	resp := composerResponse{
		Route:        "join",
		JoinBucketID: "act_1",
		Confidence:   "high",
		Reason:       "same_task_intent",
		Admission: []composerAdmission{{
			BucketID:        "act_1",
			Decision:        "join",
			ObjectiveMatch:  "medium",
			SubjectMatch:    "medium",
			StateContinuity: "weak",
		}},
	}
	got := tr.guardComposerAssignment(resp, candidates)
	if got.Route != "join" || got.JoinBucketID != "act_1" {
		t.Fatalf("join with admission = %+v, want accepted join", got)
	}
}

func TestGuardComposerAssignment_ClearsMergeWithoutAdmission(t *testing.T) {
	tr := &ActivityTriager{}
	candidates := []ActivityRow{{ID: "act_1"}, {ID: "act_2"}}
	resp := composerResponse{
		Route:      "new",
		Merge:      []string{"act_1", "act_2"},
		Confidence: "high",
		Reason:     "same_task_intent",
	}
	got := tr.guardComposerAssignment(resp, candidates)
	if len(got.Merge) != 0 {
		t.Fatalf("merge without admission = %+v, want cleared merge", got.Merge)
	}
}

func TestGuardComposerAssignment_RejectsStrongNegativeEvidence(t *testing.T) {
	tr := &ActivityTriager{}
	candidates := []ActivityRow{{ID: "act_1"}}
	resp := composerResponse{
		Route:        "join",
		JoinBucketID: "act_1",
		Confidence:   "high",
		Reason:       "same_task_intent",
		Admission: []composerAdmission{{
			BucketID:         "act_1",
			Decision:         "join",
			ObjectiveMatch:   "strong",
			SubjectMatch:     "strong",
			StateContinuity:  "strong",
			NegativeEvidence: []string{"strong negative: activity UI history review"},
		}},
	}
	got := tr.guardComposerAssignment(resp, candidates)
	if got.Route != "new" || got.JoinBucketID != "" {
		t.Fatalf("join with strong negative = %+v, want downgraded new", got)
	}
}

func hasActivityCandidate(candidates []ActivityRow, id string) bool {
	for _, candidate := range candidates {
		if candidate.ID == id {
			return true
		}
	}
	return false
}

func TestComposeFindContinuation_ConcreteAnchorsOnly(t *testing.T) {
	store := newTimelineTestStore(t)
	priorDay := startOfDayLocal(1_000)
	nextDay := priorDay + 24*60*60*1000

	if err := store.InsertActivity(ActivityRow{
		ID: "act_topic", DayStartMs: priorDay, Status: activityStatusSettled,
		Title: "Work in Synara", Entities: composeJSON([]Entity{{Type: entityTopic, Name: "Synara"}}),
		RunIDs: "[]", ActiveMsByApp: "{}", StartMs: 1_000, EndMs: 2_000, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("insert topic prior: %v", err)
	}
	if got := composeFindContinuation(store, nextDay, []Entity{{Type: entityTopic, Name: "Synara"}}); got != "" {
		t.Fatalf("topic continuation = %q, want none", got)
	}

	if err := store.InsertActivity(ActivityRow{
		ID: "act_repo", DayStartMs: priorDay, Status: activityStatusSettled,
		Title: "Fix yansu bug", Entities: composeJSON([]Entity{{Type: entityRepo, Name: "isoform/yansu"}}),
		RunIDs: "[]", ActiveMsByApp: "{}", StartMs: 3_000, EndMs: 4_000, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("insert repo prior: %v", err)
	}
	if got := composeFindContinuation(store, nextDay, []Entity{{Type: entityRepo, Name: "isoform/yansu"}}); got != "act_repo" {
		t.Fatalf("repo continuation = %q, want act_repo", got)
	}
}

func insertComposableTimelineRun(t *testing.T, store *ActivityStore, run ActivityTimelineRunRow, summary ActivityTimelineGroupSummary) {
	t.Helper()
	if run.FirstEventRowID == 0 {
		run.FirstEventRowID = run.StartMs
	}
	if run.LastEventRowID == 0 {
		run.LastEventRowID = run.FirstEventRowID
	}
	if run.Status == "" {
		run.Status = timelineRunStatusClosed
	}
	if run.SummaryStatus == "" {
		run.SummaryStatus = timelineSummaryStatusDone
	}
	if run.EventCount == 0 {
		run.EventCount = run.SummarizedEventCount
	}
	if run.EventCount == 0 {
		run.EventCount = 1
	}
	if run.SessionIDs == nil {
		run.SessionIDs = []string{}
	}
	if run.TriggerKinds == nil {
		run.TriggerKinds = []string{}
	}
	if err := insertTimelineRun(store.db, run, 0); err != nil {
		t.Fatalf("insert run %s: %v", run.ID, err)
	}
	if err := store.UpsertTimelineGroupSummary(timelineRunToGroup(run, nil), summary, "test", "", ""); err != nil {
		t.Fatalf("insert summary %s: %v", run.ID, err)
	}
	if err := store.MarkTimelineRunSummaryDone(run.ID, run.SummarizedEventCount); err != nil {
		t.Fatalf("mark summary done %s: %v", run.ID, err)
	}
}
