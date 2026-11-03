package main

import (
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestSensitiveAppProtectionEmitsTopicWithdrawal(t *testing.T) {
	for _, persistenceFails := range []bool{false, true} {
		name := "persisted"
		if persistenceFails {
			name = "persistence_failure"
		}
		t.Run(name, func(t *testing.T) {
			a, _, now := registryChatTestApp(t)
			seedThreadMoment(t, a, threadFixtureMoment{id: "beta", title: "Beta release", app: "Preview", start: now.Add(-time.Hour), end: now.Add(-time.Minute), entities: []Entity{{Type: entityProject, Name: "Beta"}}})
			if _, err := collectAndReadTopicViewsForTest(a); err != nil {
				t.Fatal(err)
			}
			before, err := a.GetTodayTopics()
			if err != nil || len(before.Topics) != 2 {
				t.Fatalf("expected readable Atlas and Beta controls: %+v %v", before, err)
			}
			a.eventBroadcaster = NewEventBroadcaster()
			events := a.eventBroadcaster.AddClient("sensitive-topics")
			t.Cleanup(func() { a.eventBroadcaster.RemoveClient("sensitive-topics") })
			if persistenceFails {
				if err := a.store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = a.AddSensitiveApp("com.example.code", "Code")
			if (err != nil) != persistenceFails {
				t.Fatalf("protection error=%v; persistence failure=%v", err, persistenceFails)
			}
			if !pii.IsSensitiveAppName("Code") {
				t.Fatal("protection did not change the in-memory policy")
			}
			expectSensitiveAppEvent(t, events)
			after, err := a.GetTodayTopics()
			if err != nil || len(after.Topics) != 1 || after.Topics[0].Name != "Beta" {
				t.Fatalf("protected Atlas stayed readable or public Beta was lost: %+v %v", after, err)
			}
			if !persistenceFails {
				stored, err := a.store.GetUIState(t.Context(), "sensitive_apps")
				var policy map[string]string
				if err != nil || json.Unmarshal([]byte(stored), &policy) != nil || policy["com.example.code"] != "Code" {
					t.Fatalf("protection event preceded persistence: %q %v", stored, err)
				}
			}
			// Removing protection also changes the readers' policy. Previously
			// protected history is still governed by the existing backend checks.
			err = a.RemoveSensitiveApp("com.example.code")
			if (err != nil) != persistenceFails {
				t.Fatalf("removal error=%v; persistence failure=%v", err, persistenceFails)
			}
			expectSensitiveAppEvent(t, events)
			if pii.IsSensitiveAppName("Code") {
				t.Fatal("removal did not change the in-memory policy")
			}
		})
	}
}

func expectSensitiveAppEvent(t *testing.T, events <-chan []byte) {
	t.Helper()
	count := 0
	for {
		select {
		case raw := <-events:
			var event BrowserEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Event == "sensitive-apps:updated" {
				count++
				if len(event.Args) != 0 {
					t.Fatalf("protection event exposed app details: %s", raw)
				}
			}
		default:
			if count != 1 {
				t.Fatalf("expected one sensitive-apps:updated event, got %d", count)
			}
			return
		}
	}
}
