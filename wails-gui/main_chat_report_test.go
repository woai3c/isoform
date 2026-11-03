package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

// mainChatReports returns the report items stored on the main chat, oldest first.
func mainChatReports(t *testing.T, app *App) []storage.StreamItemRow {
	t.Helper()
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items, err := app.store.LoadStreamItems(context.Background(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	var out []storage.StreamItemRow
	for _, item := range items {
		if item.Kind == "system" && item.ToolName == mainChatReportTool {
			out = append(out, item)
		}
	}
	return out
}

func decodeReport(t *testing.T, row storage.StreamItemRow) mainChatReport {
	t.Helper()
	var report mainChatReport
	if err := json.Unmarshal([]byte(row.ToolInput), &report); err != nil {
		t.Fatalf("report payload %q: %v", row.ToolInput, err)
	}
	return report
}

// awaitBrowserEvent reads the next browser-bridge payload that names the event.
func awaitBrowserEvent(t *testing.T, client chan []byte, name string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case payload := <-client:
			if strings.Contains(string(payload), `"`+name+`"`) {
				return string(payload)
			}
		case <-deadline:
			t.Fatalf("event %s never reached the browser bridge", name)
		}
	}
}

func TestPostToMainChatAppendsSystemItemAndEmits(t *testing.T) {
	app := mainChatTestApp(t)
	client := app.eventBroadcaster.AddClient("main-chat-report-test")
	defer app.eventBroadcaster.RemoveClient("main-chat-report-test")

	id := app.postToMainChat("Finished \"Draft the pricing page\".", mainChatReport{
		Source: mainChatSourceHandoff,
		Key:    "handoff:hf_1:completed",
		Action: mainChatActionReview,
		Target: mainChatTarget{Kind: mainChatTargetSession, ID: "sess-1"},
		Title:  "Draft the pricing page",
		Status: "completed",
	})
	if id == "" {
		t.Fatal("postToMainChat returned no item id")
	}

	reports := mainChatReports(t, app)
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	row := reports[0]
	if row.ID != id || row.Content != "Finished \"Draft the pricing page\"." {
		t.Fatalf("stored row = %+v", row)
	}
	report := decodeReport(t, row)
	if report.Action != mainChatActionReview || report.Target.ID != "sess-1" || report.Title != "Draft the pricing page" {
		t.Fatalf("payload = %+v", report)
	}

	payload := awaitBrowserEvent(t, client, mainChatItemsAppendedEvent)
	main, _ := app.ensureMainChat(context.Background())
	if !strings.Contains(payload, main.LocalID) || !strings.Contains(payload, id) {
		t.Fatalf("event payload %s does not name the main chat and item", payload)
	}

	// The same key is not posted twice; a new key is.
	if again := app.postToMainChat("Finished again.", mainChatReport{Source: mainChatSourceHandoff, Key: "handoff:hf_1:completed", Action: mainChatActionReview}); again != "" {
		t.Fatal("duplicate key was appended")
	}
	if other := app.postToMainChat("Finished other.", mainChatReport{Source: mainChatSourceHandoff, Key: "handoff:hf_2:completed", Action: mainChatActionReview}); other == "" {
		t.Fatal("distinct key was deduped")
	}
	if got := len(mainChatReports(t, app)); got != 2 {
		t.Fatalf("reports after dedupe = %d, want 2", got)
	}
	// Empty text is dropped; a missing store is a no-op.
	if app.postToMainChat("   ", mainChatReport{Source: mainChatSourceSchedule}) != "" {
		t.Fatal("blank report was appended")
	}
	if (&App{}).postToMainChat("x", mainChatReport{}) != "" {
		t.Fatal("report without a store was appended")
	}
}

