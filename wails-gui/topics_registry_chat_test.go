package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func registryChatTestApp(t *testing.T) (*App, string, time.Time) {
	t.Helper()
	return registryChatTestAppAt(t, time.Now())
}

func registryChatTestAppAt(t *testing.T, now time.Time) (*App, string, time.Time) {
	t.Helper()
	isolateActivityPrivacyTest(t)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	a, tag := topicChatTestApp(t)
	configureTopicRegistryTest(a, now)
	a.topicSource = &registryTopicSource{app: a}
	seedThreadMoment(t, a, threadFixtureMoment{id: "atlas", title: "Atlas release ready", app: "Code", start: now.Add(-time.Hour), end: now.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	return a, tag, now
}

func TestTopicRegistryAdapterSendsToMainConversationAndReadsPanel(t *testing.T) {
	a, tag, _ := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	id := views[0].ID
	topic, keys, err := a.chatTopicSnapshot(t.Context(), id)
	if err != nil || topic.ID != id || topic.Name != "Atlas" || topic.Latest == "" || len(keys.ThreadKeys) != 1 || keys.ThreadKeys[0] != "obj:atlas" {
		t.Fatalf("adapter: %+v %+v %v", topic, keys, err)
	}
	runner := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = runner
	sent, err := a.SendMainChatWithTopic("Review this release", id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case prompt := <-runner.prompt:
		for _, text := range []string{topic.Name, topic.Status, topic.Latest} {
			if !strings.Contains(prompt, text) {
				t.Fatalf("model prompt missing %q", text)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner not called")
	}
	waitForTagIdle(t, a, tag)
	if sent.SessionID != tag {
		t.Fatal("created another conversation")
	}
	page, err := a.ListMainChatTopicItems(id, "", 50)
	if err != nil || len(page.Items) < 2 {
		t.Fatalf("panel: %+v %v", page, err)
	}
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == sent.UserItemID && row.Content != "Review this release" {
			t.Fatalf("context persisted in user bubble: %+v", row)
		}
	}
	following, err := a.GetUserTopics()
	if err != nil || len(following.Topics) != 0 {
		t.Fatalf("work entered search inputs: %+v %v", following, err)
	}
}

func TestTopicRegistryAdapterRejectsWithdrawnOrEditedOriginals(t *testing.T) {
	for _, change := range []string{"forgotten", "protected", "sensitive", "closed_eyes", "entity_edit"} {
		t.Run(change, func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			views, err := collectAndReadTopicViewsForTest(a)
			if err != nil || len(views) != 1 {
				t.Fatalf("views %+v %v", views, err)
			}
			id := views[0].ID
			switch change {
			case "forgotten":
				err = a.activity.store.DeleteSession("session-atlas")
			case "protected":
				_, err = a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`)
			case "sensitive":
				pii.SetUserSensitiveApps(map[string]string{"Code": "Code"})
			case "closed_eyes":
				a.closedEyes.enabled.Store(true)
			case "entity_edit":
				_, err = a.activity.store.db.Exec(`UPDATE activities SET entities='[{"type":"project","name":"Other"}]' WHERE id='atlas'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := a.chatTopicSnapshot(t.Context(), id); err == nil {
				t.Fatal("stale source authorized topic")
			}
			if _, err := a.SendMainChatWithTopic("Review", id); err == nil {
				t.Fatal("stale source admitted a turn")
			}
		})
	}
}

func TestTopicRegistryAdapterFollowingBindsNamingRecordAndOriginalActivity(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	it := testInterest("research", "Release tooling", "Current research")
	it.Kind = interests.KindResearch
	it.Evidence = []interests.Ref{{Source: interests.SourceActivity, Key: "activity:atlas", At: now.UnixMilli()}}
	storeTestInterests(t, a, it)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 2 {
		t.Fatalf("views %+v %v", views, err)
	}
	topic, _, err := a.chatTopicSnapshot(t.Context(), it.ID)
	if err != nil || topic.Name != it.Name {
		t.Fatalf("following: %+v %v", topic, err)
	}
	if _, err := a.interestsSvc().store.update(func(st *interestsStateFile) { st.Interests[0].Name = "Changed research" }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.chatTopicSnapshot(t.Context(), it.ID); err == nil {
		t.Fatal("old naming derivative authorized after correction")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.topicSource.TopicKeys(ctx, it.ID); err == nil {
		t.Fatal("cancellation masked")
	}
}

func TestTopicRegistryAdapterAliasesReturnRequestedID(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	seedThreadMoment(t, a, threadFixtureMoment{id: "second", title: "Release", app: "Code", start: now.Add(-2 * time.Hour), end: now.Add(-90 * time.Minute), entities: []Entity{{Type: entityChannel, Name: "Release"}}})
	before, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(before) != 2 {
		t.Fatalf("before %+v %v", before, err)
	}
	seedThreadMoment(t, a, threadFixtureMoment{id: "bridge", title: "Atlas release", app: "Code", start: now.Add(-20 * time.Minute), end: now.Add(-10 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}, {Type: entityChannel, Name: "Release"}}})
	after, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(after) != 1 {
		t.Fatalf("after %+v %v", after, err)
	}
	for _, view := range before {
		topic, keys, err := a.chatTopicSnapshot(t.Context(), view.ID)
		if err != nil || topic.ID != view.ID || len(keys.ThreadKeys) != 2 {
			t.Fatalf("alias: %+v %+v %v", topic, keys, err)
		}
	}
}

func TestTopicRegistryAdapterCalendarNextUsesOriginalEvent(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	if err := a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	event := proactiveVoiceTestEvent(t, a, now)
	// Keep the fixture upcoming on the registry day even near midnight.
	midnight := time.UnixMilli(startOfDayLocal(now.UnixMilli())).In(time.Local).AddDate(0, 0, 1)
	event.Start = now.Add(midnight.Sub(now) / 2)
	event.End = event.Start.Add(30 * time.Minute)
	event.Title = "Atlas sync"
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{calendarEventRow(event)}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 || views[0].Next == nil {
		t.Fatalf("next: %+v %v", views, err)
	}
	topic, _, err := a.chatTopicSnapshot(t.Context(), views[0].ID)
	if err != nil || topic.Next != "Atlas sync" {
		t.Fatalf("calendar context: %+v %v", topic, err)
	}
	event.Title = "Changed meeting"
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{calendarEventRow(event)}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.chatTopicSnapshot(t.Context(), views[0].ID); err == nil {
		t.Fatal("calendar edit authorized old next")
	}
}

func TestTopicRegistryAdapterFindOpenInvalidatesNewStoryMetadata(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	it := testInterest("research", "Release tooling", "Current research")
	it.Kind = interests.KindResearch
	it.Evidence = []interests.Ref{{Source: interests.SourceActivity, Key: "activity:atlas", At: now.UnixMilli()}}
	storeTestInterests(t, a, it)
	if _, err := a.topicsSvc().store.update(func(st *topicsStateFile) {
		st.Finds = topicFindsState{RanOn: now.Format("2006-01-02"), Items: []TopicFind{{ID: "new-find", TopicID: it.ID, Title: "New release", PublishedAt: now.Format("2006-01-02")}}, Sent: []topicSent{{ID: it.ID, ReadSources: &topicSentReadSources{Version: 1, UserNamed: true}}}}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	topic, _, err := a.chatTopicSnapshot(t.Context(), it.ID)
	if err != nil || topic.Status != TopicStatusNewStories {
		t.Fatalf("find: %+v %v", topic, err)
	}
	if _, err := a.topicsSvc().store.update(func(st *topicsStateFile) { st.Finds.Items[0].Opened = true }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.chatTopicSnapshot(t.Context(), it.ID); err == nil {
		t.Fatal("opened find authorized old new-stories status")
	}
}

func TestTopicRegistryAdapterUnsupportedClosureKeepsFollowingCard(t *testing.T) {
	a, tag, _ := registryChatTestApp(t)
	it := testInterest("legacy", "Release tooling", "Current research")
	it.Kind = interests.KindResearch
	it.Evidence = []interests.Ref{{Source: interests.SourceChat, Key: "chat:legacy"}}
	storeTestInterests(t, a, it)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 2 {
		t.Fatalf("following card lost: %+v %v", views, err)
	}
	before, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendMainChatWithTopic("Review", it.ID); err == nil {
		t.Fatal("unsupported closure admitted")
	}
	after, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil || len(after) != len(before) {
		t.Fatalf("rejected send persisted words: %d %d %v", len(before), len(after), err)
	}
}

func TestTopicRegistryAdapterAdmissionCollectsSourcesOnce(t *testing.T) {
	a, tag, now := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	collections := 0
	a.todayChatCollectionObserver = func() { collections++ }
	_, _, err = a.admitTodayChatTurn(t.Context(), tag, storage.StreamItemRow{ID: "one-collection", Kind: "user", Content: "Review", CreatedAt: now.UnixMilli()}, TodayChatContextRef{SourceKind: "topic", SourceID: views[0].ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if collections != 1 {
		t.Fatalf("one topic admission collected all sources %d times", collections)
	}
}

func TestTopicRegistryAdapterRejectsClockStaleMetadata(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	snapshot := a.topicsSvc().store.load().ChatTopics[0]
	a.topicsSvc().now = func() time.Time { return time.UnixMilli(snapshot.ValidUntil) }
	if _, _, err := a.chatTopicSnapshot(t.Context(), views[0].ID); err == nil {
		t.Fatal("clock-stale metadata admitted")
	}
	// Pure transition controls avoid depending on wall-clock date or time zone.
	now := topicRegistryTestNow
	midnight := time.UnixMilli(startOfDayLocal(now.UnixMilli())).In(time.Local).AddDate(0, 0, 1)
	for _, tc := range []struct {
		name string
		in   topicRegistryData
		want time.Time
	}{
		{"day", topicRegistryData{now: now}, midnight},
		{"meeting", topicRegistryData{now: now, meetings: []topicMeetingObservation{{StartAt: now.Add(time.Minute).UnixMilli()}}}, now.Add(time.Minute)},
		{"moving", topicRegistryData{now: now, activities: []ActivityRow{{EndMs: now.Add(-topicRegistryMovingFor + time.Minute).UnixMilli()}}}, now.Add(time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := topicChatValidUntil(tc.in); !got.Equal(tc.want) {
				t.Fatalf("expiry=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestTopicRegistryAdapterGoalProjectHasOriginalPurposeProof(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	a := bridge.app
	configureTopicRegistryTest(a, now)
	a.topicSource = &registryTopicSource{app: a}
	goal := mobileTestPurpose(t, store, "adapter-goal", PurposeKindGoal, "Ship Atlas", false, nil)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("goal: %+v %v", views, err)
	}
	topic, keys, err := a.chatTopicSnapshot(t.Context(), views[0].ID)
	if err != nil || topic.Name != goal.Current.Title || len(keys.GoalIDs) != 1 || keys.GoalIDs[0] != goal.OpenLoop.ID {
		t.Fatalf("purpose proof: %+v %+v %v", topic, keys, err)
	}
}

func TestTopicRegistryUnchangedRefreshKeepsChatDerivation(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	before := a.topicsSvc().store.load().ChatTopics
	a.topicsSvc().now = func() time.Time { return now.Add(time.Second) }
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	after := a.topicsSvc().store.load().ChatTopics
	if proactiveVoiceHash(before) != proactiveVoiceHash(after) {
		t.Fatal("unchanged refresh churned source-bound chat metadata")
	}
}

func TestTopicRegistryFollowingGoalAdmissionReusesPurposeCollection(t *testing.T) {
	a, tag, now := registryChatTestApp(t)
	goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "research-goal", PurposeKindGoal, "Evaluate release tooling", false, nil)
	it := testInterest("research-goal", "Release tooling research", "A current research goal")
	it.Kind = interests.KindResearch
	it.Evidence = []interests.Ref{{Source: interests.SourceGoal, Key: "goal:" + goal.OpenLoop.ID, At: goal.Current.OccurredAt}}
	storeTestInterests(t, a, it)
	_, err := collectAndReadTopicViewsForTest(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.chatTopicSnapshot(t.Context(), it.ID); err != nil {
		t.Fatalf("pre-admission source unavailable: %v", err)
	}
	collections := 0
	a.todayChatCollectionObserver = func() { collections++ }
	_, _, err = a.admitTodayChatTurn(t.Context(), tag, storage.StreamItemRow{ID: "goal-one-collection", Kind: "user", Content: "Review research", CreatedAt: now.UnixMilli()}, TodayChatContextRef{SourceKind: "topic", SourceID: it.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if collections != 1 {
		t.Fatalf("following goal admission collected all sources %d times", collections)
	}
}

func TestTopicRegistryAdmissionProvesOriginalIDsAfterPromptRedaction(t *testing.T) {
	a, tag, now := registryChatTestApp(t)
	writeTodayContextMemory(t, "atlas-123456", "# Atlas\nAtlas release checklist")
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	result, _, err := a.admitTodayChatTurn(t.Context(), tag, storage.StreamItemRow{ID: "redacted-identity", Kind: "user", Content: "Review", CreatedAt: now.UnixMilli()}, TodayChatContextRef{SourceKind: "topic", SourceID: views[0].ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, `"id":"atlas-123456"`) {
		t.Fatal("source ID was not redacted in prompt fixture")
	}
	var metadata struct {
		TopicContext topicChatProof `json:"topicContext"`
	}
	if err := json.Unmarshal([]byte(result.topicMetadata), &metadata); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range metadata.TopicContext.Refs {
		if ref.SourceKind == "memory" && ref.SourceID == "atlas-123456" {
			found = true
		}
	}
	if !found {
		t.Fatalf("original packed identity missing: %+v", metadata.TopicContext.Refs)
	}
}
