package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"wails-gui/internal/interests"
)

// Persisted production-shaped SQLite fixture: 49 projects, 18 follows, 3,000
// recent unclassified rows, original run/session provenance and both pin kinds.
func TestTopicNameMigrationRealSizeIdentityChoicesAndColdBudget(t *testing.T) {
	a := populatedTodayTopicsFixture(t)
	a.topicSource = &registryTopicSource{app: a}
	svc, now := a.topicsSvc(), a.topicsSvc().now()
	var extra []ActivityRow
	for _, tc := range []struct{ id, repo, title string }{
		{"thinking", "acme/thinking_cap", "Thinking Cap: recordings"},
		{"foo", "acme/foo", "Acme foo: connector"},
	} {
		extra = append(extra, seedThreadMoment(t, a, threadFixtureMoment{id: tc.id, title: tc.title, app: "Code", start: now.Add(-48*time.Hour - time.Minute), end: now.Add(-48 * time.Hour), entities: []Entity{{Type: entityRepo, Name: tc.repo}}}))
	}
	list := a.interestsSvc().store.load().Interests
	for _, tc := range []struct{ id, name, source string }{
		{"follow-thinking", "acme/thinking_cap", "thinking"},
		{"follow-foo", "acme/foo", "foo"},
		{"follow-foo-collision", "acme-foo", "foo"},
	} {
		item := testInterest(tc.id, tc.name, "Current project")
		item.Evidence = []interests.Ref{{Source: interests.SourceActivity, Key: "activity:" + tc.source, At: now.Add(-48 * time.Hour).UnixMilli()}}
		list = append(list, item)
	}
	storeTestInterests(t, a, list...)
	newProjects := buildTopicRegistry(topicRegistryData{activities: extra, now: now}).projects
	if _, err := svc.store.update(func(state *topicsStateFile) {
		state.Projects = append(state.Projects, newProjects...)
		state.SidebarPins = map[string]bool{"follow-thinking": true, "follow-foo": true, "follow-foo-collision": true}
		state.Overrides = map[string]topicOverride{
			"follow-thinking": {Key: "acme/thinking_cap", Label: "My recordings", Renamed: true, Pinned: true, At: topicRegistryTime(now.UnixMilli())},
			// Explicit choices retain the review's deliberately generic "foo"
			// names through the normal interest-name quality filter.
			"follow-foo":           {Key: "acme/foo", Label: "acme/foo", Pinned: true, At: topicRegistryTime(now.UnixMilli())},
			"follow-foo-collision": {Key: "acme-foo", Label: "acme-foo", Pinned: true, At: topicRegistryTime(now.UnixMilli())},
			"interest-01":          {Key: "Optical camera modules 01", Label: "Optical camera modules 01", Removed: true, At: topicRegistryTime(now.UnixMilli())},
		}
		state.Weights = map[string]float64{"acme/thinking_cap": 1.35}
		for i := range state.Projects {
			p := &state.Projects[i]
			p.Aliases = []string{"legacy-" + p.ID}
			if i%3 == 0 {
				state.SidebarPins[p.Aliases[0]] = true
			}
			if i%3 == 1 {
				state.SidebarPins[p.ID] = true
			}
			if len(p.ActivityIDs) == 1 {
				switch p.ActivityIDs[0] {
				case "thinking":
					p.Name = "acme/thinking_cap"
				case "foo":
					p.Name = "acme/foo"
				default:
					p.Name = fmt.Sprintf("~/src/Atlas%02d_release", i)
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	before := svc.store.load()
	if len(before.Projects) != 49 || len(list) != 18 {
		t.Fatalf("fixture size: projects=%d follows=%d", len(before.Projects), len(list))
	}
	identities := map[string][]string{}
	bySource := map[string]string{}
	for _, p := range before.Projects {
		identities[p.ID] = p.Aliases
		for _, source := range p.ActivityIDs {
			bySource[source] = p.ID
		}
	}
	checkChoices := func() {
		t.Helper()
		state := svc.store.load()
		got := map[string][]string{}
		for _, p := range state.Projects {
			got[p.ID] = p.Aliases
		}
		if !reflect.DeepEqual(got, identities) || !reflect.DeepEqual(state.SidebarPins, before.SidebarPins) || !reflect.DeepEqual(state.Overrides, before.Overrides) || !reflect.DeepEqual(state.Weights, before.Weights) || !reflect.DeepEqual(a.interestsSvc().store.load().Interests, list) {
			t.Fatal("migration changed saved identities, aliases, pins or follow choices")
		}
	}
	checkMatches := func(result topicRegistryResult) {
		t.Helper()
		for _, tc := range []struct{ follow, target, name string }{
			{"follow-thinking", bySource["thinking"], "Thinking Cap"},
			{"follow-foo", bySource["foo"], "Acme foo"},
			{"follow-foo-collision", "follow-foo-collision", "Acme foo"},
		} {
			id := result.canonical[tc.follow]
			if id == "" {
				id = tc.follow
			}
			if id != tc.target || result.keys[tc.follow].InterestID != tc.follow {
				t.Fatalf("follow id lost: %s %+v", tc.follow, result.keys[tc.follow])
			}
			found := false
			for _, view := range result.views {
				if view.ID == id {
					found = view.Name == tc.name && view.PinnedToSidebar
				}
			}
			if !found {
				t.Fatalf("follow %s lost its human name or inherited pin", tc.follow)
			}
			if _, _, err := a.chatTopicSnapshot(t.Context(), tc.follow); err != nil {
				t.Fatalf("follow alias cannot open: %s %v", tc.follow, err)
			}
		}
	}
	for read := 0; read < 2; read++ {
		result, err := svc.registryViews(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		checkMatches(result)
		checkChoices()
	}
	// Withdraw one original source and downgrade another's readability. Saved
	// ids/aliases/pins remain reservations; neither can replay its old name.
	w, err := a.topicWithdrawalForSource("activity", []string{"work-03"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.withTopicWithdrawal(w, func() error {
		_, err := a.activity.store.db.Exec(`DELETE FROM activities WHERE id='work-03'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-work-02'`); err != nil {
		t.Fatal(err)
	}
	for read := 0; read < 2; read++ {
		result, err := svc.registryViews(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		checkMatches(result)
		checkChoices()
		for _, view := range result.views {
			if view.Name == "Atlas02 release" || view.Name == "Atlas03 release" || view.ID == "interest-01" || view.ID == "interest-02" || view.ID == "interest-03" {
				t.Fatalf("withdrawn, unreadable or Removed content returned: %+v", view)
			}
		}
	}
	// Reopen the persisted store and explicitly build its snapshot before
	// measuring settled reads.
	a.topics = newTopicsService(a)
	svc = a.topics
	svc.now = func() time.Time { return now }
	if _, err := a.activity.store.db.Exec(`UPDATE activities SET updated_at=updated_at+1 WHERE id='unnamed-0'`); err != nil {
		t.Fatal(err)
	}
	if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) == 0 {
		t.Fatalf("explicit snapshot build: %d views, %v", len(views), err)
	}
	unblock := holdTodayTopicsBuild(t, a)
	coldState := svc.store.load()
	exhausted, err := a.GetTodayTopics()
	if err != nil || len(exhausted.Topics) == 0 || !exhausted.Refreshing {
		t.Fatalf("settled snapshot was not served during refresh: %+v %v", exhausted, err)
	}
	svc.requestRegistryRefresh()
	best := time.Duration(0)
	for read := 0; read < 3; read++ {
		started := time.Now()
		response, err := a.GetTodayTopics()
		elapsed := time.Since(started)
		if best == 0 || elapsed < best {
			best = elapsed
		}
		t.Logf("real-size migration cold read %d: %s topics=%d", read, elapsed, len(response.Topics))
		if err != nil || !response.Refreshing || len(response.Topics) == 0 {
			t.Fatalf("cold read: elapsed=%s response=%+v err=%v", elapsed, response, err)
		}
		for _, view := range response.Topics {
			if view.Kind == TopicKindProject {
				if _, saved := identities[view.ID]; !saved {
					t.Fatalf("cold path minted a replacement id: %+v", view)
				}
			}
		}
		checkChoices()
	}
	if best >= topicTodayReadBudget {
		t.Fatalf("cold budget: best=%s budget=%s", best, topicTodayReadBudget)
	}
	cold, _, _ := svc.snapshotRead()
	coldByID := map[string]TopicView{}
	for _, view := range cold {
		coldByID[view.ID] = view
		if view.ID == bySource["work-02"] || view.ID == bySource["work-03"] || view.ID == "interest-01" || view.ID == "interest-02" || view.ID == "interest-03" {
			t.Fatalf("cold projection replayed withdrawn, unreadable or Removed content: %+v", view)
		}
	}
	for _, saved := range coldState.Projects {
		if saved.Name != "" && coldByID[saved.ID].Name != saved.Name {
			t.Fatalf("cold projection changed or lost a saved readable identity: %+v", saved)
		}
	}
	if !reflect.DeepEqual(coldState, svc.store.load()) {
		t.Fatal("cold migration committed partial identity or follow choices")
	}
	for _, follow := range []string{"follow-thinking", "follow-foo-collision"} {
		found := false
		for _, view := range cold {
			found = found || view.PinnedToSidebar && (view.Name == "Thinking Cap" && follow == "follow-thinking" || view.Name == "Acme foo" && follow == "follow-foo-collision")
		}
		if !found {
			t.Fatalf("cold projection lost inherited follow pin %s: %+v", follow, cold)
		}
	}
	// A refresh keeps the last published snapshot and its verified keys usable.
	if partial, err := svc.registryViews(t.Context()); err != nil || len(partial.views) == 0 {
		t.Fatalf("published read during refresh: %+v, %v", partial, err)
	} else if _, ok := partial.keys[bySource["work-00"]]; !ok {
		t.Fatalf("published read lost verified chat keys: %+v", partial.keys)
	}
	unblock()
	// Release is not publication. Wait for the existing worker's authoritative
	// chat completion before exercising every saved follow alias and pin.
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for cache := svc.loadRegistryCache(); cache == nil || !cache.chatComplete; cache = svc.loadRegistryCache() {
		select {
		case <-tick.C:
		case <-timer.C:
			t.Fatal("registry worker did not publish authoritative chat after release")
		}
	}
	if result, err := svc.registryViews(t.Context()); err != nil {
		t.Fatal(err)
	} else {
		checkMatches(result)
		checkChoices()
	}
}

func TestTopicFollowingIdentityDoesNotCollapseDisplayCollisions(t *testing.T) {
	rows := []ActivityRow{
		topicRegistryTestActivity("hyphen", "Acme foo", topicRegistryTestNow, Entity{Type: entityRepo, Name: "acme-foo"}),
		topicRegistryTestActivity("slash", "Acme foo", topicRegistryTestNow, Entity{Type: entityRepo, Name: "acme/foo"}),
	}
	following := UserTopicsView{Interests: true, Topics: []UserTopic{
		{ID: "slash-follow", Label: "acme/foo", Kind: string(interests.KindProject), Why: "Current work"},
		{ID: "hyphen-follow", Label: "acme-foo", Kind: string(interests.KindProject), Why: "Current work"},
		{ID: "source-follow", Label: "ACME/FOO", Kind: string(interests.KindProject), Why: "Current work"},
	}}
	result := buildTopicRegistry(topicRegistryData{activities: rows, following: following, now: topicRegistryTestNow,
		sidebarPins: map[string]bool{"hyphen-follow": true, "source-follow": true}})
	if len(result.views) != 2 || result.canonical["slash-follow"] == result.canonical["hyphen-follow"] || result.canonical["source-follow"] != result.canonical["slash-follow"] {
		t.Fatalf("display collision changed source/member identity: %+v", result.canonical)
	}
	for _, view := range result.views {
		if view.Name != "Acme foo" || !view.PinnedToSidebar {
			t.Fatalf("colliding display lost a pinned row: %+v", view)
		}
	}
	for _, follow := range following.Topics {
		if result.keys[follow.ID].InterestID != follow.ID || result.canonical[follow.ID] == "" {
			t.Fatalf("follow cannot resolve after collision: %s", follow.ID)
		}
	}
}