func TestPostHandoffReportPerStatus(t *testing.T) {
	app := mainChatTestApp(t)
	app.postHandoffReport(handoff.Entry{ID: "done", Title: "Ship the deck", Status: handoff.StatusCompleted, ChatSessionID: "chat-x"})
	app.postHandoffReport(handoff.Entry{ID: "done", Title: "Ship the deck", Status: handoff.StatusCompleted, ChatSessionID: "chat-x"})
	app.postHandoffReport(handoff.Entry{ID: "done", Title: "Ship the deck", Status: handoff.StatusCompleted, UpdatedAt: 42})
	completed := mainChatProactiveItems(t, app)
	if len(completed) != 2 || completed[0].Target.Kind != mainChatTargetHandoff || completed[1].Target.Kind != mainChatTargetHandoff {
		t.Fatalf("finished work lost review cards, dedupe or main-chat targets: %+v", completed)
	}
	for _, status := range []handoff.Status{handoff.StatusNeedsReview, handoff.StatusAborted, handoff.StatusBlocked} {
		app.postHandoffReport(handoff.Entry{ID: string(status), Title: "Needs a decision", Status: status, ChatSessionID: "chat-x"})
	}
	app.postHandoffReport(handoff.Entry{ID: "active", Status: handoff.StatusActive})
	items := mainChatProactiveItems(t, app)
	if len(items) != 5 || len(mainChatReports(t, app)) != 0 {
		t.Fatalf("actionable messages = %d; legacy reports = %d", len(items), len(mainChatReports(t, app)))
	}
	if last := items[4]; last.Kind != "work_blocked" || last.Target.ID != "blocked" || last.Work.Status != handoff.StatusBlocked {
		t.Fatalf("blocked message = %+v", last)
	}
}

func TestPostScheduleRunReportFiltersSurfaceAndStatus(t *testing.T) {
	app := mainChatTestApp(t)
	okRun := CronRunLifecycleEvent{JobID: "job-1", RunID: "run-1", Status: "ok", ResultSummary: "done", FinishedAt: 1_760_000_000_000}
	for _, surface := range []string{todayNewsSurfaceKind, "handoff", "other"} {
		app.postScheduleRunReport(okRun, "Hidden", surface)
	}
	for _, status := range []string{"running", "skipped", "ok"} {
		app.postScheduleRunReport(CronRunLifecycleEvent{Status: status}, "Empty", "scheduled")
	}
	if len(mainChatStatusEntries(t, app)) != 0 {
		t.Fatal("hidden or empty result posted")
	}
	app.postScheduleRunReport(okRun, "Weekly digest", "scheduled")
	app.postScheduleRunReport(okRun, "Weekly digest", "scheduled")
	app.postScheduleRunReport(CronRunLifecycleEvent{JobID: "job-1", RunID: "run-2", Status: "error", FinishedAt: 1_760_000_000_000}, "Weekly digest", "")
	entries := mainChatStatusEntries(t, app)
	if len(entries) != 2 || entries[0].Target.RunID != "run-1" || entries[1].Kind != "schedule_error" {
		t.Fatalf("folded runs = %+v", entries)
	}
	if len(mainChatReports(t, app)) != 0 {
		t.Fatal("routine runs posted separate bubbles")
	}
	app.handleCronRunLifecycle(CronRunLifecycleEvent{JobID: "job-2", JobName: "Inbox sweep", SurfaceKind: "scheduled", RunID: "run-9", Status: "ok", ResultSummary: "done"})
	if len(mainChatStatusEntries(t, app)) != 3 {
		t.Fatal("lifecycle result missing")
	}
}

func TestInjectMainChatReportsOnlyForTheMainChat(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	main, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := app.injectMainChatReports(main.LocalID); got != "" {
		t.Fatalf("empty main chat injected %q", got)
	}
	for i := 0; i < mainChatReportContextLines+3; i++ {
		app.postHandoffReport(handoff.Entry{ID: fmt.Sprintf("hf_%d", i), Title: fmt.Sprintf("Task %d", i), Status: handoff.StatusNeedsReview})
	}
	block := app.injectMainChatReports(main.LocalID)
	if !strings.Contains(block, "<background-reports>") || !strings.Contains(block, "Work finished.") || strings.Contains(block, "Task 14") {
		t.Fatalf("block = %s", block)
	}
	if strings.Count(block, "\n- [") != mainChatReportContextLines {
		t.Fatalf("lines = %d, want %d:\n%s", strings.Count(block, "\n- ["), mainChatReportContextLines, block)
	}
	// Side chats and plain chats get nothing from this producer.
	if err := app.store.SaveSession(ctx, storage.SessionRow{LocalID: "plain", ProjectID: main.ProjectID, WorkspaceID: main.WorkspaceID, Label: "Plain"}); err != nil {
		t.Fatal(err)
	}
	if got := app.injectMainChatReports("plain"); got != "" {
		t.Fatalf("plain chat injected %q", got)
	}
}

