package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func mainChatRowTopicFixture(t *testing.T) (*App, string, string) {
	t.Helper()
	a, session, _ := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("registry %+v %v", views, err)
	}
	return a, session, views[0].ID
}
func mainChatRowTopicCard(id string) mainChatProactive {
	return mainChatProactive{MomentID: id, Kind: "goal_check_in", State: "pending", Evidence: []mainChatEvidenceRef{{Kind: "activity", ID: "atlas"}}}
}
func mainChatRowStoredTags(t *testing.T, a *App, session, id string) []string {
	t.Helper()
	m, err := a.store.LoadMainChatRowMetadata(t.Context(), session, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	return m[id].TopicIDs
}
func mainChatRowPageItem(t *testing.T, a *App, session, id string) StreamItemData {
	t.Helper()
	page, err := a.LoadStreamItemsPage(session, -1, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing %s", id)
	return StreamItemData{}
}
func TestMainChatRowTopicsPublicationAndAllReadSurfaces(t *testing.T) {
	a, session, topic := mainChatRowTopicFixture(t)
	id := a.postProactiveItem(mainChatRowTopicCard("card"), "card")
	if tags := mainChatRowStoredTags(t, a, session, id); !reflect.DeepEqual(tags, []string{topic}) {
		t.Fatalf("persisted tags %v", tags)
	}
	item := mainChatRowPageItem(t, a, session, id)
	if !reflect.DeepEqual(item.TopicIDs, []string{topic}) || item.RowClass != "waiting" || item.LocalDay == "" {
		t.Fatalf("renderer %+v", item)
	}
	waiting, err := a.ListMainChatWaitingItems("", 50)
	if err != nil || len(waiting.Items) != 1 || !reflect.DeepEqual(waiting.Items[0].TopicIDs, item.TopicIDs) {
		t.Fatalf("waiting %+v %v", waiting, err)
	}
	page, err := a.ListMainChatTopicItems(topic, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range page.Items {
		if entry.Message != nil && entry.Message.ID == id {
			found = true
			if entry.Message.RowClass != "waiting" || !reflect.DeepEqual(entry.Message.TopicIDs, item.TopicIDs) {
				t.Fatalf("topic message %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("topic message absent")
	}
	reportID := a.postToMainChat("Result", mainChatReport{Source: mainChatSourceHandoff, Evidence: []mainChatEvidenceRef{{Kind: "activity", ID: "atlas"}}})
	if tags := mainChatRowStoredTags(t, a, session, reportID); !reflect.DeepEqual(tags, []string{topic}) {
		t.Fatalf("report tags %v", tags)
	}
	statusID := a.postMainChatStatus(mainChatStatusEntry{ID: "status", Kind: "schedule_ok", Text: "Ready"}, time.Now())
	if err := a.tagMainChatRowTopics(t.Context(), statusID, mainChatRowTopicCard("unused").Evidence); err != nil {
		t.Fatal(err)
	}
	if tags := mainChatRowStoredTags(t, a, session, statusID); len(tags) != 0 {
		t.Fatalf("status tags %v", tags)
	}
}
func TestMainChatRowTopicsWithdrawalRecoveryAndClosedEyes(t *testing.T) {
	a, session, topic := mainChatRowTopicFixture(t)
	id := a.postProactiveItem(mainChatRowTopicCard("card"), "card")
	setProtected := func(value string) {
		t.Helper()
		if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status=? WHERE id='session-atlas'`, value); err != nil {
			t.Fatal(err)
		}
	}
	setProtected("protected")
	if item := mainChatRowPageItem(t, a, session, id); len(item.TopicIDs) != 0 {
		t.Fatalf("protected tag visible %+v", item)
	}
	if tags := mainChatRowStoredTags(t, a, session, id); !reflect.DeepEqual(tags, []string{topic}) {
		t.Fatal("temporary gate deleted stored association")
	}
	setProtected("done")
	if item := mainChatRowPageItem(t, a, session, id); !reflect.DeepEqual(item.TopicIDs, []string{topic}) {
		t.Fatalf("tag failed to recover %+v", item)
	}
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), session, storage.StreamItemRow{ID: "user-topic", Kind: "user", Content: "My words", TopicID: topic}); err != nil {
		t.Fatal(err)
	}
	a.closedEyes.enabled.Store(true)
	if item := mainChatRowPageItem(t, a, session, id); len(item.TopicIDs) != 0 {
		t.Fatal("Closed Eyes leaked derived tag")
	}
	if item := mainChatRowPageItem(t, a, session, "user-topic"); !reflect.DeepEqual(item.TopicIDs, []string{topic}) {
		t.Fatal("Closed Eyes removed explicit user tag")
	}
	hidden := a.postProactiveItem(mainChatRowTopicCard("closed"), "closed")
	if tags := mainChatRowStoredTags(t, a, session, hidden); len(tags) != 0 {
		t.Fatal("Closed Eyes wrote derived tag")
	}
	a.closedEyes.enabled.Store(false)
	if item := mainChatRowPageItem(t, a, session, id); len(item.TopicIDs) != 1 {
		t.Fatal("Closed Eyes recovery failed")
	}
}
func TestMainChatRowTopicsUseA3SourceGates(t *testing.T) {
	for _, gate := range []string{"protected", "sensitive", "paused", "missing", "private", "mail", "slack"} {
		t.Run(gate, func(t *testing.T) {
			a, session, _ := mainChatRowTopicFixture(t)
			switch gate {
			case "protected":
				_, _ = a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`)
			case "sensitive":
				pii.SetUserSensitiveApps(map[string]string{"Code": "Code"})
			case "paused":
				now := time.Now()
				a.recordObservationPause(true, now.Add(-2*time.Hour))
				a.recordObservationPause(false, now)
			case "missing":
				if err := a.activity.store.DeleteSession("session-atlas"); err != nil {
					t.Fatal(err)
				}
			case "private":
				_, _ = a.activity.store.db.Exec(`UPDATE activities SET title='Private Browsing' WHERE id='atlas'`)
			case "mail":
				_, _ = a.activity.store.db.Exec(`UPDATE activities SET active_ms_by_app='{"Mail":1000}' WHERE id='atlas'`)
			case "slack":
				_, _ = a.activity.store.db.Exec(`UPDATE activities SET active_ms_by_app='{"Slack":1000}' WHERE id='atlas'`)
			}
			id := a.postProactiveItem(mainChatRowTopicCard("denied"), "denied")
			if tags := mainChatRowStoredTags(t, a, session, id); len(tags) != 0 {
				t.Fatalf("%s wrote tags %v", gate, tags)
			}
		})
	}
}

type mainChatMultiTopicSource struct {
	topics map[string]ChatTopic
	keys   ChatTopicKeys
}

func (s *mainChatMultiTopicSource) ListWorkTopicIDs() []string {
	ids := []string{}
	for id := range s.topics {
		ids = append(ids, id)
	}
	return ids
}
func (s *mainChatMultiTopicSource) TopicByID(ctx context.Context, id string) (ChatTopic, error) {
	return s.topics[id], ctx.Err()
}
func (s *mainChatMultiTopicSource) TopicKeys(ctx context.Context, id string) (ChatTopicKeys, error) {
	return s.keys, ctx.Err()
}
func TestMainChatRowTopicsAmbiguityAndBackfill(t *testing.T) {
	a, session, _ := mainChatRowTopicFixture(t)
	source := &mainChatMultiTopicSource{topics: map[string]ChatTopic{}, keys: ChatTopicKeys{ThreadKeys: []string{"obj:atlas"}}}
	refs := []TodayChatContextRef{{SourceKind: "activity", SourceID: "atlas"}}
	hash, err := a.chatTopicSourceFingerprint(t.Context(), refs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("topic-%d", i)
		source.topics[id] = ChatTopic{ID: id, Refs: refs, SourceFingerprint: hash}
	}
	a.topicSource = source
	ambiguous := a.postProactiveItem(mainChatRowTopicCard("ambiguous"), "ambiguous")
	if tags := mainChatRowStoredTags(t, a, session, ambiguous); len(tags) != 0 {
		t.Fatalf("three matches wrote %v", tags)
	}
	delete(source.topics, "topic-2")
	two := a.postProactiveItem(mainChatRowTopicCard("two"), "two")
	if tags := mainChatRowStoredTags(t, a, session, two); len(tags) != 2 {
		t.Fatalf("two matches %v", tags)
	}
	now := time.Now()
	for _, age := range []int{13, 15} {
		row := mainChatViewCard(t, fmt.Sprintf("old-%d", age), "goal_check_in", "pending", now.AddDate(0, 0, -age))
		row.ToolInput = mainChatViewPayload(t, mainChatRowTopicCard(row.ID))
		if _, err = a.store.AppendStreamItem(t.Context(), session, row); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.backfillMainChatRowTopics(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if tags := mainChatRowStoredTags(t, a, session, "old-13"); len(tags) != 2 {
		t.Fatalf("backfill %v", tags)
	}
	if tags := mainChatRowStoredTags(t, a, session, "old-15"); len(tags) != 0 {
		t.Fatal("backfill exceeded 14 days")
	}
	if err = a.backfillMainChatRowTopics(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if tags := mainChatRowStoredTags(t, a, session, "old-13"); len(tags) != 2 {
		t.Fatal("backfill duplicated tags")
	}
	if done, err := a.store.GetUIState(t.Context(), mainChatTopicBackfillKey); err != nil || done != "true" {
		t.Fatalf("marker %s %v", done, err)
	}
	row := mainChatViewCard(t, "after-marker", "goal_check_in", "pending", now)
	row.ToolInput = mainChatViewPayload(t, mainChatRowTopicCard(row.ID))
	if _, err = a.store.AppendStreamItem(t.Context(), session, row); err != nil {
		t.Fatal(err)
	}
	if err = a.backfillMainChatRowTopics(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if len(mainChatRowStoredTags(t, a, session, row.ID)) != 0 {
		t.Fatal("backfill ran twice")
	}
}
func TestMainChatRowTopicsPublicationRaceAndCancellation(t *testing.T) {
	a, session, _ := mainChatRowTopicFixture(t)
	a.mainChatRowTopicsBeforePublish = func() {
		_, _ = a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`)
	}
	id := a.postProactiveItem(mainChatRowTopicCard("raced"), "raced")
	if len(mainChatRowStoredTags(t, a, session, id)) != 0 {
		t.Fatal("withdrawal raced tag publication")
	}
	a.mainChatRowTopicsBeforePublish = nil
	_, _ = a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='done' WHERE id='session-atlas'`)
	if err := a.tagMainChatRowTopics(t.Context(), id, mainChatRowTopicCard("raced").Evidence); err != nil {
		t.Fatal(err)
	}
	a.mainChatRowTopicsBeforeReadPublish = func() {
		_, _ = a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`)
	}
	page, err := a.LoadStreamItemsPage(session, -1, 50)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("source withdrawal failed direct chat read: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.ID == id && len(item.TopicIDs) != 0 {
			t.Fatal("stale tag survived read fallback")
		}
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.backfillMainChatRowTopics(canceled, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel %v", err)
	}
	if marker, _ := a.store.GetUIState(t.Context(), mainChatTopicBackfillKey); marker != "" {
		t.Fatal("cancel marked done")
	}
}

func TestMainChatRowTopicsGoalAndPrincipalAliases(t *testing.T) {
	t.Run("goal", func(t *testing.T) {
		bridge, store, now := mobileTestGoalBridge(t)
		t.Setenv(topicsStateDirEnvVar, t.TempDir())
		a := bridge.app
		configureTopicRegistryTest(a, now)
		a.topicSource = &registryTopicSource{app: a}
		goal := mobileTestPurpose(t, store, "card-goal", PurposeKindGoal, "Ship Atlas", false, nil)
		views, err := collectAndReadTopicViewsForTest(a)
		if err != nil || len(views) != 1 {
			t.Fatalf("topics %+v %v", views, err)
		}
		svc := a.topicsSvc()
		svc.startRegistryWorker(t.Context())
		t.Cleanup(svc.stopRegistryWorker)
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		registry, err := svc.registryViews(canceled)
		if err != nil || len(registry.views) != 1 {
			t.Fatalf("snapshot registry %+v %v", registry, err)
		}
		keys, ok := registry.keys[views[0].ID]
		if !ok || !reflect.DeepEqual(keys.GoalIDs, []string{goal.OpenLoop.ID}) {
			t.Fatalf("snapshot goal keys %+v", keys)
		}
		main, err := a.ensureMainChat(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		id := a.postProactiveItem(mainChatProactive{MomentID: "goal-card", Kind: "goal_check_in", State: "pending", Target: mainChatTarget{Kind: "goal", ID: goal.OpenLoop.ID}}, "goal-card")
		if tags := mainChatRowStoredTags(t, a, main.LocalID, id); !reflect.DeepEqual(tags, []string{views[0].ID}) {
			t.Fatalf("goal tags %v", tags)
		}
	})
	t.Run("principal alias", func(t *testing.T) {
		a, session, _ := mainChatRowTopicFixture(t)
		person, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{ID: "alex", Kind: storage.PrincipalKindContact, DisplayName: "Alex Chen", Aliases: []string{"A Chen"}, CreatedFrom: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		configureTopicRegistryTest(a, time.Now())
		refs := []TodayChatContextRef{{SourceKind: "principal", SourceID: person.ID}}
		hash, err := a.chatTopicSourceFingerprint(t.Context(), refs)
		if err != nil {
			t.Fatal(err)
		}
		a.topicSource = &mainChatMultiTopicSource{topics: map[string]ChatTopic{"person-topic": {ID: "person-topic", Refs: refs, SourceFingerprint: hash}}, keys: ChatTopicKeys{ThreadKeys: []string{"person:a chen"}}}
		id := a.postProactiveItem(mainChatProactive{MomentID: "person-card", Kind: "person_reappeared", State: "pending", Evidence: []mainChatEvidenceRef{{Kind: "principal", ID: person.ID}}}, "person-card")
		if tags := mainChatRowStoredTags(t, a, session, id); !reflect.DeepEqual(tags, []string{"person-topic"}) {
			t.Fatalf("person tags %v", tags)
		}
	})
}
func TestMainChatRowTopicsHiddenMessageScanKeepsOlderExchanges(t *testing.T) {
	a, session, now := registryChatTestAppAt(t, time.Now().Add(-time.Minute))
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("registry %+v %v", views, err)
	}
	topic := views[0].ID
	// Old explicit exchanges remain reachable through any number of hidden
	// derived rows; a short/empty page must carry an advancing continuation.
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), session, storage.StreamItemRow{ID: "old-user", Kind: "user", Content: "My words", TopicID: topic}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		id := a.postProactiveItem(mainChatRowTopicCard(fmt.Sprintf("hidden-%d", i)), fmt.Sprintf("hidden-%d", i))
		if len(mainChatRowStoredTags(t, a, session, id)) != 1 {
			t.Fatal("seed untagged")
		}
	}
	a.saveObservationPauses([]observationPause{{Start: now.Add(time.Second).UnixMilli()}})
	a.closedEyes.enabled.Store(true)
	cursor := ""
	found := false
	for i := 0; i < 8; i++ {
		page, err := a.ListMainChatTopicItems(topic, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Items {
			if entry.Message != nil && entry.Message.ID == "old-user" {
				found = true
			}
			if entry.Message != nil && entry.Message.Kind == "system" {
				t.Fatal("hidden system row associated with topic")
			}
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = page.NextCursor
	}
	if !found {
		t.Fatal("hidden rows stranded the older exchange")
	}
}

func TestMainChatRowTopicsThreadMemberUsesReadableSureKeys(t *testing.T) {
	a, session, topic := mainChatRowTopicFixture(t)
	now := time.Now()
	seedThreadMoment(t, a, threadFixtureMoment{id: "neighbor", title: "Review this patch", app: "Code", start: now.Add(-20 * time.Minute), end: now.Add(-10 * time.Minute)})
	for _, query := range []string{
		`INSERT INTO activity_threads(id,day_start_ms,created_at,updated_at) VALUES('shared',0,1,1)`,
		`INSERT INTO activity_thread_members(activity_id,thread_id,day_start_ms,source,updated_at) VALUES('atlas','shared',0,'rule',1),('neighbor','shared',0,'rule',1)`,
	} {
		if _, err := a.activity.store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	card := mainChatRowTopicCard("thread-card")
	card.Evidence[0].ID = "neighbor"
	id := a.postProactiveItem(card, "thread-card")
	if tags := mainChatRowStoredTags(t, a, session, id); !reflect.DeepEqual(tags, []string{topic}) {
		t.Fatalf("thread keys %v", tags)
	}
	// The matching member cannot lend keys to an unreadable card ref.
	if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-neighbor'`); err != nil {
		t.Fatal(err)
	}
	if item := mainChatRowPageItem(t, a, session, id); len(item.TopicIDs) != 0 {
		t.Fatal("readable sibling authorized protected evidence")
	}
}
