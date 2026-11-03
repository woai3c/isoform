package main

import (
	"reflect"
	"testing"
	"time"

	"wails-gui/internal/interests"
)

func TestTodayAndSidebarKeepEveryMatchedFollowPin(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	seedThreadMoment(t, a, threadFixtureMoment{id: "connector", title: "Atlas release planning", app: "Code",
		start: now.Add(-time.Hour), end: now.Add(-30 * time.Minute),
		entities: []Entity{{Type: entityProject, Name: "Atlas"}, {Type: entityRepo, Name: "acme/atlas_connector"}}})
	var follows []interests.Interest
	for _, follow := range []struct{ id, name string }{{"follow-atlas", "Atlas"}, {"follow-connector", "acme/atlas_connector"}} {
		item := testInterest(follow.id, follow.name, "Current project")
		item.Evidence = []interests.Ref{{Source: interests.SourceActivity, Key: "activity:connector", At: now.Add(-30 * time.Minute).UnixMilli()}}
		follows = append(follows, item)
	}
	storeTestInterests(t, a, follows...)
	svc := a.topicsSvc()
	if _, err := svc.store.update(func(state *topicsStateFile) {
		state.Overrides = map[string]topicOverride{
			"follow-atlas":     {Key: "Atlas", Label: "Atlas", Pinned: true},
			"follow-connector": {Key: "acme/atlas_connector", Label: "acme/atlas_connector", Pinned: true},
		}
	}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.registryViews(t.Context())
	if err != nil || len(result.views) != 1 {
		t.Fatalf("full registry: %+v, %v", result.views, err)
	}
	id := result.views[0].ID
	first := result.keys[id].InterestID
	second := "follow-atlas"
	if first == second {
		second = "follow-connector"
	}
	if result.canonical[first] != id || result.canonical[second] != id || first == second {
		t.Fatalf("fixture must join both follows: %+v", result)
	}
	if _, err := svc.store.update(func(state *topicsStateFile) {
		state.SidebarPins = map[string]bool{second: true}
	}); err != nil {
		t.Fatal(err)
	}
	result, err = svc.registryViews(t.Context())
	if err != nil || len(result.views) != 1 || !result.views[0].PinnedToSidebar {
		t.Fatalf("full registry lost second follow pin: %+v, %v", result.views, err)
	}
	choices := svc.store.load().Overrides
	// Keep the production worker lifecycle active while preventing a rebuild
	// from hiding a wrong repaint of the already published warm cache.
	collects := 0
	svc.registryDuringCollect = func() { collects++ }
	unblock := holdTodayTopicsBuild(t, a)
	check := func(want bool) {
		t.Helper()
		before := collects
		wantRows := 0
		if want {
			wantRows = 1
		}
		for read := 0; read < 2; read++ {
			today, err := a.GetTodayTopics()
			if err != nil || today.Clear || len(today.Topics) != 1 || today.Topics[0].ID != id || today.Topics[0].PinnedToSidebar != want {
				t.Fatalf("warm Today pinned=%t: %+v, %v", want, today, err)
			}
			sidebar, err := a.ListSidebarTopics()
			if err != nil || len(sidebar) != wantRows || want && sidebar[0].ID != id {
				t.Fatalf("warm sidebar pinned=%t: %+v, %v", want, sidebar, err)
			}
		}
		if collects != before || !reflect.DeepEqual(choices, svc.store.load().Overrides) {
			t.Fatal("warm reads rebuilt the registry or changed follow choices")
		}
	}
	check(true)
	// Repaint current alias choices without changing source identity or
	// waiting for the blocked worker's pending publication.
	for _, pin := range []struct {
		alias  string
		pinned bool
	}{{second, false}, {first, true}, {second, true}, {first, false}, {second, false}} {
		if _, err := svc.store.update(func(state *topicsStateFile) {
			state.SidebarPins[pin.alias] = pin.pinned
		}); err != nil {
			t.Fatal(err)
		}
		pins := svc.store.load().SidebarPins
		check(pins[first] || pins[second])
	}
	unblock()
	svc.stopRegistryWorker()
	a.topics = newTopicsService(a)
	configureTopicRegistryTest(a, now)
	svc = a.topicsSvc()
	if err := a.PinTopicToSidebar(second, true); err != nil {
		t.Fatal(err)
	}
	if sidebar, err := a.ListSidebarTopics(); err != nil || len(sidebar) != 1 || sidebar[0].ID != id {
		t.Fatalf("reopened alias pin: %+v, %v", sidebar, err)
	}
	if err := a.PinTopicToSidebar(first, false); err != nil {
		t.Fatal(err)
	}
	if sidebar, err := a.ListSidebarTopics(); err != nil || len(sidebar) != 0 {
		t.Fatalf("unpin through another alias left a sidebar row: %+v, %v", sidebar, err)
	}
	if !reflect.DeepEqual(choices, svc.store.load().Overrides) {
		t.Fatal("sidebar lifecycle changed regular follow pins")
	}
}