func TestPostEngagementReports(t *testing.T) {
	app := mainChatTestApp(t)
	text := engagementText{Title: "Your morning brief is ready.", PushBody: "Your brief is ready."}
	for _, kind := range []engagement.Kind{engagement.KindWorkFinished, engagement.KindWorkBlocked, engagement.KindMeetingEnded, engagement.KindMeetingFollowup} {
		app.postEngagementReport(engagement.Moment{Kind: kind, DedupeKey: string(kind)}, text)
	}
	if len(mainChatProactiveItems(t, app)) != 0 {
		t.Fatal("silent or separately reported moment posted")
	}
	app.postEngagementReport(engagement.Moment{Kind: engagement.KindBriefReady, DedupeKey: "brief:1"}, text)
	app.postEngagementReport(engagement.Moment{Kind: engagement.KindGoalCheckIn, DedupeKey: "goal:1"}, engagementText{Title: "How is the launch going?"})
	items := mainChatProactiveItems(t, app)
	if len(items) != 2 || items[0].Target.Kind != mainChatTargetToday || len(items[0].ActionIDs) != 2 || items[0].ActionIDs[0] != "open" || items[0].ActionIDs[1] != "mute_kind" || items[1].Body != "How is the launch going?" {
		t.Fatalf("items = %+v", items)
	}
}

func TestMainChatEngagementParamsPreserveDisplayContextWithoutMail(t *testing.T) {
	app := mainChatTestApp(t)
	moment := engagement.Moment{ID: "goal-context", Kind: engagement.KindGoalCheckIn, Params: map[string]string{"title": "Launch", "detail": "not part of this sentence"}}
	params := mainChatEngagementParams(moment)
	if len(params) != 1 || params["title"] != "Launch" {
		t.Fatalf("goal display params = %+v", params)
	}
	app.postEngagementReport(moment, engagementText{Title: "How is the launch going?"})
	items := mainChatProactiveItems(t, app)
	if len(items) != 1 || !strings.Contains(items[0].Localized["en"].Body, "Launch") {
		t.Fatalf("goal display context missing: %+v", items)
	}
	moment = proactiveVoiceTestFlaggedMail(t, app, time.Now(), "Please pay the invoice")
	moment.ID = "mail-context"
	moment.Params["subject"] = "Private mail subject"
	if len(mainChatEngagementParams(moment)) != 0 {
		t.Fatal("mail parameters entered chat copy")
	}
	app.postEngagementReport(moment, engagementText{Title: "Alice is waiting on Private mail subject.", PushBody: "Someone is waiting on your reply."})
	rows := mainChatStructuredRows(t, app, mainChatProactiveTool)
	if len(rows) != 2 {
		t.Fatalf("proactive reports = %d", len(rows))
	}
	if strings.Contains(rows[0].ToolInput, "not part of this sentence") || strings.Contains(rows[1].ToolInput+rows[1].Content, "Private mail subject") || strings.Contains(rows[1].ToolInput, "Alice") {
		t.Fatalf("report leaked private parameters: %+v", rows)
	}
}

