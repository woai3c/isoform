package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestActivityThreadStretchesGapsAndActiveTime(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	for _, moment := range []threadFixtureMoment{
		{id: "brief", title: "Fix morning briefing generation in isoform hub session", app: "Claude", start: at(day, 9, 10), end: at(day, 9, 20), entities: []Entity{{Type: entityRepo, Name: "isoform"}}},
		{id: "hats", title: "Research wearable hat devices in ChatGPT", app: "ChatGPT", start: at(day, 9, 21), end: at(day, 9, 27), entities: []Entity{{Type: entityTopic, Name: "Research wearable hats"}}},
		{id: "workers", title: "Coordinate Claude workers in the isoform hub Claude Code session", app: "Claude", start: at(day, 9, 30), end: at(day, 9, 34), entities: []Entity{{Type: entityRepo, Name: "isoform"}}},
	} {
		seedThreadMoment(t, app, moment)
	}
	app.runner = &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"briefing":     {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform work"},
		"workers":      {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform work"},
		"wearable hat": {thread: "new1", kind: "work", confidence: "high", title: "Wearable research"},
	})}
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, at(day, 10, 0)); err != nil {
		t.Fatal(err)
	}
	isoformID, hatsID := threadOf(t, app, "brief"), threadOf(t, app, "hats")
	if isoformID == "" || isoformID != threadOf(t, app, "workers") || hatsID == "" || hatsID == isoformID {
		t.Fatalf("grouping changed: isoform=%q hats=%q workers=%q", isoformID, hatsID, threadOf(t, app, "workers"))
	}
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 2 {
		t.Fatalf("threads = %+v", got.Threads)
	}
	isoform := got.Threads[0]
	if isoform.ID != isoformID || strings.Join(isoform.ActivityIDs, ",") != "brief,workers" {
		t.Fatalf("recorded thread changed: %+v", isoform)
	}
	if len(isoform.Stretches) != 2 || len(isoform.Gaps) != 1 {
		t.Fatalf("stretches=%+v gaps=%+v", isoform.Stretches, isoform.Gaps)
	}
	first, second := isoform.Stretches[0], isoform.Stretches[1]
	if strings.Join(first.ActivityIDs, ",") != "brief" || strings.Join(second.ActivityIDs, ",") != "workers" {
		t.Fatalf("stretch membership = %+v", isoform.Stretches)
	}
	if first.Start != at(day, 9, 10).UnixMilli() || first.End != at(day, 9, 20).UnixMilli() || second.Start != at(day, 9, 30).UnixMilli() || second.End != at(day, 9, 34).UnixMilli() {
		t.Fatalf("stretch bounds = %+v", isoform.Stretches)
	}
	gap := isoform.Gaps[0]
	if gap.Start != first.End || gap.End != second.Start || gap.Minutes != 10 || gap.Label != "Wearable research" || gap.ThreadID != hatsID {
		t.Fatalf("gap = %+v", gap)
	}
	if isoform.LinkReason.Kind != "rule" || strings.Join(isoform.LinkReason.Keys, ",") != "file:isoform" || isoform.LinkReason.ResumedWithinMinutes == nil || *isoform.LinkReason.ResumedWithinMinutes != 10 {
		t.Fatalf("link reason = %+v", isoform.LinkReason)
	}
	hats := got.Threads[1]
	if hats.LinkReason.Kind != "model" || hats.LinkReason.Text != "test" {
		t.Fatalf("model link reason = %+v", hats.LinkReason)
	}
	if !hats.Interruption || isoform.Interruption {
		t.Fatalf("interruption flags: isoform=%v hats=%v", isoform.Interruption, hats.Interruption)
	}

	spent, total, err := app.activityMomentTimeMs(context.Background(), dayMs, dayMs+activityThreadDayMs)
	if err != nil {
		t.Fatal(err)
	}
	if isoform.ActiveMs != spent["brief"]+spent["workers"] || isoform.ActiveMs != isoform.TimeSpentMs || first.ActiveMs != spent["brief"] || second.ActiveMs != spent["workers"] || first.ActiveMs+second.ActiveMs != isoform.ActiveMs {
		t.Fatalf("active time: thread=%d spent=%d stretches=%d,%d", isoform.ActiveMs, isoform.TimeSpentMs, first.ActiveMs, second.ActiveMs)
	}
	var activeTotal int64
	for _, thread := range got.Threads {
		activeTotal += thread.ActiveMs
	}
	summary, err := app.GetDailySummary(day.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if activeTotal != total || total != summary.TotalActiveMs {
		t.Fatalf("day active time changed: threads=%d base=%d summary=%d", activeTotal, total, summary.TotalActiveMs)
	}
}

