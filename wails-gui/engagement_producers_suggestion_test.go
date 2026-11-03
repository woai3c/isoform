package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

// suggestionTestApp wires an engagement rig to a real activity store and a
// handoff store, the three things the suggestion producer touches. The data
// folder override keeps the memory file a decision writes (dismiss and
// do_once sync the initiative's learning memory) inside a per-test temporary
// directory even when the package TestMain isolation is not in effect.
func suggestionTestApp(t *testing.T) (*App, *engagementTestRig, *ActivityStore) {
	t.Helper()
	t.Setenv(agentHomeEnv, t.TempDir())
	rig := newEngagementTestRig(t)
	activity, err := NewActivityStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activity.Close() })
	handoffs, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: rig.store, engagement: rig.svc, activity: &activityMonitorState{store: activity}, handoffStore: handoffs}
	return app, rig, activity
}

// saveProposedAutomation stores a proposed automation initiative with one
// observation per day listed, each with a quote, so the gate can count days.
func saveProposedAutomation(t *testing.T, store *ActivityStore, id, name string, confidence float64, scheduleKind string, days []time.Time) Initiative {
	t.Helper()
	evidence := make([]InitiativeEvidence, 0, len(days))
	for i, day := range days {
		evidence = append(evidence, InitiativeEvidence{Kind: "activity", Quote: "Drafted the " + name + " on day " + string(rune('A'+i)), OccurredAt: day.UnixMilli()})
	}
	plan := InitiativeExecutionPlan{Kind: "automation", Automation: &AutomationExecutionPlan{
		Name: name, Description: "Send the " + name, Prompt: "Draft and send the " + name, ScheduleKind: scheduleKind,
		ScheduleConfig: json.RawMessage(`{}`), ScheduleEvidence: "every Monday morning",
	}}
	if scheduleKind == "cron" {
		plan.Automation.ScheduleConfig = json.RawMessage(`{"expr":"0 9 * * 1"}`)
	}
	item := Initiative{
		ID: id, Kind: "automation", Status: InitiativeStatusProposed, AutonomyMode: InitiativeModeApproval,
		SourceKind: "activity", SourceID: id + "-source-0", Title: name, Description: "Send the " + name,
		Confidence: confidence, RiskLevel: "low", Reversibility: "reversible", Evidence: evidence[:1],
		ExecutionPlan: plan, Fingerprint: "fp-" + id, MemoryState: InitiativeMemoryEligible,
		CreatedAt: days[0].UnixMilli(), UpdatedAt: days[0].UnixMilli(),
	}
	if err := store.SaveInitiative(item); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(days); i++ {
		if _, err := store.db.Exec(`INSERT INTO initiative_observations (initiative_id, source_kind, source_id, evidence_json, confidence,
			confidence_adjustment_json, observed_at, predicted_action, rationale) VALUES (?, 'activity', ?, ?, ?, '{}', ?, '', '')`,
			id, id+"-source-"+string(rune('0'+i)), encodeInitiativeJSON([]InitiativeEvidence{evidence[i]}, "[]"), confidence, days[i].UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := store.GetInitiative(id)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func suggestionMoments(t *testing.T, rig *engagementTestRig) []storage.EngagementMomentRow {
	t.Helper()
	return openMoments(t, rig.store, string(engagement.KindSuggestion))
}

func TestSuggestionGateRaisesOneQualifiedPatternAWeek(t *testing.T) {
	app, rig, activity := suggestionTestApp(t)
	ctx := context.Background()
	day := func(offset int, hour int) time.Time {
		return rig.clock.AddDate(0, 0, -offset).Add(time.Duration(hour-10) * time.Hour)
	}

	// Three sightings on two days: not enough days.
	saveProposedAutomation(t, activity, "init-two-days", "status email", 0.9, "cron", []time.Time{day(2, 9), day(2, 15), day(1, 9)})
	// Two sightings on two days: not enough of either.
	saveProposedAutomation(t, activity, "init-two", "expense report", 0.95, "cron", []time.Time{day(3, 9), day(1, 9)})
	app.checkSuggestionEngagement(ctx, rig.clock)
	if got := suggestionMoments(t, rig); len(got) != 0 {
		t.Fatalf("unqualified patterns must not be suggested: %+v", got)
	}

	// Two qualified patterns: the more confident one goes first, once this week.
	saveProposedAutomation(t, activity, "init-weekly", "weekly report", 0.7, "cron", []time.Time{day(6, 9), day(4, 9), day(2, 9)})
	saveProposedAutomation(t, activity, "init-cleanup", "downloads cleanup", 0.8, "manual", []time.Time{day(5, 18), day(3, 18), day(1, 18)})
	app.checkSuggestionEngagement(ctx, rig.clock)
	app.checkSuggestionEngagement(ctx, rig.clock.Add(time.Hour))
	moments := suggestionMoments(t, rig)
	if len(moments) != 1 || moments[0].SubjectID != "init-cleanup" || moments[0].DedupeKey != engagement.DedupeKey(engagement.KindSuggestion, engagement.WeekKey(rig.clock)) {
		t.Fatalf("one suggestion for the most confident pattern this week, got %+v", moments)
	}
	var params map[string]string
	_ = json.Unmarshal([]byte(moments[0].Params), &params)
	if params["title"] != "downloads cleanup" || params["count"] != "3" {
		t.Fatalf("params = %v", params)
	}

	// Next week the other pattern is suggested; the first one never again. The
	// rig's clock is pinned (a Monday in 2026), so week keys never depend on
	// the wall clock, and moving it forward gives the second moment a later
	// creation time than the first.
	nextWeek := rig.clock.AddDate(0, 0, 7)
	rig.clock = nextWeek
	app.checkSuggestionEngagement(ctx, nextWeek)
	moments = suggestionMoments(t, rig)
	subjects := map[string]string{}
	for _, moment := range moments {
		subjects[moment.SubjectID] = moment.DedupeKey
	}
	if len(moments) != 2 || subjects["init-weekly"] != engagement.DedupeKey(engagement.KindSuggestion, engagement.WeekKey(nextWeek)) {
		t.Fatalf("the second week must suggest the remaining pattern: %+v", moments)
	}
	app.checkSuggestionEngagement(ctx, nextWeek.AddDate(0, 0, 7))
	if got := suggestionMoments(t, rig); len(got) != 2 {
		t.Fatalf("nothing new to suggest must raise nothing: %+v", got)
	}
}

func TestTodaySuggestionViewAndDecisions(t *testing.T) {
	app, rig, activity := suggestionTestApp(t)
	ctx := context.Background()
	day := func(offset int) time.Time { return rig.clock.AddDate(0, 0, -offset) }
	saveProposedAutomation(t, activity, "init-cleanup", "downloads cleanup", 0.8, "manual", []time.Time{day(5), day(3), day(1)})
	app.checkSuggestionEngagement(ctx, rig.clock)

	view, err := app.GetTodaySuggestion()
	if err != nil || !view.Available || view.ID != "init-cleanup" || view.Times != 3 || view.Days != 3 || view.Schedule != "once" {
		t.Fatalf("view = %+v %v", view, err)
	}
	if len(view.Evidence) != 3 || view.Evidence[0] != "Drafted the downloads cleanup on day C" {
		t.Fatalf("evidence newest first = %v", view.Evidence)
	}
	if _, err := app.ResolveTodaySuggestion(TodaySuggestionDecision{ID: "init-cleanup", Decision: "later"}); err == nil {
		t.Fatal("an unknown decision must be refused")
	}

	// Dismiss with a reason closes the initiative with that feedback and retires
	// the pending moment, so it is never delivered after the fact.
	after, err := app.ResolveTodaySuggestion(TodaySuggestionDecision{ID: "init-cleanup", Decision: "dismiss", Reason: "only for one project"})
	if err != nil || after.Available {
		t.Fatalf("after dismiss = %+v %v", after, err)
	}
	item, _ := activity.GetInitiative("init-cleanup")
	if item.Status != InitiativeStatusDismissed || item.LatestFeedback == nil || item.LatestFeedback.Reason != "only for one project" {
		t.Fatalf("dismissed initiative = %+v", item)
	}
	rows, _ := rig.store.ListEngagementMoments(ctx, storage.EngagementStateSuppressed, 0)
	if len(rows) != 1 || rows[0].Kind != "suggestion" {
		t.Fatalf("the open moment must be retired: %+v", rows)
	}
}

func TestTodaySuggestionAcceptCreatesWorkAndRecordsTheReceipt(t *testing.T) {
	app, rig, activity := suggestionTestApp(t)
	ctx := context.Background()
	day := func(offset int) time.Time { return rig.clock.AddDate(0, 0, -offset) }
	saveProposedAutomation(t, activity, "init-cleanup", "downloads cleanup", 0.8, "manual", []time.Time{day(5), day(3), day(1)})
	app.checkSuggestionEngagement(ctx, rig.clock)
	moments := suggestionMoments(t, rig)
	if len(moments) != 1 {
		t.Fatalf("want one moment, got %d", len(moments))
	}
	// The notice went out; accepting starts work but does not complete it.
	if err := rig.store.SetEngagementMomentState(ctx, moments[0].ID, storage.EngagementStateSent, "ok", storage.EngagementMomentRow{Channel: "push", SentAt: rig.clock.UnixMilli()}); err != nil {
		t.Fatal(err)
	}

	view, err := app.ResolveTodaySuggestion(TodaySuggestionDecision{ID: "init-cleanup", Decision: "do_once"})
	if err != nil || view.Available {
		t.Fatalf("after do_once = %+v %v", view, err)
	}
	item, _ := activity.GetInitiative("init-cleanup")
	if item.Status != InitiativeStatusApproved || item.ExecutionRef == "" {
		t.Fatalf("approved initiative = %+v", item)
	}
	entries, err := app.handoffStore.List()
	if err != nil || len(entries) != 1 || entries[0].Title != "downloads cleanup" || entries[0].SourceID != "init-cleanup" {
		t.Fatalf("one-time work = %+v %v", entries, err)
	}
	receipts, _ := rig.store.ListEngagementReceipts(ctx, moments[0].ID)
	interaction := receipts[len(receipts)-1]
	if len(receipts) != 3 || interaction.State != storage.EngagementReceiptOpened || interaction.Action != "do_once" {
		t.Fatalf("receipts = %+v", receipts)
	}
	counts, _ := rig.svc.WeekCounts(ctx)
	if counts.Sent != 1 || counts.ActedOn != 0 {
		t.Fatalf("the weekly line must wait for completion: %+v", counts)
	}
}