func TestMeetingNotesAreSavedSilentlyWithTheMeeting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	app.activity = &activityMonitorState{store: newLifecycleStore(t)}
	stubMeetingNotesLearn(t)
	start := time.Date(2026, 10, 14, 9, 30, 0, 0, time.Local)
	session := ActivitySessionRow{
		ID:         "meeting-session-1",
		StartTime:  start.UnixMilli(),
		EndTime:    start.Add(47 * time.Minute).UnixMilli(),
		IsMeeting:  true,
		MeetingApp: "Zoom",
	}

	if _, err := app.recordMeetingNotes(session, "Zoom", 5, "", "", &meetingFollowupExtraction{Summary: "We started."}); err != nil {
		t.Fatal(err)
	}
	r, ok, err := app.activity.store.meetingNotesByKey(session.ID)
	if err != nil || !ok || len(r.Slices) != 1 || r.Slices[0].Summary != "We started." || r.MeetingApp != "Zoom" {
		t.Fatalf("notes = %+v ok=%v err=%v", r, ok, err)
	}

	// Notes are not Knowledge.
	if entries, err := app.GetKnowledgeEntries(); err != nil || len(entries) != 0 {
		t.Fatalf("knowledge entries = %+v err=%v, want none", entries, err)
	}

	// No Transcript: chat session is created any more.
	all, err := app.store.LoadAllSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, rows := range all {
		for _, row := range rows {
			if strings.HasPrefix(row.Label, "Transcript:") || row.LocalID == session.ID {
				t.Fatalf("a transcript chat session was created: %+v", row)
			}
		}
	}

	// Recording a call never announces itself: no main-chat line, also when
	// the notes are saved again.
	if _, err := app.recordMeetingNotes(session, "Zoom", 9, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if reports := mainChatReports(t, app); len(reports) != 0 {
		t.Fatalf("main chat reports = %d, want none", len(reports))
	}
	if r, _, _ := app.activity.store.meetingNotesByKey(session.ID); len(r.Slices) != 1 || r.Slices[0].WordCount != 9 || r.Slices[0].Summary != "We started." {
		t.Fatalf("saving again did not rewrite in place: %+v", r.Slices)
	}
}

