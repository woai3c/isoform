package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

func TestMobileWeekDeclineIsReceiptedAndStoredForTheWeek(t *testing.T) {
	app, brief, now := yourWeekFrozenWindowFixture(t, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		"2026-10-19", intPointer(5), 3, []yourWeekSnapshotRef{{ID: "t-halcyon", Minutes: 360}})
	bridge, err := newMobileBridge(app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	delivered, ok := bridge.app.todayChatDeliveredBrief(brief.BriefID)
	if !ok || delivered.YourWeek == nil {
		t.Fatal("week not delivered")
	}
	path := "/briefs/" + brief.BriefID + "/week-decline"
	get := path + "?" + url.Values{"version": {delivered.ConversationKey}}.Encode()
	reader := mobileTestDevice(mobileScopeBriefRead)
	if status := mobileTestChatCall(bridge, reader, http.MethodGet, "/status", ""); status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"briefWeekDecline":true`) {
		t.Fatalf("missing decline capability: %d %s", status.Code, status.Body.String())
	}
	if got := mobileTestChatCall(bridge, reader, http.MethodGet, get, ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"declined":false`) {
		t.Fatalf("initial: %d %s", got.Code, got.Body.String())
	}
	body := `{"idempotencyKey":"decline-week","version":"` + delivered.ConversationKey + `"}`
	if got := mobileTestChatCall(bridge, reader, http.MethodPost, path, body); got.Code != http.StatusForbidden {
		t.Fatalf("missing write scope: %d", got.Code)
	}
	writer := mobileTestDevice(mobileScopeBriefRead, mobileScopeChatWrite)
	first := mobileTestChatCall(bridge, writer, http.MethodPost, path, body)
	second := mobileTestChatCall(bridge, writer, http.MethodPost, path, body)
	if first.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("receipt: %d %s / %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if got := mobileTestChatCall(bridge, reader, http.MethodGet, get, ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"declined":true`) {
		t.Fatalf("stored: %d %s", got.Code, got.Body.String())
	}
	if declined, err := app.GetTodayYourWeekDeclined(delivered.YourWeek.Start); err != nil || !declined {
		t.Fatalf("phone decline was not visible on the computer: %t %v", declined, err)
	}
	stale := path + "?" + url.Values{"version": {strings.Repeat("0", 64)}}.Encode()
	if got := mobileTestChatCall(bridge, reader, http.MethodGet, stale, ""); got.Code != http.StatusConflict {
		t.Fatalf("stale: %d %s", got.Code, got.Body.String())
	}
}

