package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func mobileTestThreadBridge(t *testing.T) (*mobileBridge, *handoff.Store, time.Time) {
	t.Helper()
	bridge, _, now := mobileTestGoalBridge(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.handoffStore = store
	return bridge, store, now
}

func mobileTestDecodeThread(t *testing.T, body string) MobileTaskDetail {
	t.Helper()
	var reply struct {
		Data MobileTaskDetail `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return reply.Data
}

func TestMobileTaskThreadProjectsOriginEventsOutcomeAndActions(t *testing.T) {
	bridge, store, now := mobileTestThreadBridge(t)
	at := now.Add(-time.Hour).UnixMilli()
	entry := handoff.Entry{ID: "sow", Title: "Send Priya the signed SOW", Status: handoff.StatusNeedsReview,
		Workdir: "/Users/private/work", CreatedAt: at, UpdatedAt: at, Thread: &handoff.Thread{
			Origin: &handoff.Origin{Kind: handoff.OriginUpNext, At: at},
		}}
	entry.Thread.AppendEvent(handoff.Event{ID: "q", Kind: handoff.EventIsoform, At: at,
		Text:    "Priya asked for the signed SOW by today.\n\nSend the signed v3 now, or wait for Lena?",
		Choices: []handoff.Choice{{ID: "now", Label: "Send v3 now"}, {ID: "wait", Label: "Wait for Lena"}}})
	entry.Thread.AppendEvent(handoff.Event{ID: "f", Kind: handoff.EventFile, At: at, Path: "/Users/private/work/headlines.md"})
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/tasks/sow", "")
	if code != http.StatusOK {
		t.Fatalf("detail answered %d %s", code, body)
	}
	if strings.Contains(body, "/Users/private") {
		t.Fatalf("a file's folder reached the phone: %s", body)
	}
	detail := mobileTestDecodeThread(t, body)
	if detail.Origin == nil || detail.Origin.Kind != handoff.OriginUpNext || detail.Origin.At == "" {
		t.Fatalf("origin = %+v", detail.Origin)
	}
	if len(detail.Events) != 2 || !strings.Contains(detail.Events[0].Text, "\n\nSend the signed v3") ||
		len(detail.Events[0].Choices) != 2 || detail.Events[1].File == nil || detail.Events[1].File.Name != "headlines.md" {
		t.Fatalf("events = %+v", detail.Events)
	}
	if strings.Join(detail.Actions, ",") != "mark_done,rename,not_now,made" {
		t.Fatalf("actions = %v", detail.Actions)
	}
	if !detail.Unread {
		t.Fatal("a task changed since it was opened should be unread")
	}
}

func TestMobileTaskWithoutThreadKeepsTheOlderShape(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	if _, err := store.Save(handoff.Entry{ID: "plain", Title: "Plain work", Status: handoff.StatusInProgress}); err != nil {
		t.Fatal(err)
	}
	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/tasks/plain", "")
	if code != http.StatusOK || strings.Contains(body, `"origin"`) || strings.Contains(body, `"outcome"`) {
		t.Fatalf("plain task answered %d %s", code, body)
	}
	detail := mobileTestDecodeThread(t, body)
	if detail.Events == nil || len(detail.Events) != 0 || strings.Join(detail.Actions, ",") != "rename,stop" {
		t.Fatalf("plain thread = %+v", detail)
	}
}

func TestMobileTaskDoneRowCarriesItsOutcomeOnly(t *testing.T) {
	now := time.Date(2026, 10, 30, 17, 12, 0, 0, time.UTC)
	done := func(id string, outcome *handoff.Outcome) handoff.Entry {
		return handoff.Entry{ID: id, Title: id, Status: handoff.StatusCompleted, UpdatedAt: now.Add(-time.Hour).UnixMilli(),
			Thread: &handoff.Thread{Outcome: outcome}}
	}
	page := mobileTaskPage([]handoff.Entry{
		done("sent", &handoff.Outcome{Kind: handoff.OutcomeSent, By: handoff.OutcomeByIsoform, At: now.UnixMilli(),
			Params: map[string]string{"person": "Priya", "thing": "the signed SOW"}}),
		done("summary-missing", &handoff.Outcome{Kind: handoff.OutcomeCompleted, By: handoff.OutcomeByIsoform}),
		done("none", nil),
	}, nil, now)
	if !page.Threads {
		t.Fatal("the page should tell phones this Mac serves threads")
	}
	byID := map[string]MobileTaskView{}
	for _, task := range page.Tasks {
		byID[task.ID] = task
	}
	sent := byID["sent"].Outcome
	if sent == nil || sent.Kind != handoff.OutcomeSent || sent.By != handoff.OutcomeByIsoform ||
		sent.Params["person"] != "Priya" || sent.At != now.Format(time.RFC3339) {
		t.Fatalf("sent outcome = %+v", sent)
	}
	if byID["summary-missing"].Outcome != nil || byID["none"].Outcome != nil {
		t.Fatal("a Done task without an outcome sentence must not carry an outcome")
	}
	if byID["sent"].Unread {
		t.Fatal("Done tasks are never unread")
	}
}

func TestMobileTaskActionsMarkDoneRenameChooseAndScope(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	review := handoff.Entry{ID: "review", Title: "Old title", Status: handoff.StatusNeedsReview, Thread: &handoff.Thread{}}
	review.Thread.AppendEvent(handoff.Event{ID: "q", Kind: handoff.EventIsoform, Text: "Now or later?",
		Choices: []handoff.Choice{{ID: "now", Label: "Send v3 now"}}})
	for _, entry := range []handoff.Entry{review, {ID: "running", Title: "Running", Status: handoff.StatusInProgress}} {
		if _, err := store.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	reader := mobileTestGoalDevice(mobileScopeBriefRead)
	if code, body := mobileTestGoalCall(t, bridge, reader, http.MethodPost, "/tasks/review/actions",
		`{"idempotencyKey":"k0","action":"mark_done"}`); code != http.StatusForbidden || !strings.Contains(body, mobileScopeHandoff) {
		t.Fatalf("action without handoff:create answered %d %s", code, body)
	}
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff)
	call := func(path, body string) (int, string) {
		return mobileTestGoalCall(t, bridge, device, http.MethodPost, path, body)
	}
	if code, _ := call("/tasks/review/actions", `{"idempotencyKey":"k1","action":"explode"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown action answered %d", code)
	}
	if code, _ := call("/tasks/review/actions", `{"idempotencyKey":"k2","action":"rename","title":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty rename answered %d", code)
	}
	code, body := call("/tasks/review/actions", `{"idempotencyKey":"k3","action":"rename","title":"Send Priya the signed SOW"}`)
	if code != http.StatusOK || mobileTestDecodeThread(t, body).Title != "Send Priya the signed SOW" {
		t.Fatalf("rename answered %d %s", code, body)
	}
	code, body = call("/tasks/review/actions", `{"idempotencyKey":"k4","action":"choose","eventId":"q","choiceId":"now"}`)
	detail := mobileTestDecodeThread(t, body)
	if code != http.StatusOK || detail.Events[0].ChosenID != "now" || detail.Events[1].Kind != handoff.EventYou ||
		detail.Events[1].Text != "Send v3 now" {
		t.Fatalf("choose answered %d %+v", code, detail.Events)
	}
	if code, _ := call("/tasks/review/actions", `{"idempotencyKey":"k5","action":"choose","eventId":"q","choiceId":"now"}`); code != http.StatusConflict {
		t.Fatalf("a second tap answered %d", code)
	}
	if code, _ := call("/tasks/running/actions", `{"idempotencyKey":"k6","action":"mark_done"}`); code != http.StatusConflict {
		t.Fatalf("marking running work done answered %d", code)
	}
	code, body = call("/tasks/review/actions", `{"idempotencyKey":"k7","action":"mark_done"}`)
	if code != http.StatusOK {
		t.Fatalf("mark done answered %d %s", code, body)
	}
	detail = mobileTestDecodeThread(t, body)
	if detail.Status != string(handoff.StatusCompleted) || detail.Outcome == nil ||
		detail.Outcome.Kind != handoff.OutcomeMarkedDone || detail.Outcome.By != handoff.OutcomeByYou {
		t.Fatalf("marked done = %+v", detail)
	}
	if replayCode, replay := call("/tasks/review/actions", `{"idempotencyKey":"k7","action":"mark_done"}`); replayCode != code || replay != body {
		t.Fatalf("replay = %d %s", replayCode, replay)
	}
}

func TestMobileTaskOpenedAndNotNowKeepTheTaskInPlace(t *testing.T) {
	bridge, store, now := mobileTestThreadBridge(t)
	updated := now.Add(-time.Hour).UnixMilli()
	if _, err := store.Save(handoff.Entry{ID: "wait", Title: "Waiting", Status: handoff.StatusNeedsReview, UpdatedAt: updated}); err != nil {
		t.Fatal(err)
	}
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff)
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/wait/actions", `{"idempotencyKey":"o1","action":"opened"}`)
	detail := mobileTestDecodeThread(t, body)
	if code != http.StatusOK || detail.Unread || detail.UpdatedAt != time.UnixMilli(updated).UTC().Format(time.RFC3339) {
		t.Fatalf("opened answered %d %+v", code, detail)
	}
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/wait/actions", `{"idempotencyKey":"o2","action":"not_now"}`); code != http.StatusOK {
		t.Fatalf("not now answered %d %s", code, body)
	}
	_, list := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/tasks", "")
	if strings.Contains(list, `"id":"wait"`) {
		t.Fatalf("a Not now task stays out of Needs you: %s", list)
	}
}

func TestMobileTaskReplyStaysInTheTask(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	if _, err := store.Save(handoff.Entry{ID: "waiting", Title: "Pricing FAQ", Status: handoff.StatusActive, Body: "Write the FAQ."}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(handoff.Entry{ID: "chatty", Title: "Pricing page", Status: handoff.StatusNeedsReview, ChatSessionID: mobileTestChatID}); err != nil {
		t.Fatal(err)
	}
	if code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead), http.MethodPost,
		"/tasks/waiting/messages", `{"idempotencyKey":"r0","text":"Keep it short"}`); code != http.StatusForbidden || !strings.Contains(body, mobileScopeChatWrite) {
		t.Fatalf("reply without chat:write answered %d %s", code, body)
	}
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeChatWrite)
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/waiting/messages", `{"idempotencyKey":"r1","text":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("blank reply answered %d", code)
	}
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/waiting/messages", `{"idempotencyKey":"r2","text":"Keep it short"}`)
	detail := mobileTestDecodeThread(t, body)
	if code != http.StatusOK || len(detail.Events) != 1 || detail.Events[0].Kind != handoff.EventYou || detail.Events[0].Text != "Keep it short" {
		t.Fatalf("reply to waiting work answered %d %+v", code, detail)
	}
	saved, _ := store.Get("waiting")
	if !strings.Contains(saved.Body, "## Notes from you\n\nKeep it short") {
		t.Fatalf("a reply before the work starts must shape its brief: %q", saved.Body)
	}
	if turns.count() != 0 {
		t.Fatal("a reply to unstarted work must not start a chat turn")
	}

	code, body = mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/chatty/messages", `{"idempotencyKey":"r3","text":"Make the headline shorter"}`)
	if code != http.StatusOK {
		t.Fatalf("reply to a task with its chat answered %d %s", code, body)
	}
	mobileTestWaitFor(t, "the task's chat to answer", func() bool {
		_, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/tasks/chatty", "")
		events := mobileTestDecodeThread(t, body).Events
		return len(events) == 2 && events[1].Kind == handoff.EventIsoform && events[1].Text == "Canned reply."
	})
	if turns.count() != 1 {
		t.Fatalf("the reply should be the task chat's next turn, got %d turns", turns.count())
	}
	mainChat, _ := bridge.app.store.LoadStreamItems(t.Context(), mobileTestChatID)
	if len(mainChat) == 0 {
		t.Fatal("the reply should land in the task's own chat")
	}
}

func TestMobileTaskChoiceRetriesBusyAndFailedTurns(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	thread := &handoff.Thread{}
	thread.AppendEvent(handoff.Event{ID: "q", Kind: handoff.EventIsoform, Text: "Which?",
		Choices: []handoff.Choice{{ID: "short", Label: "Keep it short"}}})
	if _, err := store.Save(handoff.Entry{ID: "choice", Title: "Choice", Status: handoff.StatusNeedsReview,
		ChatSessionID: mobileTestChatID, Thread: thread}); err != nil {
		t.Fatal(err)
	}
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff)
	body := `{"idempotencyKey":"choice-retry","action":"choose","eventId":"q","choiceId":"short"}`
	call := func() (int, string) {
		return mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/choice/actions", body)
	}
	assertOpen := func() {
		t.Helper()
		saved, err := store.Get("choice")
		if err != nil || len(saved.Thread.Events) != 1 || saved.Thread.Events[0].ChosenID != "" {
			t.Fatalf("choice must remain open: %+v, %v", saved.Thread, err)
		}
	}
	bridge.app.mu.Lock()
	bridge.app.activeTags[mobileTestChatID] = true
	bridge.app.mu.Unlock()
	if code, response := call(); code != http.StatusConflict {
		t.Fatalf("busy choice = %d %s", code, response)
	}
	assertOpen()
	bridge.app.mu.Lock()
	delete(bridge.app.activeTags, mobileTestChatID)
	bridge.app.mu.Unlock()
	// The fixture's project path does not exist, so real dispatch fails before
	// writing a user row. The same key must remain retryable.
	if code, response := call(); code != http.StatusServiceUnavailable {
		t.Fatalf("failed choice = %d %s", code, response)
	}
	assertOpen()
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	code, response := call()
	if code != http.StatusOK {
		t.Fatalf("retry = %d %s", code, response)
	}
	mobileTestWaitFor(t, "choice answer", func() bool {
		_, response := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/tasks/choice", "")
		events := mobileTestDecodeThread(t, response).Events
		return len(events) == 3 && events[0].ChosenID == "short" && events[1].Text == "Keep it short" && events[2].Text == "Canned reply."
	})
	if turns.count() != 1 {
		t.Fatalf("expected one turn, got %d", turns.count())
	}
}

func TestMobileTaskChoiceBeforeStartShapesBrief(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	thread := &handoff.Thread{}
	thread.AppendEvent(handoff.Event{ID: "q", Kind: handoff.EventIsoform, Text: "Which?",
		Choices: []handoff.Choice{{ID: "short", Label: "Keep it short"}}})
	if _, err := store.Save(handoff.Entry{ID: "waiting-choice", Title: "FAQ", Status: handoff.StatusActive,
		Body: "Write the FAQ.", Thread: thread}); err != nil {
		t.Fatal(err)
	}
	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff)
	code, response := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/waiting-choice/actions",
		`{"idempotencyKey":"choice","action":"choose","eventId":"q","choiceId":"short"}`)
	if code != http.StatusOK {
		t.Fatalf("choice = %d %s", code, response)
	}
	saved, err := store.Get("waiting-choice")
	if err != nil || !strings.Contains(saved.Body, "## Notes from you\n\nKeep it short") {
		t.Fatalf("execution brief = %q, %v", saved.Body, err)
	}
}

