package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func mainChatStructuredRows(t *testing.T, app *App, tool string) []storage.StreamItemRow {
	t.Helper()
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := app.store.LoadStreamItems(context.Background(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	var out []storage.StreamItemRow
	for _, row := range rows {
		if row.ToolName == tool {
			out = append(out, row)
		}
	}
	return out
}

func mainChatProactiveItems(t *testing.T, app *App) []mainChatProactive {
	t.Helper()
	var out []mainChatProactive
	for _, row := range mainChatStructuredRows(t, app, mainChatProactiveTool) {
		var item mainChatProactive
		if err := json.Unmarshal([]byte(row.ToolInput), &item); err != nil {
			t.Fatal(err)
		}
		out = append(out, item)
	}
	return out
}

func mainChatStatusEntries(t *testing.T, app *App) []mainChatStatusEntry {
	t.Helper()
	var out []mainChatStatusEntry
	for _, row := range mainChatStructuredRows(t, app, mainChatStatusTool) {
		var status mainChatStatus
		if err := json.Unmarshal([]byte(row.ToolInput), &status); err != nil {
			t.Fatal(err)
		}
		out = append(out, status.Entries...)
	}
	return out
}

func TestMainChatStatusFoldsAcrossProducersAndRestart(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.Local)
	entry := mainChatStatusEntry{ID: "capture:1", Kind: "capture_paused", Text: "Screen capture is paused.", Target: mainChatTarget{Kind: "settings"}}
	id := app.postMainChatStatus(entry, now)
	app.postMainChatStatus(entry, now.Add(time.Minute))
	reopened := &App{store: app.store, eventBroadcaster: NewEventBroadcaster()}
	second := mainChatStatusEntry{ID: "schedule:1", Kind: "schedule_error", Text: "A schedule failed.", Target: mainChatTarget{Kind: "automation", ID: "job-1"}}
	if got := reopened.postMainChatStatus(second, now.Add(time.Hour)); got != id || got == "" {
		t.Fatalf("period row changed: %q -> %q", id, got)
	}
	rows := mainChatStructuredRows(t, app, mainChatStatusTool)
	if len(rows) != 1 || len(mainChatStatusEntries(t, app)) != 2 || !strings.Contains(rows[0].Content, second.Text) {
		t.Fatalf("status not folded: %+v", rows)
	}
	if got := app.postMainChatStatus(mainChatStatusEntry{ID: "later", Kind: "brief_missed", Text: "Your brief was missed."}, now.Add(4*time.Hour)); got == id || got == "" {
		t.Fatal("next period reused prior row")
	}
}

func TestMainChatProactiveRedactedBeforeStorageAndMailGeneric(t *testing.T) {
	app := mainChatTestApp(t)
	// A flagged current source exercises the generic fallback without disclosing its subject.
	app.postEngagementReport(engagement.Moment{ID: "m-1", Kind: engagement.KindGoalCheckIn, SubjectID: "goal-1", Params: map[string]string{"title": "Use card 4111 1111 1111 1111"}}, engagementText{PushBody: "A quick check on a goal.", Title: `You set "Use card 4111 1111 1111 1111". How is it going?`})
	mail := proactiveVoiceTestFlaggedMail(t, app, time.Now(), "Pay invoice with card 4111 1111 1111 1111")
	mail.ID = "m-2"
	mail.Params["subject"] = "Private acquisition"
	app.postEngagementReport(mail, engagementText{PushBody: "Someone is waiting for your reply.", Title: "Dana needs Private acquisition."})
	rows := mainChatStructuredRows(t, app, mainChatProactiveTool)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, row := range rows {
		if strings.Contains(row.Content+row.ToolInput, "4111") || strings.Contains(row.Content+row.ToolInput, "Private acquisition") {
			t.Fatalf("private content persisted: %+v", row)
		}
	}
	if !strings.Contains(rows[0].ToolInput, "REDACTED:credit_card") {
		t.Fatalf("missing redaction: %s", rows[0].ToolInput)
	}
}

func TestMainChatProactiveSubjectsAndDraftsNeverReachPromptBuilder(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "iv-p-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", home)
	app := mainChatTestApp(t)
	main, _ := app.ensureMainChat(context.Background())
	secret := "CONFIDENTIAL-subject-82910"
	// Deliberately hostile display fields prove exclusion is structural,
	// independent of today's generic waiting-on-reply template.
	payload := `{"momentId":"mail-1","kind":"waiting_on_reply","headline":"` + secret + `","body":"` + secret + `","subject":"` + secret + `","draft":true,"draftText":"` + secret + `","state":"pending","localized":{"en":{"headline":"` + secret + `","body":"` + secret + `","actionLabels":{"open":"` + secret + `"}}},"evidence":[{"kind":"mail","id":"` + secret + `"}]}`
	app.appendMainChatItem(context.Background(), main.LocalID, storage.StreamItemRow{ID: "private", Kind: "system", Content: secret, ToolName: mainChatProactiveTool, ToolInput: payload}, mainChatSourceEngagement)
	app.postToMainChat(secret, mainChatReport{Source: mainChatSourceEngagement, Status: string(engagement.KindWaitingOnReply), Title: secret, Summary: secret})
	app.postMainChatStatus(mainChatStatusEntry{ID: "status-secret", Kind: "brief_missed", Text: secret}, time.Now())
	block := app.injectMainChatReports(main.LocalID)
	if strings.Contains(block, secret) || !strings.Contains(block, "Someone") {
		t.Fatalf("prompt producer = %s", block)
	}
	if prompt := app.composePrompt("What needs my attention?", main.LocalID); strings.Contains(prompt, secret) || !strings.Contains(prompt, "<background-reports>") {
		t.Fatalf("full prompt did not preserve the display-field boundary: %s", prompt)
	}
	// The other history entry point excludes structured system rows too.
	history, err := app.store.LoadConversationHistory(context.Background(), main.LocalID, 100)
	if err != nil || len(history) != 0 {
		t.Fatalf("history leaked proactive payload: %+v, %v", history, err)
	}
}