func seedActivityThreadEvidenceFixture(t *testing.T) (*App, ActivityRow, ActivityRow, int64) {
	t.Helper()
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	first := seedThreadMoment(t, app, threadFixtureMoment{id: "first", title: "Edit isoform activity code", app: "Claude", start: at(day, 9, 0), end: at(day, 9, 5), entities: []Entity{{Type: entityRepo, Name: "isoform"}}})
	second := seedThreadMoment(t, app, threadFixtureMoment{id: "second", title: "Review isoform activity code", app: "Claude", start: at(day, 9, 6), end: at(day, 9, 10), entities: []Entity{{Type: entityRepo, Name: "isoform"}}})
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, false, at(day, 10, 0)); err != nil {
		t.Fatal(err)
	}
	return app, first, second, dayMs
}

func setActivityThreadEvidence(t *testing.T, app *App, row ActivityRow, excluded bool, tokens ...ActivityEvidenceToken) {
	t.Helper()
	runID := composeParseStrings(row.RunIDs)[0]
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens = ?, evidence_excluded = ? WHERE id = ?`, composeJSON(tokens), excluded, runID); err != nil {
		t.Fatal(err)
	}
}

func activityThreadViewForMoment(t *testing.T, app *App, dayMs int64, activityID string) ActivityThreadView {
	t.Helper()
	activityThreadCacheForget(dayMs)
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	threadID := threadOf(t, app, activityID)
	for _, thread := range got.Threads {
		if thread.ID == threadID {
			return thread
		}
	}
	t.Fatalf("thread %q missing from %+v", threadID, got.Threads)
	return ActivityThreadView{}
}

func TestActivityThreadResumePointPriorityAndPrivacy(t *testing.T) {
	t.Run("last moment and specificity", func(t *testing.T) {
		app, first, second, dayMs := seedActivityThreadEvidenceFixture(t)
		setActivityThreadEvidence(t, app, first, false, ActivityEvidenceToken{Kind: "pr", Value: "example/isoform#1", LastSeenMs: first.EndMs})
		setActivityThreadEvidence(t, app, second, false,
			ActivityEvidenceToken{Kind: "repo", Value: "example/isoform", LastSeenMs: second.EndMs},
			ActivityEvidenceToken{Kind: "figure", Value: "42 gb", LastSeenMs: second.EndMs},
			ActivityEvidenceToken{Kind: "path", Value: "~/src/isoform/activity_threads.go", LastSeenMs: second.EndMs},
			ActivityEvidenceToken{Kind: "pr", Value: "example/isoform#2", LastSeenMs: second.EndMs})
		thread := activityThreadViewForMoment(t, app, dayMs, second.ID)
		if len(thread.Stretches) != 1 || thread.Stretches[0].ResumePoint == nil || *thread.Stretches[0].ResumePoint != (ActivityThreadResumePoint{Kind: "pr", Label: "example/isoform#2"}) {
			t.Fatalf("resume point = %+v", thread.Stretches)
		}
	})
	t.Run("redacted evidence skipped", func(t *testing.T) {
		app, first, second, dayMs := seedActivityThreadEvidenceFixture(t)
		setActivityThreadEvidence(t, app, first, false, ActivityEvidenceToken{Kind: "pr", Value: "example/isoform#1", LastSeenMs: first.EndMs})
		setActivityThreadEvidence(t, app, second, false,
			ActivityEvidenceToken{Kind: "path", Value: "/tmp/" + activityPrivacySecret + "/file.go", LastSeenMs: second.EndMs},
			ActivityEvidenceToken{Kind: "figure", Value: "42 gb", LastSeenMs: second.EndMs},
			ActivityEvidenceToken{Kind: "repo", Value: "example/isoform", LastSeenMs: second.EndMs})
		thread := activityThreadViewForMoment(t, app, dayMs, second.ID)
		if len(thread.Stretches) != 1 || thread.Stretches[0].ResumePoint == nil || *thread.Stretches[0].ResumePoint != (ActivityThreadResumePoint{Kind: "figure", Label: "42 gb"}) {
			t.Fatalf("redacted resume point = %+v", thread.Stretches)
		}
		raw, _ := json.Marshal(thread)
		if strings.Contains(string(raw), activityPrivacySecret) {
			t.Fatal("redacted evidence reached the thread view")
		}
	})
	t.Run("excluded evidence skipped", func(t *testing.T) {
		app, first, second, dayMs := seedActivityThreadEvidenceFixture(t)
		setActivityThreadEvidence(t, app, first, false, ActivityEvidenceToken{Kind: "path", Value: "~/src/isoform/first.go", LastSeenMs: first.EndMs})
		setActivityThreadEvidence(t, app, second, true, ActivityEvidenceToken{Kind: "pr", Value: "example/isoform#2", LastSeenMs: second.EndMs})
		thread := activityThreadViewForMoment(t, app, dayMs, second.ID)
		if len(thread.Stretches) != 1 || thread.Stretches[0].ResumePoint != nil {
			t.Fatalf("excluded resume point = %+v", thread.Stretches)
		}
	})
	t.Run("allowed run used after excluded run", func(t *testing.T) {
		day, _ := threadTestDay(t)
		app, _, second, _ := seedActivityThreadEvidenceFixture(t)
		other := seedThreadMoment(t, app, threadFixtureMoment{id: "other", title: "Review another editor run", app: "Claude", start: at(day, 9, 20), end: at(day, 9, 25)})
		setActivityThreadEvidence(t, app, second, true, ActivityEvidenceToken{Kind: "path", Value: "~/src/isoform/excluded.go", LastSeenMs: second.EndMs})
		setActivityThreadEvidence(t, app, other, false, ActivityEvidenceToken{Kind: "pr", Value: "example/isoform#3", LastSeenMs: other.EndMs})
		second.RunIDs = composeJSON(append(composeParseStrings(second.RunIDs), composeParseStrings(other.RunIDs)...))
		point, err := app.activityThreadResumePoint(context.Background(), second)
		if err != nil || point == nil || *point != (ActivityThreadResumePoint{Kind: "pr", Label: "example/isoform#3"}) {
			t.Fatalf("mixed-run resume point = %+v, %v", point, err)
		}
	})
	t.Run("model filtered moment skipped", func(t *testing.T) {
		app, _, second, _ := seedActivityThreadEvidenceFixture(t)
		setActivityThreadEvidence(t, app, second, false, ActivityEvidenceToken{Kind: "pr", Value: "example/isoform#2", LastSeenMs: second.EndMs})
		second.ActiveMsByApp = `{"Private Notes":1000}`
		point, err := app.activityThreadResumePoint(context.Background(), second)
		if err != nil || point != nil {
			t.Fatalf("filtered resume point = %+v, %v", point, err)
		}
	})
}

func TestConfirmActivityThreadLink(t *testing.T) {
	app, _, second, dayMs := seedActivityThreadEvidenceFixture(t)
	threadID := threadOf(t, app, second.ID)
	if thread := activityThreadViewForMoment(t, app, dayMs, second.ID); thread.Reviewed {
		t.Fatal("rule memberships unexpectedly reviewed")
	}
	if err := app.ConfirmActivityThreadLink(threadID); err != nil {
		t.Fatal(err)
	}
	var reviewed int
	if err := app.activity.store.db.QueryRow(`SELECT COUNT(*) FROM activity_thread_members WHERE thread_id = ? AND reviewed = 1`, threadID).Scan(&reviewed); err != nil || reviewed != 0 {
		t.Fatalf("reviewed memberships = %d, %v", reviewed, err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_thread_members SET updated_at = 123 WHERE thread_id = ?`, threadID); err != nil {
		t.Fatal(err)
	}
	if err := app.ConfirmActivityThreadLink(threadID); err != nil {
		t.Fatal(err)
	}
	var unchanged int
	if err := app.activity.store.db.QueryRow(`SELECT COUNT(*) FROM activity_thread_members WHERE thread_id = ? AND updated_at = 123`, threadID).Scan(&unchanged); err != nil || unchanged != 2 {
		t.Fatalf("idempotent confirmation rewrote memberships: %d, %v", unchanged, err)
	}
	if thread := activityThreadViewForMoment(t, app, dayMs, second.ID); !thread.Reviewed {
		t.Fatal("confirmed thread is not reviewed")
	}
	if err := app.ConfirmActivityThreadLink("missing"); err == nil {
		t.Fatal("missing thread confirmation succeeded")
	}
}