func TestMeetingNotesKeyedByMeetingJoinRejoinedSlices(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	app.activity = &activityMonitorState{store: newLifecycleStore(t)}
	stubMeetingNotesLearn(t)
	start := time.Date(2026, 10, 14, 9, 30, 0, 0, time.Local)
	first := ActivitySessionRow{ID: "slice-1", StartTime: start.UnixMilli(), EndTime: start.Add(10 * time.Minute).UnixMilli(), IsMeeting: true, MeetingApp: "Zoom"}
	second := ActivitySessionRow{ID: "slice-2", StartTime: start.Add(12 * time.Minute).UnixMilli(), EndTime: start.Add(30 * time.Minute).UnixMilli(), IsMeeting: true, MeetingApp: "Zoom"}
	for _, s := range []ActivitySessionRow{first, second, second} {
		if _, err := app.recordMeetingNotes(s, "Zoom", 3, "meeting-X", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	r, ok, _ := app.activity.store.meetingNotesByKey("meeting-X")
	if !ok || len(r.Slices) != 2 || r.Slices[0].SessionID != first.ID || r.Slices[1].SessionID != second.ID {
		t.Fatalf("a rejoined call = %+v, want one meeting with both slices in order", r)
	}
	if r.StartTime != first.StartTime || r.EndTime != second.EndTime {
		t.Fatalf("span = %d..%d", r.StartTime, r.EndTime)
	}
	if got := len(mainChatReports(t, app)); got != 0 {
		t.Fatalf("a rejoined call posted %d lines, want none", got)
	}
}

func TestPostEngagementReportCollapsesRepeatedChecks(t *testing.T) {
	app := mainChatTestApp(t)
	outageStart := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	text := engagementText{Title: "System audio has been off for 15 minutes. Turn it back on?"}
	// Every 5-minute check during one outage delivers the moment again, each
	// time with a different dedupe key and the same CreatedAt.
	for i := 0; i < 8; i++ {
		app.postEngagementReport(engagement.Moment{
			Kind:      engagement.KindCapturePaused,
			SubjectID: "system_audio",
			DedupeKey: fmt.Sprintf("capture_paused:system_audio:check-%d", i),
			CreatedAt: outageStart,
		}, text)
	}
	if got := len(mainChatProactiveItems(t, app)); got != 1 {
		t.Fatalf("one outage posted %d lines, want 1", got)
	}
	// A later outage of the same source is a new line.
	app.postEngagementReport(engagement.Moment{Kind: engagement.KindCapturePaused, SubjectID: "system_audio", DedupeKey: "x", CreatedAt: outageStart.Add(3 * time.Hour)}, text)
	if got := len(mainChatProactiveItems(t, app)); got != 2 {
		t.Fatalf("second outage posted %d lines total, want 2", got)
	}

	// Generic-only commitment checks are withheld until current source evidence
	// can supply the sentence required by this card kind.
	slipping := engagementText{Title: "The launch deck is due today."}
	base := time.Date(2026, 10, 14, 14, 5, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		app.postEngagementReport(engagement.Moment{
			Kind:      engagement.KindCommitmentSlipping,
			SubjectID: "loop-1",
			DedupeKey: fmt.Sprintf("commitment:loop-1:%d", i),
			CreatedAt: base.Add(time.Duration(i) * 30 * time.Minute),
		}, slipping)
	}
	if got := len(mainChatProactiveItems(t, app)); got != 2 {
		t.Fatalf("generic-only commitment checks posted %d lines, want only 2 outages", got)
	}
	// A generic-only check from another loop is also withheld; an independent
	// headline card still posts once after repeated checks.
	app.postEngagementReport(engagement.Moment{Kind: engagement.KindCommitmentSlipping, SubjectID: "loop-2", DedupeKey: "y", CreatedAt: base}, slipping)
	app.postEngagementReport(engagement.Moment{Kind: engagement.KindGoalCheckIn, SubjectID: "loop-2", DedupeKey: "z"}, engagementText{Title: "How is the launch going?"})
	app.postEngagementReport(engagement.Moment{Kind: engagement.KindGoalCheckIn, SubjectID: "loop-2", DedupeKey: "z2"}, engagementText{Title: "How is the launch going?"})
	if got := len(mainChatProactiveItems(t, app)); got != 3 {
		t.Fatalf("reports = %d, want 3", got)
	}
}

func TestMeetingNotesRetryRetainsEarlierSlicesAndOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	app.activity = &activityMonitorState{store: newLifecycleStore(t)}
	stubMeetingNotesLearn(t)
	start := time.Now().Add(-time.Hour)
	first := ActivitySessionRow{ID: "first-slice", StartTime: start.UnixMilli(), EndTime: start.Add(time.Minute).UnixMilli()}
	later := ActivitySessionRow{ID: "later-slice", StartTime: start.Add(2 * time.Minute).UnixMilli(), EndTime: start.Add(3 * time.Minute).UnixMilli()}
	if _, err := app.recordMeetingNotes(later, "Zoom", 2, "retry-call", "", &meetingFollowupExtraction{Summary: "Last part."}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.recordMeetingNotes(first, "Zoom", 2, "retry-call", "", &meetingFollowupExtraction{Summary: "First part."}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.recordMeetingNotes(later, "Zoom", 3, "retry-call", "", &meetingFollowupExtraction{Summary: "Last part corrected."}); err != nil {
		t.Fatal(err)
	}
	notes := meetingNotesView(mustMeetingNotes(t, app, "retry-call"), nil)
	if notes.Summary != "First part.\n\nLast part corrected." || len(notes.Parts) != 2 || notes.StartTime != first.StartTime {
		t.Fatalf("retry truncated, duplicated or reordered the call: %+v", notes)
	}
}

func mustMeetingNotes(t *testing.T, app *App, key string) meetingNotesRecord {
	t.Helper()
	r, ok, err := app.activity.store.meetingNotesByKey(key)
	if err != nil || !ok {
		t.Fatalf("notes %s: ok=%v err=%v", key, ok, err)
	}
	return r
}
