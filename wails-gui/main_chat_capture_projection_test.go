package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMainChatCaptureProjectionUsesRecordedInterval(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	ctx := context.Background()
	start := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	end := start.Add(time.Hour)
	_, err := app.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{ID: "capture-real", Kind: "capture_paused", SubjectID: "screen", DedupeKey: "capture:screen:real", CreatedAt: start.UnixMilli(), State: storage.EngagementStatePending})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]engagementCaptureOutageInterval{{Source: "screen", Start: start.UnixMilli(), End: end.UnixMilli()}, {Source: "microphone", Start: start.UnixMilli(), End: end.Add(time.Hour).UnixMilli()}})
	if err := app.store.SetUIState(ctx, engagementCaptureIntervalsKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	project := app.mainChatCaptureProjector(ctx)
	input := `{"period":"today","entries":[{"id":"line","momentId":"capture-real","kind":"capture_paused","text":"old","target":{"kind":"settings"}},{"id":"other","kind":"capture_paused","text":"old","target":{"kind":"settings"}}]}`
	var out struct {
		Entries []struct {
			Start int64 `json:"captureStartAt"`
			End   int64 `json:"captureEndAt"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(project(mainChatStatusTool, input)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 || out.Entries[0].Start != start.UnixMilli() || out.Entries[0].End != end.UnixMilli() || out.Entries[1].End != 0 {
		t.Fatalf("wrong projection: %+v", out)
	}
}
