package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

// A real SQLite fixture; no production stores, app launch, or model calls.
// Durations are diagnostic rather than load-dependent pass/fail assertions.
func TestMainChatTopicPageReadTiming(t *testing.T) {
	a, session, now := registryChatTestApp(t)
	for i := 1; i < 30; i++ {
		name := fmt.Sprintf("Project%02d", i)
		seedThreadMoment(t, a, threadFixtureMoment{id: fmt.Sprintf("fixture-%d", i), title: name + " ready", app: "Code", start: now.Add(-time.Hour), end: now.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: name}}})
	}
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	rows := make([]storage.StreamItemRow, 2000)
	kinds := []string{"user", "assistant", "thinking", "tool_use", "tool_result", "system"}
	for i := range rows {
		rows[i] = storage.StreamItemRow{ID: fmt.Sprintf("fixture-row-%d", i), Kind: kinds[i%len(kinds)], Content: strings.Repeat("Public fixture content for the page-read workload. ", 20), CreatedAt: now.UnixMilli() + int64(i)}
	}
	if err := a.store.SaveStreamItems(t.Context(), session, rows); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		started := time.Now()
		topics, err := a.GetTodayTopics()
		topicsTime := time.Since(started)
		if err != nil || len(topics.Topics) != topicRegistryLimit {
			t.Fatalf("topics=%d err=%v", len(topics.Topics), err)
		}
		started = time.Now()
		page, err := a.LoadStreamItemsPage(session, -1, 100)
		streamTime := time.Since(started)
		if err != nil || len(page.Items) != 100 {
			t.Fatalf("items=%d err=%v", len(page.Items), err)
		}
		started = time.Now()
		_, err = a.GetMainChatOverview()
		overviewTime := time.Since(started)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("attempt=%d GetTodayTopics=%s LoadStreamItemsPage=%s GetMainChatOverview=%s", attempt+1, topicsTime, streamTime, overviewTime)
	}
}