func TestMobileTaskReplyCompletionCrossesRelay(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	identity, err := createMobileHubIdentity(bridge.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	relay := newMobileTestRelay(t, identity.Credential, identity.HubID)
	bridge.mu.Lock()
	bridge.identity = &identity
	bridge.mu.Unlock()
	if err := bridge.enableRelay(relay.url(), false); err != nil {
		t.Fatal(err)
	}
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead, mobileScopeChatRead, mobileScopeChatWrite})
	conn := relay.acceptRegistration(t, mobileTestListed(phone))
	if _, err := store.Save(handoff.Entry{ID: "relay-task", Title: "FAQ", Status: handoff.StatusNeedsReview,
		ChatSessionID: mobileTestChatID}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	mobileChatRunnerOverride = func(app *App, chatID, turnID, prompt string, cancelled <-chan struct{}) {
		select {
		case <-release:
		case <-cancelled:
			return
		}
		app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: chatID, Content: "Delayed answer."})
		app.emitAgentEvent(AgentEvent{Type: "close", Tag: chatID})
	}
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	send := func(seq uint64, id, method, path, body string) {
		t.Helper()
		raw, err := json.Marshal(mobileRequestPlaintext{ID: id, Method: method, Path: path, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		conn.send(t, map[string]any{"t": "in", "env": phone.sealRequest(t, bridge.identity.Public, bridge.identity.HubID, seq, raw)})
	}
	next := func() (mobileEnvelope, []byte) {
		t.Helper()
		frame := relay.nextFrame(t, "out")
		var out mobileEventFrame
		if err := json.Unmarshal(frame.raw, &out); err != nil {
			t.Fatal(err)
		}
		return out.Env, phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, out.Env)
	}
	send(1, mobileTestOtherID, http.MethodPost, "/tasks/relay-task/messages", `{"idempotencyKey":"reply","text":"Short please"}`)
	for {
		env, raw := next()
		if env.Kind != mobileKindResponse {
			continue
		}
		var response mobileResponsePlaintext
		if err := json.Unmarshal(raw, &response); err != nil || response.Status != http.StatusOK || strings.Contains(response.Body, "Delayed answer") {
			t.Fatalf("immediate reply = %s, %v", raw, err)
		}
		break
	}
	close(release)
	for {
		env, raw := next()
		if env.Kind != mobileKindEvent {
			continue
		}
		var event mobileChatTurnEvent
		if json.Unmarshal(raw, &event) == nil && event.Event == mobileEventChatTurn && event.State == mobileChatEventDone && event.ChatID == mobileTestChatID {
			break
		}
	}
	send(2, mobileTestDeviceID, http.MethodGet, "/tasks/relay-task", "")
	for {
		env, raw := next()
		if env.Kind != mobileKindResponse {
			continue
		}
		var response mobileResponsePlaintext
		if err := json.Unmarshal(raw, &response); err != nil || response.Status != http.StatusOK || !strings.Contains(response.Body, "Delayed answer.") {
			t.Fatalf("refreshed task = %s, %v", raw, err)
		}
		break
	}
}

