package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func insertRowActionActivity(t *testing.T, app *App, id, title string, at time.Time) ActivityRow {
	t.Helper()
	row := ActivityRow{
		ID: id, DayStartMs: at.Add(-time.Hour).UnixMilli(), Status: activityStatusSettled, Title: title,
		Description: "Compared two finishes and picked one.", Entities: `[{"type":"person","name":"Priya"}]`,
		RunIDs: `[]`, ActiveMsByApp: `{"Safari":60000}`, StartMs: at.Add(-time.Hour).UnixMilli(), EndMs: at.UnixMilli(),
		CreatedAt: at.UnixMilli(), UpdatedAt: at.UnixMilli(),
	}
	if err := app.activity.store.InsertActivity(row); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestActivityFollowUpTextIsShortAndReadable(t *testing.T) {
	cases := []struct {
		title, person, lang, want string
	}{
		// A real AI title: only its first clause, in sentence case.
		{"Review and merge isoform PRs, direct Kimi workers, and plan desktop app release in the isoform hub", "", "en", "Follow up on review and merge isoform PRs"},
		{"Comparing Halcyon Arc desk lamps for Priya", "Priya", "en", "Follow up with Priya about comparing Halcyon Arc desk lamps for Priya"},
		// A first word with other capitals keeps its case.
		{"API docs cleanup; release notes", "", "en", "Follow up on API docs cleanup"},
		{"审查并合并 isoform PR，规划发布", "", "zh-CN", "跟进：审查并合并 isoform PR"},
		{"审查并合并 isoform PR", "张三", "zh-CN", "与张三跟进：审查并合并 isoform PR"},
		{"", "Priya", "en", ""},
	}
	for _, tc := range cases {
		if got := activityFollowUpText(tc.title, tc.person, tc.lang); got != tc.want {
			t.Errorf("activityFollowUpText(%q, %q) = %q, want %q", tc.title, tc.person, got, tc.want)
		}
	}
	long := "Investigate why the nightly synchronization of calendar invitations between the desktop computer and the paired phone keeps duplicating recurring events"
	got := activityFollowUpText(long, "", "en")
	if n := utf8.RuneCountInString(got); n > activityFollowUpMaxRunes || n < 40 {
		t.Fatalf("long title gave %d runes: %q", n, got)
	}
	if !strings.HasPrefix(got, "Follow up on investigate why") || strings.HasSuffix(got, " ") || !strings.Contains(long, strings.TrimPrefix(got, "Follow up on i")) {
		t.Fatalf("long title is not cut at a word boundary: %q", got)
	}
}

// The short name is display-only: a commitment keeps the user's full words
// in the ledger, and Today, Goals and the phone shorten them when shown.
func TestUserCommitmentStoresFullWords(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now().Add(-10 * time.Minute)
	dictated := "Make sure I do some sort of calf race or exercise for AFO like every day"
	detail, err := app.createUserPurpose(userPurposeCreate{Key: "full-words", Origin: "goals", Kind: PurposeKindCommitment, Text: dictated}, now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := app.userPurposeStore()
	stored, err := store.GetDetail(detail.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := purposeCurrentLedgerEvent(stored)
	if current.Title != dictated || current.DesiredOutcome != dictated {
		t.Fatalf("stored commitment = %q / %q, want the full words", current.Title, current.DesiredOutcome)
	}
	if got := mobileGoalTitle(stored.OpenLoop.Kind, current.Title); got != "AFO calf raises, every day" {
		t.Fatalf("shown commitment = %q", got)
	}
}

func TestActivityFollowUpAddRetryUndo(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	// The Purpose ledger refuses times ahead of the wall clock.
	now := time.Now().Add(-10 * time.Minute)
	row := insertRowActionActivity(t, app, "act-lamp", "Comparing Halcyon Arc desk lamps for Priya", now.Add(-2*time.Hour))

	added, err := app.addActivityFollowUp(row.ID, "  Follow up with Priya about the Halcyon Arc lamp ", "click-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if added.Text != "Follow up with Priya about the Halcyon Arc lamp" || added.OpenLoopID == "" || added.EventID == "" {
		t.Fatalf("added = %+v", added)
	}
	again, err := app.addActivityFollowUp(row.ID, "Follow up with Priya about the Halcyon Arc lamp", "click-1", now.Add(time.Second))
	if err != nil || again.OpenLoopID != added.OpenLoopID {
		t.Fatalf("a retry must return the same follow-up: %+v %v", again, err)
	}
	store, _ := app.userPurposeStore()
	detail, err := store.GetDetail(added.OpenLoopID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.OpenLoop.Kind != PurposeKindCommitment || !activityFollowUpPurpose(detail) || detail.Evidence[0].SourceID != row.ID ||
		detail.Evidence[0].EvidenceClass != PurposeEvidenceExplicitUser {
		t.Fatalf("follow-up purpose = %+v", detail)
	}
	open, err := app.activityOpenFollowUps([]string{row.ID})
	if err != nil || open[row.ID] == nil || open[row.ID].OpenLoopID != added.OpenLoopID {
		t.Fatalf("open follow-ups = %+v %v", open, err)
	}

	if err := app.undoActivityFollowUp(added.OpenLoopID, "stale-event", now.Add(time.Minute)); err != errUserPurposeChanged {
		t.Fatalf("undo against a stale event = %v", err)
	}
	if err := app.undoActivityFollowUp(added.OpenLoopID, added.EventID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	detail, _ = store.GetDetail(added.OpenLoopID)
	if purposeCurrentLedgerEvent(detail).LifecycleState != PurposeStateDismissed {
		t.Fatalf("undo left the follow-up %q", purposeCurrentLedgerEvent(detail).LifecycleState)
	}
	if err := app.undoActivityFollowUp(added.OpenLoopID, added.EventID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("a second undo must be a no-op: %v", err)
	}
	open, _ = app.activityOpenFollowUps([]string{row.ID})
	if open[row.ID] != nil {
		t.Fatalf("an undone follow-up is still open: %+v", open[row.ID])
	}
	if _, err := app.addActivityFollowUp(row.ID, "   ", "click-2", now); err != errActivityFollowUpEmpty {
		t.Fatalf("empty follow-up = %v", err)
	}
	if _, err := app.addActivityFollowUp("missing", "Call Priya", "click-3", now); err != errActivityNotFound {
		t.Fatalf("missing moment = %v", err)
	}
}

func TestActivityFollowUpNeedsAttentionRightAfterDueToday(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now()
	row := insertRowActionActivity(t, app, "act-lamp", "Comparing Halcyon Arc desk lamps for Priya", now.Add(-2*time.Hour))
	added, err := app.addActivityFollowUp(row.ID, "Follow up with Priya about the Halcyon Arc lamp", "k", now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// An undated goal the user added elsewhere still needs no attention.
	if _, err := app.createUserPurpose(userPurposeCreate{Key: "plain", Origin: "computer", Kind: PurposeKindCommitment, Text: "Run payroll"}, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	store, _ := app.userPurposeStore()
	details, err := store.ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	in := mobileBriefInput{AsOf: now, Location: time.Local}
	collectMobileBriefPurposes(app, &in, details, nil)
	var followUp, plain *mobileBriefCandidate
	for i := range in.WorkingToward {
		switch in.WorkingToward[i].SourceID {
		case added.OpenLoopID:
			followUp = &in.WorkingToward[i]
		default:
			plain = &in.WorkingToward[i]
		}
	}
	if followUp == nil || !followUp.FollowUp || plain == nil || plain.FollowUp {
		t.Fatalf("working toward = %+v", in.WorkingToward)
	}
	reason, rank := mobileBriefAttention(*followUp, now, time.Local, "en")
	if reason != "Follow-up you added today" || rank != mobileBriefAttentionFollowUp {
		t.Fatalf("attention = %q %d", reason, rank)
	}
	if zh, _ := mobileBriefAttention(*followUp, now, time.Local, "zh"); zh != "你今天添加的跟进" {
		t.Fatalf("zh attention = %q", zh)
	}
	if reason, rank := mobileBriefAttention(*plain, now, time.Local, "en"); reason != "" || rank != 0 {
		t.Fatalf("an undated goal must stay out: %q %d", reason, rank)
	}
	if !(mobileBriefAttentionDueToday < mobileBriefAttentionFollowUp && mobileBriefAttentionFollowUp < mobileBriefAttentionPersonWaiting) {
		t.Fatal("a follow-up ranks right after Due today")
	}
	later, _ := mobileBriefAttention(*followUp, now.AddDate(0, 0, 1), time.Local, "en")
	if later != "Follow-up you added yesterday" {
		t.Fatalf("next day = %q", later)
	}

	// Deleting the moment keeps the follow-up on the user's own words.
	if err := app.activity.store.DeleteActivity(row.ID); err != nil {
		t.Fatal(err)
	}
	details, _ = store.ListDetailsAll()
	in = mobileBriefInput{AsOf: now, Location: time.Local}
	collectMobileBriefPurposes(app, &in, details, nil)
	found := false
	for _, item := range in.WorkingToward {
		found = found || item.SourceID == added.OpenLoopID && item.FollowUp && !item.EvidenceUnavailable
	}
	if !found {
		t.Fatalf("follow-up left the brief with its moment: %+v", in.WorkingToward)
	}
}

type reminderDeliveries struct {
	mu      sync.Mutex
	desktop []engagementText
	push    []engagementText
}

func newReminderTestApp(t *testing.T, clock *time.Time) (*App, *reminderDeliveries) {
	t.Helper()
	app := newMobileBriefPurposeTestApp(t)
	got := &reminderDeliveries{}
	app.engagement = newEngagementService(app.store, func(time.Time) engagement.Presence {
		return engagement.Presence{PhoneRegistered: true, PhoneLastSeen: *clock}
	}, engagementDelivery{
		Push: func(_ context.Context, _ storage.EngagementMomentRow, _ engagement.Moment, _ engagement.Verdict, text engagementText) error {
			got.mu.Lock()
			defer got.mu.Unlock()
			got.push = append(got.push, text)
			return nil
		},
		Desktop: func(_ context.Context, _ storage.EngagementMomentRow, _ engagement.Moment, _ engagement.Verdict, text engagementText) error {
			got.mu.Lock()
			defer got.mu.Unlock()
			got.desktop = append(got.desktop, text)
			return nil
		},
	})
	app.engagement.now = func() time.Time { return *clock }
	return app, got
}

func TestActivityReminderWaitsForItsTimeThenReachesComputerAndPhone(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 20, 15, 0, 0, 0, loc)
	app, got := newReminderTestApp(t, &clock)
	ctx := context.Background()
	row := insertRowActionActivity(t, app, "act-pr", "Review and merge isoform PRs, direct Kimi workers, and plan the release", clock.Add(-time.Hour))

	if _, err := app.setActivityReminder(ctx, row.ID, clock.Add(10*time.Second), clock); err != errActivityReminderTime {
		t.Fatalf("a reminder in the past or now = %v", err)
	}
	// Late at night, inside quiet hours: the user chose this time.
	at := time.Date(2026, 10, 20, 23, 30, 0, 0, loc)
	setAt, err := app.setActivityReminder(ctx, row.ID, at, clock)
	if err != nil || setAt != at.UnixMilli() {
		t.Fatalf("set = %d %v", setAt, err)
	}
	pending, _ := app.activityPendingReminders(ctx)
	if pending[row.ID] != at.UnixMilli() {
		t.Fatalf("pending = %v", pending)
	}

	clock = at.Add(-time.Minute)
	app.engagement.Pass(ctx)
	if len(got.desktop)+len(got.push) != 0 {
		t.Fatalf("delivered before its time: desktop=%d push=%d", len(got.desktop), len(got.push))
	}
	clock = at.Add(time.Minute)
	app.engagement.Pass(ctx)
	if len(got.desktop) != 1 || len(got.push) != 1 {
		t.Fatalf("expected the computer and the phone, got desktop=%d push=%d", len(got.desktop), len(got.push))
	}
	if got.push[0].PushBody != "A reminder from Isoform." || strings.Contains(got.push[0].PushBody, "isoform PRs") {
		t.Fatalf("the push body must name nothing: %+v", got.push[0])
	}
	if got.desktop[0].Title != "Reminder: Review and merge isoform PRs" {
		t.Fatalf("reminder text = %q", got.desktop[0].Title)
	}
	pending, _ = app.activityPendingReminders(ctx)
	if pending[row.ID] != 0 {
		t.Fatalf("a delivered reminder is still pending: %v", pending)
	}
	clock = at.Add(2 * time.Hour)
	app.engagement.Pass(ctx)
	if len(got.desktop) != 1 || len(got.push) != 1 {
		t.Fatal("a reminder is said once")
	}
}

func TestActivityReminderMovesAndCancels(t *testing.T) {
	clock := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	app, got := newReminderTestApp(t, &clock)
	ctx := context.Background()
	row := insertRowActionActivity(t, app, "act-pr", "Plan the release", clock.Add(-time.Hour))

	first := clock.Add(3 * time.Hour)
	second := clock.Add(18 * time.Hour)
	if _, err := app.setActivityReminder(ctx, row.ID, first, clock); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setActivityReminder(ctx, row.ID, second, clock); err != nil {
		t.Fatal(err)
	}
	pending, _ := app.activityPendingReminders(ctx)
	if pending[row.ID] != second.UnixMilli() {
		t.Fatalf("a moved reminder = %v", pending)
	}
	clock = first.Add(time.Minute)
	app.engagement.Pass(ctx)
	if len(got.desktop)+len(got.push) != 0 {
		t.Fatal("the replaced time still fired")
	}
	if err := app.cancelActivityReminder(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	pending, _ = app.activityPendingReminders(ctx)
	if pending[row.ID] != 0 {
		t.Fatalf("cancelled reminder still pending: %v", pending)
	}
	clock = second.Add(time.Minute)
	app.engagement.Pass(ctx)
	if len(got.desktop)+len(got.push) != 0 {
		t.Fatal("a cancelled reminder fired")
	}

	app.engagement = nil
	if _, err := app.setActivityReminder(ctx, row.ID, clock.Add(time.Hour), clock); err != errActivityRemindersOff {
		t.Fatalf("without the engagement service = %v", err)
	}
}

func TestActivityCorrectionSurvivesRecomposeAndTeachesTheComposer(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	row := insertRowActionActivity(t, app, "act-lamp", "Browsing the Halcyon catalog", now.Add(-time.Hour))

	if _, err := store.correctActivityText(row.ID, "   ", "", now); err != errActivityTitleEmpty {
		t.Fatalf("empty title = %v", err)
	}
	changed, err := store.correctActivityText(row.ID, "Priya's Halcyon Arc desk lamp", "", now)
	if err != nil || !changed {
		t.Fatalf("correct = %v %v", changed, err)
	}
	if changed, _ := store.correctActivityText(row.ID, "Priya's Halcyon Arc desk lamp", "", now); changed {
		t.Fatal("the same text again is not a change")
	}
	// The composer refreshes the moment with its own words.
	recomposed := row
	recomposed.Title, recomposed.Description, recomposed.UpdatedAt = "Reviewing Halcyon lamp finishes", "Compared two lamp finishes.", now.Add(time.Minute).UnixMilli()
	if err := store.UpdateActivity(recomposed); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetActivityByID(row.ID)
	if err != nil || stored.Title != "Priya's Halcyon Arc desk lamp" || stored.Description != "" {
		t.Fatalf("the user's text must win: %+v %v", stored, err)
	}
	corrected, _ := store.activityCorrectedIDs([]string{row.ID})
	if !corrected[row.ID] {
		t.Fatal("corrected flag")
	}
	corrections, err := store.recentActivityNameCorrections(activityNamingCorrectionsInPrompt)
	if err != nil || len(corrections) != 1 || corrections[0].Before != "Browsing the Halcyon catalog" || corrections[0].After != "Priya's Halcyon Arc desk lamp" {
		t.Fatalf("corrections = %+v %v", corrections, err)
	}
	block := composerNamingCorrections(corrections)
	if !strings.Contains(block, `"Browsing the Halcyon catalog" became "Priya's Halcyon Arc desk lamp"`) || !strings.Contains(block, "naming style") {
		t.Fatalf("prompt block = %q", block)
	}
	if composerNamingCorrections(nil) != "" {
		t.Fatal("no corrections, no block")
	}
	// Deleting the moment erases its corrections.
	if err := store.DeleteActivity(row.ID); err != nil {
		t.Fatal(err)
	}
	corrections, _ = store.recentActivityNameCorrections(activityNamingCorrectionsInPrompt)
	if len(corrections) != 0 {
		t.Fatalf("corrections outlived their moment: %+v", corrections)
	}
}

func TestActivityRowActionsFillTheDayView(t *testing.T) {
	clock := time.Now().Add(-time.Minute)
	app, _ := newReminderTestApp(t, &clock)
	ctx := context.Background()
	row := insertRowActionActivity(t, app, "act-lamp", "Comparing Halcyon Arc desk lamps for Priya, checking finishes", clock.Add(-time.Hour))
	private := insertRowActionActivity(t, app, "act-private", "Private Browsing", clock.Add(-30*time.Minute))
	if _, err := app.addActivityFollowUp(row.ID, "Follow up with Priya about the Halcyon Arc lamp", "k", clock); err != nil {
		t.Fatal(err)
	}
	at := clock.Add(3 * time.Hour)
	if _, err := app.setActivityReminder(ctx, row.ID, at, clock); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.correctActivityText(row.ID, "Comparing Halcyon Arc desk lamps for Priya, checking finishes", "Picked the linen shade.", clock); err != nil {
		t.Fatal(err)
	}
	views, err := app.GetActivitiesInRange(clock.Add(-6*time.Hour).UnixMilli(), clock.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ActivityView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	lamp := byID[row.ID]
	if lamp.FollowUpText != "Follow up with Priya about comparing Halcyon Arc desk lamps for Priya" || lamp.FollowUp == nil ||
		lamp.ReminderAt != at.UnixMilli() || !lamp.Corrected || lamp.ActionsWithheld != "" {
		t.Fatalf("row state = %+v", lamp)
	}
	hidden := byID[private.ID]
	if hidden.ActionsWithheld != activityActionsPrivate || hidden.FollowUpText != "" {
		t.Fatalf("a private moment offers actions: %+v", hidden)
	}
	if _, err := app.addActivityFollowUp(private.ID, "Look at this later", "k2", clock); err != errActivityActionsWithheld {
		t.Fatalf("follow-up on a private moment = %v", err)
	}
}

func TestActivityUserCorrectionsMigrationIsIdempotent(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	ok, err := store.activityUserCorrectionsPresent()
	if err != nil || !ok {
		t.Fatalf("fresh store = %v %v", ok, err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := migrateActivityUserCorrections(tx); err != nil {
		t.Fatalf("replay = %v", err)
	}
}

func TestActivityReminderChoices(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	afternoon := activityReminderChoices(time.Date(2026, 10, 20, 14, 7, 30, 0, loc))
	if len(afternoon) != 2 || afternoon[0].ID != activityReminderLaterToday ||
		!time.UnixMilli(afternoon[0].AtMs).Equal(time.Date(2026, 10, 20, 17, 15, 0, 0, loc)) ||
		!time.UnixMilli(afternoon[1].AtMs).Equal(time.Date(2026, 10, 21, 9, 0, 0, 0, loc)) {
		t.Fatalf("afternoon choices = %+v", afternoon)
	}
	evening := activityReminderChoices(time.Date(2026, 10, 20, 20, 30, 0, 0, loc))
	if len(evening) != 1 || evening[0].ID != activityReminderTomorrowMorning {
		t.Fatalf("after 8 PM only tomorrow morning: %+v", evening)
	}
}

func TestActivityReminderToastOpensItsChatCard(t *testing.T) {
	clock := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	app, _ := newReminderTestApp(t, &clock)
	ctx := context.Background()
	row := insertRowActionActivity(t, app, "act-pr", "Plan the release", time.Date(2026, 10, 19, 16, 0, 0, 0, time.Local))
	if _, err := app.setActivityReminder(ctx, row.ID, clock.Add(time.Hour), clock); err != nil {
		t.Fatal(err)
	}
	open, err := app.store.OpenEngagementMomentsOfKind(ctx, string(engagement.KindReminder))
	if err != nil || len(open) != 1 {
		t.Fatalf("open reminders = %+v %v", open, err)
	}
	if got := engagementClickData(momentFromRow(open[0])); got != "main-chat-item:"+open[0].ID {
		t.Fatalf("click data = %q", got)
	}
}

func TestActivityFollowUpCanBeTalkedAboutFromToday(t *testing.T) {
	a := newTodayContextTestApp(t, false)
	followUp := PurposeOpenLoopDetail{
		OpenLoop: PurposeOpenLoopRecord{ID: "follow-up", Kind: PurposeKindCommitment},
		Current:  PurposeOpenLoopEventRecord{Title: "Follow up with Priya about the Halcyon Arc lamp"},
		Evidence: []PurposeEvidenceRecord{{SourceKind: activityFollowUpSourceKind, SourceID: "act-lamp", EvidenceClass: PurposeEvidenceExplicitUser, PrivacyDecision: TaskContextPrivacyRedact}},
	}
	kept := a.filterTodayChatPurposes(todayChatSources{Purposes: []PurposeOpenLoopDetail{followUp}})
	if len(kept) != 1 || kept[0].OpenLoop.ID != "follow-up" {
		t.Fatalf("a follow-up must stay available to Today's chat: %+v", kept)
	}
}

func TestActivityRemindersNeverPauseOrMuteTheirKind(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	var last storage.EngagementMomentRow
	for i := 0; i < 25; i++ {
		row, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
			Kind: string(engagement.KindReminder), DedupeKey: fmt.Sprintf("reminder:a:%d", i),
			State: storage.EngagementStateSent, Channel: "push", CreatedAt: rig.clock.Add(-time.Hour).UnixMilli(), SentAt: rig.clock.Add(-time.Minute).UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		state := storage.EngagementReceiptDismissed
		if i == 24 {
			state = storage.EngagementReceiptMuted
		}
		if err := rig.svc.Receipt(ctx, row.ID, "push", state, "", rig.clock); err != nil {
			t.Fatal(err)
		}
		last = row
	}
	if rig.svc.Paused(engagement.KindReminder) {
		t.Fatal("dismissing reminders paused the kind")
	}
	prefs, err := rig.store.ListEngagementPrefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prefs {
		if p.Kind == string(engagement.KindReminder) && p.Enabled == 0 {
			t.Fatalf("a muted reminder turned the kind off: %+v (last %s)", p, last.ID)
		}
	}
	// The next reminder still goes out, even with the kind muted for a day.
	if err := rig.svc.MuteKind(ctx, engagement.KindReminder, rig.clock.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Submit(ctx, engagement.Moment{Kind: engagement.KindReminder, SubjectID: "act", DedupeKey: "reminder:act:next",
		Params: map[string]string{"title": "Plan the release"}, Channels: []engagement.Channel{engagement.ChannelDesktop, engagement.ChannelPush}}); err != nil {
		t.Fatal(err)
	}
	rig.svc.Pass(ctx)
	if len(rig.desktop) != 1 || len(rig.pushed) != 1 {
		t.Fatalf("reminder after dismissals and a mute: desktop=%d push=%d", len(rig.desktop), len(rig.pushed))
	}
}