func TestMobileWeekDeclineReadsComputerAndLegacyChoices(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "computer", true: "legacy phone"}[legacy], func(t *testing.T) {
			app, brief, now := yourWeekFrozenWindowFixture(t, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
				"2026-10-19", intPointer(5), 3, []yourWeekSnapshotRef{{ID: "t-halcyon", Minutes: 360}})
			bridge, err := newMobileBridge(app, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			dir, err := mobileBriefStateDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := newMobileBriefStore(dir).save(brief, now); err != nil {
				t.Fatal(err)
			}
			delivered, ok := app.todayChatDeliveredBrief(brief.BriefID)
			if !ok || delivered.YourWeek == nil {
				t.Fatal("week not delivered")
			}
			week := delivered.YourWeek
			if legacy {
				err = app.store.SetUIState(context.Background(), todayYourWeekLegacyDeclineKey(week.Start, week.End), "true")
			} else {
				err = app.DeclineTodayYourWeek(week.Start)
			}
			if err != nil {
				t.Fatal(err)
			}
			get := "/briefs/" + brief.BriefID + "/week-decline?" + url.Values{"version": {delivered.ConversationKey}}.Encode()
			got := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefRead), http.MethodGet, get, "")
			if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"declined":true`) {
				t.Fatalf("phone did not see decline: %d %s", got.Code, got.Body.String())
			}
			if declined, err := app.GetTodayYourWeekDeclined(week.Start); err != nil || !declined {
				t.Fatalf("computer did not see decline: %t %v", declined, err)
			}
		})
	}
}

func TestMobileStatusAdvertisesMergedCapabilities(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	status := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefRead), http.MethodGet, "/status", "")
	if status.Code != http.StatusOK {
		t.Fatalf("status: %d", status.Code)
	}
	for _, capability := range []string{"topics", "taskThreads", "sees", "firstRead", "todayMeetingPrep", "notificationTest", "briefRelevanceFeedback", "briefAsk", "briefWeekDecline"} {
		if !strings.Contains(status.Body.String(), `"`+capability+`":true`) {
			t.Errorf("missing capability %s", capability)
		}
	}
}

func TestMobileWeekTopicAskRevalidatesItsBriefAndTopic(t *testing.T) {
	app, brief, now := yourWeekFrozenWindowFixture(t, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		"2026-10-19", intPointer(5), 3, []yourWeekSnapshotRef{{ID: "t-halcyon", Minutes: 360}})
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	delivered, ok := app.todayChatDeliveredBrief(brief.BriefID)
	if !ok || delivered.YourWeek == nil || delivered.YourWeek.Ask == nil || delivered.YourWeek.Ask.TopicID == "" {
		t.Fatal("fixture needs a topic-based week Ask")
	}
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 2)}
	app.runner = runner
	app.sessionTempDirs = map[string]string{}
	app.streams = map[string]context.CancelFunc{}
	app.runIDByTag = map[string]string{}
	app.pendingTraceIDs = map[string]string{}
	app.eventBroadcaster = NewEventBroadcaster()
	bridge, err := newMobileBridge(app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	ask := delivered.YourWeek.Ask
	if _, err := app.SaveMemoryEntry("week-topic-source", "# Halcyon\nPrepare the launch plan."); err != nil {
		t.Fatal(err)
	}
	refs := []TodayChatContextRef{{SourceKind: "memory", SourceID: "week-topic-source"}}
	fingerprint, err := app.chatTopicSourceFingerprint(t.Context(), refs)
	if err != nil {
		t.Fatal(err)
	}
	clock := app.topicsSvc().now()
	if _, err := app.topicsSvc().store.update(func(state *topicsStateFile) {
		state.ChatTopics = append(state.ChatTopics, topicChatSnapshot{Topic: ChatTopic{ID: ask.TopicID, Name: "Halcyon", Refs: refs, SourceFingerprint: fingerprint},
			DerivedAt: clock.Add(-time.Minute).UnixMilli(), ValidUntil: clock.Add(time.Hour).UnixMilli()})
	}); err != nil {
		t.Fatal(err)
	}
	app.topicSource = &registryTopicSource{app: app}
	if _, err := app.topicSource.TopicByID(t.Context(), ask.TopicID); err != nil {
		t.Fatalf("registry topic: %v", err)
	}
	input := mobileBriefChatInput{IdempotencyKey: "week-topic", BriefID: brief.BriefID, BriefVersion: delivered.ConversationKey,
		Ask: true, WeekTopicID: ask.TopicID, WeekMessage: ask.Message, Text: ask.Message}
	ref, _, err := app.mobileBriefChatReference(input, input.Text)
	if err != nil {
		t.Fatalf("week reference: %v", err)
	}
	sources := app.collectTodayChatSources(t.Context(), time.Now())
	current, ok := app.todayChatDeliveredBriefFrom(t.Context(), brief.BriefID, &sources)
	if !ok || current.YourWeek == nil || current.YourWeek.Ask == nil {
		t.Fatalf("week disappeared while resolving: ok=%v week=%+v", ok, current.YourWeek)
	}
	if ref.SourceID != mobileBriefConversationKey(current) || mobileBriefModelDigest(current.YourWeek.Ask.TopicID+"\x00"+current.YourWeek.Ask.Message) != ref.ParagraphDigest {
		t.Fatalf("week reference changed: version %q/%q topic %q/%q digest %q", ref.SourceID, mobileBriefConversationKey(current), ask.TopicID, current.YourWeek.Ask.TopicID, ref.ParagraphDigest)
	}
	if _, err := app.resolveTodayChatTopic(t.Context(), ask.TopicID, sources); err != nil {
		t.Fatalf("topic source: %v", err)
	}
	if _, _, err := app.loadTodayChatContextItem(t.Context(), ref); err != nil {
		t.Fatalf("week source: %v", err)
	}
	path := "/chats/" + main.LocalID + "/brief-messages"
	bad := input
	bad.IdempotencyKey = "forged"
	bad.WeekMessage = "Invented words"
	if got := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path, mobileBriefChatBody(t, bad)); got.Code != http.StatusConflict {
		t.Fatalf("forged week Ask: %d %s", got.Code, got.Body.String())
	}
	got := mobileTestChatCall(bridge, mobileBriefChatDevice(), http.MethodPost, path, mobileBriefChatBody(t, input))
	if got.Code != http.StatusAccepted {
		t.Fatalf("week Ask: %d %s", got.Code, got.Body.String())
	}
	select {
	case task := <-runner.inputs:
		prompt, _ := task.PromptFunc()
		if !task.NoTools || !strings.Contains(prompt, "Halcyon") {
			t.Fatal("week Ask lost its topic context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("week Ask did not start")
	}
	waitForTagIdle(t, app, main.LocalID)
}