func TestRecordHandoffThreadResultAddsMessageFilesAndOutcome(t *testing.T) {
	entry := handoff.Entry{ID: "hf", AttemptID: "a1", Status: handoff.StatusCompleted}
	transcript := "Wrote three headlines for the pricing page. They are in the repo.\n\n<deliverables>\n/Users/me/repo/headlines.md | Headlines\n</deliverables>"
	recordHandoffThreadResult(&entry, transcript, 42)
	thread := entry.Thread
	if thread == nil || len(thread.Events) != 2 || thread.Events[0].Kind != handoff.EventIsoform ||
		strings.Contains(thread.Events[0].Text, "deliverables") || thread.Events[1].Kind != handoff.EventFile ||
		thread.Events[1].Path != "/Users/me/repo/headlines.md" {
		t.Fatalf("thread = %+v", thread)
	}
	if thread.Outcome == nil || thread.Outcome.Params["summary"] != "Wrote three headlines for the pricing page." {
		t.Fatalf("outcome = %+v", thread.Outcome)
	}
	review := handoff.Entry{ID: "hf2", Status: handoff.StatusNeedsReview}
	recordHandoffThreadResult(&review, "Partly done.", 42)
	if review.Thread.Outcome != nil {
		t.Fatal("work that needs review has no outcome yet")
	}
}