func TestActivityThreadRenameTeachesNaming(t *testing.T) {
	day, _ := threadTestDay(t)
	app, first, second, dayMs := seedActivityThreadEvidenceFixture(t)
	threadID := threadOf(t, app, second.ID)
	if err := app.RenameActivityThread(threadID, "Hub workers"); err != nil {
		t.Fatal(err)
	}
	if err := app.RenameActivityThread(threadID, "Hub workers final"); err != nil {
		t.Fatal(err)
	}
	seedThreadMoment(t, app, threadFixtureMoment{id: "hats", title: "Research wearable hat devices in ChatGPT", app: "ChatGPT", start: at(day, 10, 0), end: at(day, 10, 5)})
	runner := &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"wearable hat": {thread: "new1", kind: "work", confidence: "high", title: "Wearable research"},
	})}
	app.runner = runner
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 || strings.Count(runner.prompts[0], `- renamed a thread to "Hub workers final"`) != 1 || strings.Contains(runner.prompts[0], `"Hub workers"`) {
		t.Fatalf("rename missing or duplicated in naming examples: %v", runner.prompts)
	}
	if threadOf(t, app, first.ID) != threadID {
		t.Fatal("rename changed membership")
	}
}

func TestActivityThreadUnfinishedFromReadableOutcome(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, app, "unfinished", fixture, at(day, 18, 0))
	row.DayStartMs = dayMs
	if err := app.activity.store.UpdateActivity(row); err != nil {
		t.Fatal(err)
	}
	runID := composeParseStrings(row.RunIDs)[0]
	if err := app.activity.store.UpsertActivityRunLink(runID, row.ID, 1, row.EndMs); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := app.activity.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	threadID := threadIDForMoment(row.ID)
	if err := insertActivityThread(ctx, tx, threadID, dayMs, "Sailing gloves", nil, threadKindWork, row.EndMs); err != nil {
		t.Fatal(err)
	}
	if err := upsertActivityThreadMember(ctx, tx, row.ID, threadID, dayMs, threadSourceAI, "test", true, row.EndMs); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Unfinished {
		t.Fatalf("unexpected outcome before extraction: %+v, %v", got.Threads, err)
	}
	row = saveFixtureOutcome(t, app, row, fixture)
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || !got.Threads[0].Unfinished {
		t.Fatalf("unfinished outcome not projected through warm cache: %+v, %v", got.Threads, err)
	}
	app.saveObservationPauses([]observationPause{{Start: row.EndMs + 1}})
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Unfinished {
		t.Fatalf("paused outcome survived warm cache: %+v, %v", got.Threads, err)
	}
	app.saveObservationPauses(nil)
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || !got.Threads[0].Unfinished {
		t.Fatalf("outcome did not recover after pause: %+v, %v", got.Threads, err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activities SET thread_outcome = '' WHERE id = ?`, row.ID); err != nil {
		t.Fatal(err)
	}
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Unfinished {
		t.Fatalf("withdrawn outcome survived warm cache: %+v, %v", got.Threads, err)
	}
}

func TestActivityThreadGapLabelFallsBackToAppAndAway(t *testing.T) {
	row := ActivityRow{ID: "loose", StartMs: 1000, EndMs: 2000, ActiveMsByApp: `{"Notes":1000}`}
	label, threadID := activityThreadGapLabel(0, 3000, "current", []ActivityRow{row}, nil, nil, nil)
	if label != "Notes" || threadID != "" {
		t.Fatalf("app gap = %q, %q", label, threadID)
	}
	label, threadID = activityThreadGapLabel(0, 3000, "current", nil, nil, nil, nil)
	if label != "away" || threadID != "" {
		t.Fatalf("away gap = %q, %q", label, threadID)
	}
}

func TestActivityThreadGapLabelUnionsOverlappingMoments(t *testing.T) {
	minute := int64(time.Minute / time.Millisecond)
	rows := []ActivityRow{
		{ID: "b1", StartMs: 0, EndMs: 4 * minute},
		{ID: "b2", StartMs: 0, EndMs: 4 * minute},
		{ID: "c1", StartMs: 0, EndMs: 6 * minute},
	}
	members := map[string]activityThreadMember{
		"b1": {ActivityID: "b1", ThreadID: "b"},
		"b2": {ActivityID: "b2", ThreadID: "b"},
		"c1": {ActivityID: "c1", ThreadID: "c"},
	}
	label, threadID := activityThreadGapLabel(0, 10*minute, "current", rows, members, map[string]int{"b": 1, "c": 2}, map[string]string{"b": "B thread", "c": "C thread"})
	if label != "C thread" || threadID != "c" {
		t.Fatalf("overlapping gap = %q, %q", label, threadID)
	}
}

func TestActivityThreadCacheRefreshesEvidenceAndPrivacy(t *testing.T) {
	app, _, second, dayMs := seedActivityThreadEvidenceFixture(t)
	setActivityThreadEvidence(t, app, second, false, ActivityEvidenceToken{Kind: "pr", Value: "example/isoform#2", LastSeenMs: second.EndMs})
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Stretches[0].ResumePoint == nil || got.Threads[0].Stretches[0].ResumePoint.Kind != "pr" {
		t.Fatalf("initial cached resume point = %+v, %v", got.Threads, err)
	}
	setActivityThreadEvidence(t, app, second, false, ActivityEvidenceToken{Kind: "path", Value: "~/src/isoform/changed.go", LastSeenMs: second.EndMs})
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Stretches[0].ResumePoint == nil || got.Threads[0].Stretches[0].ResumePoint.Label != "~/src/isoform/changed.go" {
		t.Fatalf("updated evidence survived in cache: %+v, %v", got.Threads, err)
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 0 {
		t.Fatalf("Closed Eyes returned cached threads: %+v, %v", got.Threads, err)
	}
	app.closedEyes.enabled.Store(false)
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Stretches[0].ResumePoint == nil || got.Threads[0].Stretches[0].ResumePoint.Label != "~/src/isoform/changed.go" {
		t.Fatalf("readable evidence did not recover: %+v, %v", got.Threads, err)
	}
}

func TestActivityThreadGapLabelUsesDominantApp(t *testing.T) {
	rows := []ActivityRow{{ID: "loose", StartMs: 0, EndMs: 10000, ActiveMsByApp: `{"Browser":1000,"Terminal":9000}`}}
	label, threadID := activityThreadGapLabel(0, 10000, "current", rows, nil, nil, nil)
	if label != "Terminal" || threadID != "" {
		t.Fatalf("dominant app = %q, %q", label, threadID)
	}
}

func TestActivityThreadLinkReasonRuleKeysAndUserSource(t *testing.T) {
	view := ActivityThreadView{ActivityIDs: []string{"file", "pr", "person", "channel", "site", "duplicate", "unknown"}, Gaps: []ActivityThreadGap{{Minutes: 40}, {Minutes: 5}}}
	members := map[string]activityThreadMember{}
	for id, reason := range map[string]string{"file": "obj:src/activity.go", "pr": "pr:owner/repo#12", "person": "person:teammate", "channel": "channel:development", "site": "site:example.com", "duplicate": "pr:owner/repo#12", "unknown": "unsupported:key"} {
		members[id] = activityThreadMember{Source: threadSourceRule, Reason: reason}
	}
	reason := activityThreadLinkReason(view, members)
	if reason.Kind != "rule" || strings.Join(reason.Keys, ",") != "file:src/activity.go,pr:owner/repo#12,person:teammate,channel:development,site:example.com" || reason.Text != "" || reason.ResumedWithinMinutes == nil || *reason.ResumedWithinMinutes != 5 {
		t.Fatalf("rule reason = %+v", reason)
	}
	view.ActivityIDs = append(view.ActivityIDs, "moved")
	members["moved"] = activityThreadMember{Source: threadSourceUser, Reason: "move", Reviewed: true}
	reason = activityThreadLinkReason(view, members)
	if reason.Kind != "user" || len(reason.Keys) != 0 || reason.Text != "" {
		t.Fatalf("user reason = %+v", reason)
	}
}

func TestActivityThreadStretchDoesNotSplitOnTime(t *testing.T) {
	app, _, second, dayMs := seedActivityThreadEvidenceFixture(t)
	day, _ := threadTestDay(t)
	second.StartMs, second.EndMs = at(day, 16, 0).UnixMilli(), at(day, 16, 5).UnixMilli()
	if err := app.activity.store.UpdateActivity(second); err != nil {
		t.Fatal(err)
	}
	thread := activityThreadViewForMoment(t, app, dayMs, second.ID)
	if len(thread.Stretches) != 1 || len(thread.Gaps) != 0 || len(thread.Stretches[0].ActivityIDs) != 2 || thread.LinkReason.ResumedWithinMinutes != nil {
		t.Fatalf("time alone split a stretch: %+v", thread)
	}
}

func TestActivityThreadRenameExamplesWithholdPrivateAndDeletedMembers(t *testing.T) {
	app, first, second, _ := seedActivityThreadEvidenceFixture(t)
	threadID := threadOf(t, app, second.ID)
	if err := app.RenameActivityThread(threadID, "Hub workers"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	examples, err := app.activity.store.activityThreadCorrectionExamples(ctx, map[string]ActivityRow{first.ID: first, second.ID: second})
	if err != nil || len(examples) != 1 || examples[0].RenamedTitle != "Hub workers" {
		t.Fatalf("readable rename = %+v, %v", examples, err)
	}
	examples, err = app.activity.store.activityThreadCorrectionExamples(ctx, map[string]ActivityRow{})
	if err != nil || len(examples) != 0 {
		t.Fatalf("private rename = %+v, %v", examples, err)
	}
	if _, err := app.activity.store.db.Exec(`DELETE FROM activity_thread_members WHERE thread_id = ?`, threadID); err != nil {
		t.Fatal(err)
	}
	examples, err = app.activity.store.activityThreadCorrectionExamples(ctx, map[string]ActivityRow{first.ID: first, second.ID: second})
	if err != nil || len(examples) != 0 {
		t.Fatalf("orphaned rename = %+v, %v", examples, err)
	}
}

func TestActivityThreadGapLabelUnionsOverlappingAppMoments(t *testing.T) {
	rows := []ActivityRow{
		{ID: "thread", StartMs: 0, EndMs: 6000},
		{ID: "loose1", StartMs: 6000, EndMs: 10000, ActiveMsByApp: `{"Terminal":4000}`},
		{ID: "loose2", StartMs: 6000, EndMs: 10000, ActiveMsByApp: `{"Terminal":4000}`},
	}
	label, threadID := activityThreadGapLabel(0, 10000, "current", rows,
		map[string]activityThreadMember{"thread": {ThreadID: "other"}},
		map[string]int{"other": 1}, map[string]string{"other": "Other work"})
	if label != "Other work" || threadID != "other" {
		t.Fatalf("overlapping app time beat longer thread: %q, %q", label, threadID)
	}
}

func TestActivityThreadModelReasonsRedactAndWithdrawWithContext(t *testing.T) {
	for _, withdrawal := range []string{"protected", "deleted", "edited"} {
		t.Run(withdrawal, func(t *testing.T) {
			app := newThreadTestApp(t)
			day, dayMs := threadTestDay(t)
			first := seedThreadMoment(t, app, threadFixtureMoment{id: "first", title: "Research wearable hats", app: "ChatGPT", start: at(day, 9, 0), end: at(day, 9, 5)})
			second := seedThreadMoment(t, app, threadFixtureMoment{id: "second", title: "Check sailing gloves", app: "Safari", start: at(day, 9, 10), end: at(day, 9, 15)})
			reply := threadReply(map[string]threadAnswer{
				"wearable hats":  {thread: "new1", kind: "work", confidence: "high", title: "Hats"},
				"sailing gloves": {thread: "new2", kind: "work", confidence: "high", title: "Gloves"},
			})
			app.runner = &threadTestRunner{reply: func(prompt string) string {
				var response threadModelResponse
				if err := json.Unmarshal([]byte(reply(prompt)), &response); err != nil {
					t.Fatal(err)
				}
				for i := range response.Moments {
					response.Moments[i].Reason = "Research wearable hats " + activityPrivacySecret
				}
				raw, _ := json.Marshal(response)
				return string(raw)
			}}
			if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, time.Now()); err != nil {
				t.Fatal(err)
			}
			got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
			if err != nil || len(got.Threads) != 2 {
				t.Fatalf("initial read: %+v, %v", got, err)
			}
			reason := got.Threads[1].LinkReason
			if reason.Kind != "model" || !strings.Contains(reason.Text, "wearable hats") || strings.Contains(reason.Text, activityPrivacySecret) {
				t.Fatalf("model reason not safely preserved: %+v", reason)
			}
			switch withdrawal {
			case "protected":
				if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status = 'protected' WHERE id = ?`, "session-"+first.ID); err != nil {
					t.Fatal(err)
				}
			case "edited":
				if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data = json_set(data, '$.windowTitle', 'Updated observation') WHERE session_id = ?`, "session-"+first.ID); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if _, err := app.activity.store.db.Exec(`DELETE FROM activities WHERE id = ?`, first.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := app.activity.store.db.Exec(`DELETE FROM activity_thread_members WHERE activity_id = ?`, first.ID); err != nil {
					t.Fatal(err)
				}
			}
			got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
			expectedThreads := 1
			if withdrawal == "edited" {
				expectedThreads = 2
			}
			if err != nil || len(got.Threads) != expectedThreads || got.Threads[len(got.Threads)-1].LinkReason.Kind != "model" || got.Threads[len(got.Threads)-1].LinkReason.Text != "" {
				t.Fatalf("withdrawn explanation survived warm cache: %+v, %v", got, err)
			}
			// Legacy reasons lack a complete input context and are withheld.
			if _, err := app.activity.store.db.Exec(`UPDATE activity_thread_members SET reason = ? WHERE activity_id = ?`, "Research wearable hats", second.ID); err != nil {
				t.Fatal(err)
			}
			activityThreadCacheForget(dayMs)
			got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
			if err != nil || len(got.Threads) != expectedThreads || got.Threads[len(got.Threads)-1].LinkReason.Text != "" {
				t.Fatalf("legacy explanation exposed: %+v, %v", got, err)
			}
		})
	}
}

func TestActivityThreadLinkReasonSkipsSecretRuleKeys(t *testing.T) {
	reason := activityThreadLinkReason(ActivityThreadView{ActivityIDs: []string{"secret", "safe"}}, map[string]activityThreadMember{
		"secret": {Source: threadSourceRule, Reason: "obj:/tmp/" + activityPrivacySecret + "/file.go"},
		"safe":   {Source: threadSourceRule, Reason: "pr:owner/repo#1"},
	})
	if strings.Join(reason.Keys, ",") != "pr:owner/repo#1" {
		t.Fatalf("unsafe rule keys: %+v", reason)
	}
}

func TestActivityThreadReasonUsesProviderTitleSnapshot(t *testing.T) {
	app, first, second, dayMs := seedActivityThreadEvidenceFixture(t)
	threadID := threadOf(t, app, second.ID)
	if err := app.RenameActivityThread(threadID, "Withdrawn context"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The pass has captured its thread states, then a concurrent rename clears
	// that input before the remaining reason context is collected.
	threads, _, err := app.activity.store.loadActivityThreads(ctx, dayMs)
	if err != nil {
		t.Fatal(err)
	}
	titleSnapshot := activityThreadReasonTitleHashes(threads)
	if err := app.RenameActivityThread(threadID, ""); err != nil {
		t.Fatal(err)
	}
	contextHash, err := app.currentActivityThreadReasonContext(ctx, dayMs, []ActivityRow{first, second})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := app.activity.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	record := activityThreadStoredReason{Version: 2, Context: contextHash}
	if err := upsertActivityThreadMember(ctx, tx, second.ID, threadID, dayMs, threadSourceAI, "Withdrawn context", true, time.Now().UnixMilli(), record); err != nil {
		t.Fatal(err)
	}
	aux, err := app.activityThreadReasonAux(ctx)
	if err != nil {
		t.Fatal(err)
	}
	basis, _ := json.Marshal(activityThreadStoredReason{Version: 2, Context: contextHash, Titles: titleSnapshot, Aux: aux, Sources: contextHash})
	if _, err := tx.ExecContext(ctx, `UPDATE activity_threads SET reason_basis=? WHERE id=?`, string(basis), threadID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	activityThreadCacheForget(dayMs)
	for i := 0; i < 2; i++ {
		got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
		if err != nil || len(got.Threads) != 1 || got.Threads[0].LinkReason.Kind != "model" || got.Threads[0].LinkReason.Text != "" {
			t.Fatalf("old provider title exposed (read %d): %+v, %v", i, got, err)
		}
	}
}
