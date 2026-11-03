package main

import (
	"fmt"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestTodayColdHistoryPreservesSavedProjectIdentity(t *testing.T) {
	for _, merged := range []bool{false, true} {
		t.Run(fmt.Sprintf("merged=%t", merged), func(t *testing.T) {
			a, _, now := registryChatTestApp(t)
			// Start with Beta alone, so its saved identity predates Alpha.
			if _, err := a.activity.store.db.Exec(`UPDATE activities SET entities='[{"type":"project","name":"Beta"}]',title='Beta' WHERE id='atlas'`); err != nil {
				t.Fatal(err)
			}
			first, err := collectAndReadTopicViewsForTest(a)
			if err != nil || len(first) != 1 {
				t.Fatalf("initial: %+v %v", first, err)
			}
			id := first[0].ID
			if err := a.PinTopicToSidebar(id, true); err != nil {
				t.Fatal(err)
			}
			alias := ""
			if merged {
				seedThreadMoment(t, a, threadFixtureMoment{id: "alpha", title: "Alpha", app: "Code", start: now.Add(-50 * time.Minute), end: now.Add(-45 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Alpha"}}})
				separate, err := collectAndReadTopicViewsForTest(a)
				if err != nil || len(separate) != 2 {
					t.Fatalf("separate: %+v %v", separate, err)
				}
				for _, view := range separate {
					if view.ID != id {
						alias = view.ID
					}
				}
			}
			seedThreadMoment(t, a, threadFixtureMoment{id: "bridge", title: "Alpha Beta", app: "Code", start: now.Add(-40 * time.Minute), end: now.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Alpha"}, {Type: entityProject, Name: "Beta"}}})
			full, err := collectAndReadTopicViewsForTest(a)
			if err != nil || len(full) != 1 || full[0].ID != id || !full[0].PinnedToSidebar {
				t.Fatalf("full identity changed: %+v %v", full, err)
			}
			for i := 0; i < 8; i++ {
				at := now.Add(-time.Duration(i+1) * time.Minute)
				seedThreadMoment(t, a, threadFixtureMoment{id: fmt.Sprintf("unnamed-%d", i), title: "Reading", app: "Code", start: at.Add(-time.Second), end: at})
			}
			if alias != "" {
				// A pin on a merged alias still belongs to the canonical card.
				if _, err := a.topicsSvc().store.update(func(state *topicsStateFile) {
					state.SidebarPins = map[string]bool{alias: true}
				}); err != nil {
					t.Fatal(err)
				}
			}
			// A relaunch has no in-memory cache, and changed database metadata
			// prevents using the persisted display snapshot. Keep publication held.
			a.topicsSvc().registryCacheMu.Lock()
			a.topicsSvc().registryCache = nil
			a.topicsSvc().registryCacheMu.Unlock()
			unblock := holdTodayTopicsBuild(t, a)
			var cold TodayTopicsView
			elapsed := coldReadBestOfThree(t, func() {
				var err error
				cold, err = a.GetTodayTopics()
				if err != nil || !cold.Refreshing || len(cold.Topics) != 1 {
					t.Fatalf("cold: %+v %v", cold, err)
				}
				if cold.Topics[0].ID != id || !cold.Topics[0].PinnedToSidebar || cold.Topics[0].Name != full[0].Name {
					t.Fatalf("cold identity %+v differs from full %+v", cold.Topics, full)
				}
			})
			if elapsed >= 300*time.Millisecond {
				t.Fatalf("cold saved identity exceeded budget: best=%s", elapsed)
			}
			t.Logf("cold saved identity: id=%s pinned=true best=%s", id, elapsed)
			phone := mobileTopicTestList(t, &mobileBridge{app: a, ctx: t.Context()})
			if len(phone) != 1 || phone[0].ID != id || phone[0].Name != full[0].Name {
				t.Fatalf("phone changed cold identity: %+v", phone)
			}
			// Opening the cold card must resolve the saved admission proof even
			// before the background build can publish. Aliases remain usable too.
			ids := []string{id}
			if alias != "" {
				ids = append(ids, alias)
			}
			for _, requested := range ids {
				topic, keys, err := a.chatTopicSnapshot(t.Context(), requested)
				if err != nil || topic.ID != requested || len(keys.ThreadKeys) != 2 {
					t.Fatalf("cold Open %s: %+v %+v %v", requested, topic, keys, err)
				}
			}
			unblock()
			// Pin uses the complete registry's keys; it must accept the exact
			// ID advertised by the cold card rather than a newly minted ID.
			for _, requested := range ids {
				if err := a.PinTopicToSidebar(requested, true); err != nil {
					t.Fatalf("cold Pin %s: %v", requested, err)
				}
			}
			result, err := a.topicsSvc().registryViews(t.Context())
			if err != nil || len(result.views) != 1 || result.views[0].ID != cold.Topics[0].ID || !result.views[0].PinnedToSidebar {
				t.Fatalf("published identity: %+v %v", result.views, err)
			}
		})
	}
}

func TestTodaySnapshotReadsRemainStableUntilBuild(t *testing.T) {
	previousSensitiveApps := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previousSensitiveApps) })
	for _, change := range []string{"readable", "missing", "protected", "goal", "unsaved", "edited"} {
		t.Run(change, func(t *testing.T) {
			a, _, now := registryChatTestApp(t)
			full, err := collectAndReadTopicViewsForTest(a)
			if err != nil || len(full) != 1 {
				t.Fatalf("initial: %+v %v", full, err)
			}
			state := a.topicsSvc().store.load()
			switch change {
			case "missing":
				state.Projects[0].ActivityIDs = append(state.Projects[0].ActivityIDs, "missing")
			case "protected":
				if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`); err != nil {
					t.Fatal(err)
				}
			case "goal":
				state.Projects[0].GoalIDs = []string{"unverified-goal"}
			case "unsaved":
				state.Projects = nil
			case "edited":
				if _, err := a.activity.store.db.Exec(`UPDATE activities SET entities='[{"type":"project","name":"Changed"}]',title='Changed' WHERE id='atlas'`); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 8; i++ {
				at := now.Add(-time.Duration(i+1) * time.Minute)
				seedThreadMoment(t, a, threadFixtureMoment{id: fmt.Sprintf("unnamed-%d", i), title: "Reading", app: "Code", start: at.Add(-time.Second), end: at})
			}
			stale, err := a.todayTopicViews()
			if err != nil || len(stale) != 1 || stale[0].ID != full[0].ID {
				t.Fatalf("out-of-band change altered published view: %+v %v", stale, err)
			}
			views, err := collectAndReadTopicViewsForTest(a)
			if err != nil {
				t.Fatal(err)
			}
			if change == "protected" {
				snapshot := a.topicsSvc().snapshots.Current()
				if len(views) != 0 || snapshot == nil || snapshot.Topics == nil || len(snapshot.Topics) != 0 {
					t.Fatalf("protected source remained in the rebuilt snapshot: views=%+v snapshot=%+v", views, snapshot)
				}
				return
			}
			if len(views) != 1 || views[0].ID != full[0].ID {
				t.Fatalf("valid source identity was lost on rebuild: %+v", views)
			}
			if change == "edited" && views[0].Name != "Changed" {
				t.Fatalf("saved name survived a source edit: %+v", views)
			}
		})
	}
}