func TestMainChatProactiveActionValidatesAndPersists(t *testing.T) {
	app := mainChatTestApp(t)
	event := proactiveVoiceTestEvent(t, app, time.Now())
	moment := meetingPrepMoment(event, time.Now(), time.Local)
	moment.ID = "m-action"
	app.postEngagementReport(moment, engagementText{PushBody: "A meeting starts soon.", Title: "Design review starts in 15 minutes."})
	if _, err := app.ResolveMainChatProactiveAction("m-action", "send"); err == nil {
		t.Fatal("unsupported action accepted")
	}
	state, err := app.ResolveMainChatProactiveAction("m-action", "open")
	if err != nil || state != "opened" {
		t.Fatalf("open = %q, %v", state, err)
	}
	if got := mainChatProactiveItems(t, app)[0].State; got != "opened" {
		t.Fatalf("stored state = %q", got)
	}
	if state, err := app.ResolveMainChatProactiveAction("m-action", "open"); err != nil || state != "opened" {
		t.Fatalf("repeated action = %q, %v", state, err)
	}
}

func TestMainChatReminderDoneMarksReceiptActed(t *testing.T) {
	app := mainChatTestApp(t)
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"momentId":"m-reminder","kind":"reminder","actionIds":["done","not_now"],"state":"pending","target":{"kind":"today"}}`
	app.appendMainChatItem(context.Background(), main.LocalID, storage.StreamItemRow{
		ID: "reminder-action", Kind: "system", Content: "A reminder from Isoform.",
		ToolName: mainChatProactiveTool, ToolInput: payload,
	}, mainChatSourceEngagement)

	state, err := app.ResolveMainChatProactiveAction("m-reminder", "done")
	if err != nil || state != "acted" {
		t.Fatalf("done = %q, %v", state, err)
	}
	items := mainChatProactiveItems(t, app)
	if len(items) != 1 || items[0].State != "acted" {
		t.Fatalf("reminder state = %+v", items)
	}
}

func TestCorrectMainChatProactiveFactValidatesAndReviewsPendingFact(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	chat := mainChatTestApp(t)
	rig.app.store = chat.store

	if _, err := rig.app.CorrectMainChatProactiveFact("fact-empty", "   "); err == nil {
		t.Fatal("empty correction was accepted")
	}

	main, err := rig.app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	appendCard := func(id, kind, state, target string) {
		t.Helper()
		payload, marshalErr := json.Marshal(mainChatProactive{
			MomentID: id, Kind: kind, ActionIDs: []string{"correct"},
			ActionLabels: map[string]string{"correct": "Not right"},
			Target:       mainChatTarget{Kind: "memory", ID: target}, State: state,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		chatRow := storage.StreamItemRow{
			ID: id + "-row", Kind: "system", Content: "A learned fact.",
			ToolName: mainChatProactiveTool, ToolInput: string(payload),
		}
		if appended := rig.app.appendMainChatItem(context.Background(), main.LocalID, chatRow, mainChatSourceEngagement); appended == "" {
			t.Fatal("card was not appended")
		}
	}
	appendCard("fact-wrong-kind", "reminder", "pending", "operation-1")
	if _, err := rig.app.CorrectMainChatProactiveFact("fact-wrong-kind", "Use concise replies."); err == nil {
		t.Fatal("wrong-kind correction was accepted")
	}
	appendCard("fact-answered", string(engagement.KindLearnedToConfirm), "acted", "operation-1")
	if _, err := rig.app.CorrectMainChatProactiveFact("fact-answered", "Use concise replies."); err == nil {
		t.Fatal("non-pending correction was accepted")
	}

	seedSelfDreamDuplicateEntries(t, rig)
	detail, err := rig.app.runSelfDreamShadow(context.Background(), "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	var operation SelfDreamOperationRecord
	for _, candidate := range detail.Operations {
		if candidate.Operation == SelfDreamOperationMerge {
			operation = candidate
			break
		}
	}
	if operation.ID == "" {
		t.Fatal("missing correction operation")
	}
	appendCard("fact-correct", string(engagement.KindLearnedToConfirm), "pending", operation.ID)
	state, err := rig.app.CorrectMainChatProactiveFact("fact-correct", "  Use concise replies.  ")
	if err != nil || state != storage.EngagementReceiptActed {
		t.Fatalf("correction = %q, %v", state, err)
	}
	decision, err := rig.store.GetSelfDreamDecisionByIdempotency("main-chat:fact-correct:correct")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != SelfDreamDecisionCorrect || decision.Note != "Use concise replies." {
		t.Fatalf("decision = %+v", decision)
	}
	row, err := rig.app.store.FindProactiveStreamItem(context.Background(), main.LocalID, "fact-correct")
	if err != nil {
		t.Fatal(err)
	}
	var updated mainChatProactive
	if err := json.Unmarshal([]byte(row.ToolInput), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.State != storage.EngagementReceiptActed {
		t.Fatalf("stored card state = %q", updated.State)
	}
}

func TestMainChatRoutineThresholds(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		kind engagement.Kind
		age  time.Duration
		want bool
	}{
		{engagement.KindCapturePaused, 59 * time.Minute, true}, {engagement.KindCapturePaused, time.Hour, false},
		{engagement.KindBriefMissed, 0, true}, {engagement.KindPlaybookResult, 0, true}, {engagement.KindBriefReady, 0, false},
	} {
		if got := engagementMomentIsStatus(engagement.Moment{Kind: tc.kind, CreatedAt: now.Add(-tc.age)}, now); got != tc.want {
			t.Fatalf("%s/%v status=%v", tc.kind, tc.age, got)
		}
	}
}

func TestMainChatDigestRetryDoesNotRepeatChildren(t *testing.T) {
	app := mainChatTestApp(t)
	mail := proactiveVoiceTestFlaggedMail(t, app, time.Now(), "Please pay the invoice")
	mail.ID = "digest-a"
	mail.Params["subject"] = "Private subject"
	entries := []engagementDigestItem{
		{Moment: mail, Text: engagementText{PushBody: "Someone is waiting on your reply.", Title: "Private subject"}},
		{Moment: engagement.Moment{ID: "digest-b", Kind: engagement.KindCalendarConflict, SubjectID: "event-b", Params: map[string]string{
			"event_type": "overlap", "event_first_title": "Planning", "event_first_start": time.Now().Add(time.Hour).Format(time.RFC3339),
			"event_second_title": "Review", "event_second_start": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
			"summary": "Two meetings overlap.",
		}}, Text: engagementText{PushBody: "Your calendar needs a look.", Title: "Two events overlap."}},
	}
	if _, err := app.engagementDigest(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	// Simulate a restart after only one original moment was marked sent.
	reopened := &App{store: app.store, eventBroadcaster: NewEventBroadcaster()}
	if _, err := reopened.engagementDigest(context.Background(), entries[1:]); err != nil {
		t.Fatal(err)
	}
	items := mainChatProactiveItems(t, app)
	if len(items) != 1 || len(items[0].Items) != 2 {
		t.Fatalf("retry repeated digest children: %+v", items)
	}
	if entries := mainChatStatusEntries(t, app); len(entries) != 1 || entries[0].Kind != "digest_done" {
		t.Fatalf("retry repeated the digest completion status: %+v", entries)
	}
	if strings.Contains(proactiveFallback(items[0]), "Private subject") {
		t.Fatal("digest fallback exposed mail subject")
	}
}

// chat-review v1.0.7: a fold line gets Show only from a destination saved
// when it was written, so every status kind stores its own.
func TestMainChatStatusEntriesSaveTheirDestinationWhenWritten(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Date(2026, 10, 29, 18, 0, 0, 0, time.Local)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	app.engagement.now = func() time.Time { return now }
	want := map[string]mainChatTarget{
		string(engagement.KindPlaybookResult): {Kind: "playbook", ID: "plan-3"},
		string(engagement.KindScheduleMissed): {Kind: mainChatTargetAutomation, ID: "job-2"},
		string(engagement.KindCapturePaused):  {Kind: "settings", ID: "activity"},
	}
	moments := []engagement.Moment{
		{ID: "m-plan", Kind: engagement.KindPlaybookResult, SubjectID: "plan-3", DedupeKey: "playbook_result:run-3", CreatedAt: now, Params: map[string]string{"title": "Weekly review", "result": "Done."}},
		{ID: "m-job", Kind: engagement.KindScheduleMissed, SubjectID: "job-2", DedupeKey: "schedule_missed:job-2", CreatedAt: now, Params: map[string]string{"title": "Inbox sweep", "failures": "1"}},
		{ID: "m-pause", Kind: engagement.KindCapturePaused, SubjectID: "screen", DedupeKey: "capture_paused:screen", CreatedAt: now, Params: map[string]string{"source": "screen"}},
	}
	for _, moment := range moments {
		spec, _ := engagement.Spec(moment.Kind)
		if !app.postEngagementReport(moment, app.engagement.render(spec, moment)) {
			t.Fatalf("%s was not posted", moment.Kind)
		}
	}
	entries := mainChatStatusEntries(t, app)
	if len(entries) != len(want) {
		t.Fatalf("status entries = %+v", entries)
	}
	for _, entry := range entries {
		if entry.Target != want[entry.Kind] {
			t.Errorf("%s saved target %+v, want %+v", entry.Kind, entry.Target, want[entry.Kind])
		}
	}
	// Finished work posts through its handoff report, which saves the same target.
	if got, _ := engagementChatTarget(engagement.Moment{Kind: engagement.KindWorkFinished, SubjectID: "work-7"}); got != (mainChatTarget{Kind: mainChatTargetHandoff, ID: "work-7"}) {
		t.Errorf("work_finished target = %+v", got)
	}
}
