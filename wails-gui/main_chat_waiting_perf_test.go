package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

type mainChatWaitingPerfTiming struct {
	Operation string        `json:"operation"`
	P50       time.Duration `json:"p50"`
	P90       time.Duration `json:"p90"`
	Max       time.Duration `json:"max"`
}

func TestMainChatWaitingPerfTimings(t *testing.T) {
	if strings.TrimSpace(os.Getenv(mainChatReadPerfFixtureEnv)) == "" {
		t.Skipf("set %s to measure the main-chat waiting state", mainChatReadPerfFixtureEnv)
	}
	ensureMainChatReadPerfFixture(t)
	a, _, closeApp := openMainChatReadPerfApp(t)
	defer closeApp()
	broadcaster := NewEventBroadcaster()
	events := broadcaster.AddClient("waiting-perf-test")
	a.eventBroadcaster = broadcaster
	a.startMainChatWaitingLifecycle(t.Context())
	defer a.stopMainChatWaitingLifecycle()
	state := waitForMainChatWaiting(t, a, func(state MainChatWaitingState) bool { return state.Ready })

	recomputes := make([]time.Duration, 30)
	for i := range recomputes {
		start := time.Now()
		if _, _, err := a.computeMainChatWaiting(); err != nil {
			t.Fatal(err)
		}
		recomputes[i] = time.Since(start)
	}
	getters := make([]time.Duration, 30)
	for i := range getters {
		start := time.Now()
		_ = a.GetMainChatWaiting()
		getters[i] = time.Since(start)
	}

	page, err := a.ListMainChatWaitingItems("", 50)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("load a card for create-event timing: items=%d err=%v", len(page.Items), err)
	}
	newest := page.Items[len(page.Items)-1]
	rowID := fmt.Sprintf("perf-waiting-event-%d", time.Now().UnixNano())
	started := time.Now()
	storedID := a.appendMainChatItem(context.Background(), state.SessionID, storage.StreamItemRow{
		ID: rowID, Kind: "system", Content: newest.Content,
		ToolName: mainChatProactiveTool, ToolInput: newest.ToolInput, CreatedAt: time.Now().UnixMilli(),
	}, mainChatSourceEngagement)
	if storedID == "" {
		t.Fatal("append card for create-event timing failed")
	}
	defer func() {
		if err := a.store.DeleteStreamItemByID(context.Background(), rowID); err != nil {
			t.Errorf("remove measured card from fixture: %v", err)
		}
		a.mainChatRowsChanged(state.SessionID)
	}()
	_, emittedAt := nextMainChatWaitingEvent(t, events)
	createLatency := emittedAt.Sub(started)
	t.Logf("waiting create-to-event latency: %s", createLatency)

	results := []mainChatWaitingPerfTiming{
		summarizeMainChatWaitingPerf("recompute", recomputes),
		summarizeMainChatWaitingPerf("getter", getters),
	}
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := mainChatReadPerfArtifactDir(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main-chat-waiting-after.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("waiting-state fixture timings: %s; create-to-event=%s", raw, createLatency)
}

func summarizeMainChatWaitingPerf(operation string, samples []time.Duration) mainChatWaitingPerfTiming {
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return mainChatWaitingPerfTiming{
		Operation: operation,
		P50:       ordered[(len(ordered)-1)*50/100],
		P90:       ordered[(len(ordered)-1)*90/100],
		Max:       ordered[len(ordered)-1],
	}
}
