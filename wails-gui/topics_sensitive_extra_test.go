package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestTopicSensitiveStartupBeforeActivityKeepsProjectsAndChoices(t *testing.T) {
	for _, denyAtlas := range []bool{false, true} {
		name, policy := "independent_custom_app", `{"com.example.unrelated":"Other"}`
		if denyAtlas {
			name, policy = "denied_app_scrubbed_after_ready", `{"com.example.code":"Code"}`
		}
		t.Run(name, func(t *testing.T) {
			a, _, now := registryChatTestAppAt(t, topicTestNow)
			seedThreadMoment(t, a, threadFixtureMoment{id: "beta", title: "Beta release", app: "Preview", start: now.Add(-time.Hour), end: now.Add(-time.Minute), entities: []Entity{{Type: entityProject, Name: "Beta"}}})
			storeTestInterests(t, a, testInterest("i-first", "First interest", "Following one"), testInterest("i-pinned", "Pinned interest", "Following two"))
			views, err := collectAndReadTopicViewsForTest(a)
			if err != nil || len(views) != 4 {
				t.Fatalf("initial topics: %+v %v", views, err)
			}
			var atlas, beta string
			for _, view := range views {
				switch view.Name {
				case "Atlas":
					atlas = view.ID
				case "Beta":
					beta = view.ID
				}
			}
			if atlas == "" || beta == "" {
				t.Fatalf("missing work controls: %+v", views)
			}
			if err := a.PinTopicToSidebar(beta, true); err != nil {
				t.Fatal(err)
			}
			if _, err := a.topicsSvc().update(UserTopicUpdate{ID: "i-pinned", Action: "pin"}); err != nil {
				t.Fatal(err)
			}
			beforeOrder, err := a.GetUserTopics()
			if err != nil || len(beforeOrder.Topics) != 2 || beforeOrder.Topics[0].ID != "i-pinned" {
				t.Fatalf("Your topics control: %+v %v", beforeOrder, err)
			}
			before := a.topicsSvc().store.load()
			if len(before.Projects) != 2 || len(before.ChatTopics) == 0 {
				t.Fatalf("missing durable controls: %+v", before)
			}
			if err := a.store.SetUIState(t.Context(), "sensitive_apps", policy); err != nil {
				t.Fatal(err)
			}
			// A new service reloads topics.json while Activity has not opened yet.
			activity := a.activity
			a.activity = nil
			t.Cleanup(func() { a.activity = activity })
			a.topics = newTopicsService(a)
			configureTopicRegistryTest(a, now)
			pii.SetUserSensitiveApps(nil)
			a.loadUserSensitiveApps()
			early := a.topicsSvc().store.load()
			wantEarly := before
			wantEarly.ChatTopics = nil
			wantEarly.Today = nil
			if !reflect.DeepEqual(early, wantEarly) {
				t.Fatalf("early startup changed project identities/choices or retained chat: before=%+v after=%+v", wantEarly, early)
			}
			// Same second phase as startup, once historical Activity is ready.
			a.activity = activity
			if err := a.scrubSensitiveTopics(); err != nil {
				t.Fatal(err)
			}
			after := a.topicsSvc().store.load()
			if !denyAtlas && !reflect.DeepEqual(after, wantEarly) {
				t.Fatalf("readable projects changed after Activity became ready: before=%+v after=%+v", wantEarly, after)
			}
			if denyAtlas {
				for _, project := range after.Projects {
					if project.ID == atlas && (project.Name != "" || len(project.MemberKeys) != 0) {
						t.Fatalf("ready phase retained denied work: %+v", project)
					}
				}
			}
			if !reflect.DeepEqual(after.SidebarPins, before.SidebarPins) || !reflect.DeepEqual(after.Overrides, before.Overrides) {
				t.Fatalf("ready phase changed user choices: %+v", after)
			}
			afterOrder, err := a.GetUserTopics()
			if err != nil || !reflect.DeepEqual(afterOrder.Topics, beforeOrder.Topics) {
				t.Fatalf("Your topics order changed: before=%+v after=%+v err=%v", beforeOrder.Topics, afterOrder.Topics, err)
			}
			sidebar, err := a.ListSidebarTopics()
			if err != nil || len(sidebar) != 1 || sidebar[0].ID != beta || !sidebar[0].PinnedToSidebar {
				t.Fatalf("readable project/pin lost after restart: %+v %v", sidebar, err)
			}
		})
	}
}

func TestTopicSensitiveFailedScrubClearsChatTopics(t *testing.T) {
	for _, failure := range []string{"lock_timeout", "database_error"} {
		t.Run(failure, func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			views, err := collectAndReadTopicViewsForTest(a)
			if err != nil || len(views) != 1 {
				t.Fatalf("initial topic: %+v %v", views, err)
			}
			if err := a.PinTopicToSidebar(views[0].ID, true); err != nil {
				t.Fatal(err)
			}
			before := a.topicsSvc().store.load()
			if len(before.ChatTopics) == 0 || len(before.Projects) != 1 {
				t.Fatalf("missing failure controls: %+v", before)
			}
			switch failure {
			case "lock_timeout":
				// An already-expired budget and held memory barrier make the
				// lock failure deterministic; cleanup must not need that lock.
				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				defer cancel()
				memoryMutationMu.Lock()
				func() {
					defer memoryMutationMu.Unlock()
					err = a.scrubSensitiveTopicsContext(ctx)
				}()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected lock deadline, got %v", err)
				}
			case "database_error":
				if err := a.activity.store.Close(); err != nil {
					t.Fatal(err)
				}
				err = a.scrubSensitiveTopics()
				if err == nil || !strings.Contains(err.Error(), "closed") {
					t.Fatalf("expected closed Activity database, got %v", err)
				}
			}
			want := before
			want.ChatTopics = nil
			want.Today = nil
			if after := a.topicsSvc().store.load(); !reflect.DeepEqual(after, want) {
				t.Fatalf("failed scrub retained chat or damaged projects/choices: before=%+v after=%+v", want, after)
			}
		})
	}
}