func TestMobileHandoffCreateRecordsItsOriginAndStatusAdvertisesThreads(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	device := mobileTestGoalDevice(mobileScopeHandoff)
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/handoffs",
		`{"idempotencyKey":"c0","title":"T","brief":"B","workDir":"","origin":{"kind":"elsewhere"}}`); code != http.StatusBadRequest {
		t.Fatalf("unknown origin answered %d", code)
	}
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/handoffs",
		`{"idempotencyKey":"c1","title":"Thank Maya for the Lena intro","brief":"Write it","workDir":"","origin":{"kind":"up_next"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create answered %d %s", code, body)
	}
	var reply struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(body), &reply)
	saved, err := store.Get(reply.Data.ID)
	if err != nil || saved.Thread == nil || saved.Thread.Origin == nil || saved.Thread.Origin.Kind != handoff.OriginUpNext ||
		saved.Thread.Origin.At == 0 {
		t.Fatalf("saved origin = %+v %v", saved.Thread, err)
	}
	_, status := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/status", "")
	if !strings.Contains(status, `"taskThreads":true`) {
		t.Fatalf("status does not advertise task threads: %s", status)
	}
}

func TestMobileScheduledTasksListBriefsAndRoutinesDueSoon(t *testing.T) {
	now := time.Date(2026, 10, 30, 17, 12, 0, 0, time.UTC)
	at := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	items := []UpcomingItem{
		{ID: "brief:evening", Kind: upcomingKindBrief, Slot: "evening", Enabled: true, NextRunAt: at(2 * time.Hour)},
		{ID: "job1", Kind: upcomingKindSchedule, JobID: "job1", Name: "Weekly update for the team", Enabled: true, NextRunAt: at(time.Hour)},
		{ID: "job2", Kind: upcomingKindSchedule, JobID: "job2", Name: "Paused routine", Enabled: false, NextRunAt: at(time.Hour)},
		{ID: "job3", Kind: upcomingKindSchedule, JobID: "job3", Name: "Next week", Enabled: true, NextRunAt: at(72 * time.Hour)},
		{ID: "news", Kind: upcomingKindNews, JobID: "news", Enabled: true, NextRunAt: at(time.Hour)},
		{ID: "job4", Kind: upcomingKindSchedule, JobID: "job4", Name: "Reply about Halcyon SOW", Enabled: true, NextRunAt: at(time.Hour)},
		{ID: "sys", Kind: upcomingKindSchedule, JobID: "sys", Name: "System", Enabled: true, NextRunAt: at(time.Hour), System: true},
	}
	rows := mobileScheduledTasks(items, []string{mobileGoalFold("Halcyon SOW")}, now)
	if len(rows) != 2 || rows[0].ID != "routine.job1" || rows[0].Title != "Weekly update for the team" ||
		rows[1].ID != "brief.evening" || rows[1].Slot != "evening" || rows[1].Title != "" {
		t.Fatalf("scheduled = %+v", rows)
	}
}

func TestMobileTaskRunnerNamesTheExternalAssistant(t *testing.T) {
	for provider, want := range map[string]string{"claude-code": "claude_code", "Codex": "codex", "managed": "", "": ""} {
		if got := mobileTaskRunner(provider); got != want {
			t.Fatalf("runner(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestMobileSuggestionEvidenceNeedsADatedObservation(t *testing.T) {
	if mobileSuggestionEvidence(3, 2, nil) != nil {
		t.Fatal("no observation, no fact")
	}
	evidence := mobileSuggestionEvidence(3, 2, []InitiativeObservation{
		{SourceKind: "activity", ObservedAt: 1000}, {SourceKind: "claude_code", ObservedAt: 5000},
	})
	if evidence == nil || evidence.Source != "claude_code" || evidence.At != time.UnixMilli(5000).UTC().Format(time.RFC3339) ||
		evidence.Text != "You did this 3 times across 2 days." || evidence.TextLocalized["zh-CN"] != "你在 2 天里做了 3 次。" {
		t.Fatalf("evidence = %+v", evidence)
	}
	if mobileOfferDeckKind(evidence.Source) != "claude_code" || mobileOfferDeckKind("mail") != "reply" ||
		mobileOfferDeckKind("calendar") != "your_day" || mobileOfferDeckKind("screen") != "" {
		t.Fatal("deck kinds")
	}
}
