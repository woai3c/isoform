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

// chat-review v1.0.10 H9: a missed-brief line carries the brief's own day,
// slot, due time and recorded cause, so it is worded from the brief's date.
func TestMainChatBriefMissedProjectionCarriesTheBriefsOwnDay(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	ctx := context.Background()
	for _, row := range []storage.EngagementMomentRow{
		{ID: "missed-evening", Kind: "brief_missed", SubjectID: "2026-11-01-evening", DedupeKey: "brief_missed:2026-11-01-evening", Params: `{"slot":"evening","time":"18:00","reason":"asleep"}`},
		{ID: "missed-morning", Kind: "brief_missed", SubjectID: "2026-11-02-morning", DedupeKey: "brief_missed:2026-11-02-morning", Params: `{"slot":"morning","time":"07:30"}`},
	} {
		row.CreatedAt = time.Now().UnixMilli()
		row.State = storage.EngagementStatePending
		if _, err := app.store.CreateEngagementMoment(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	input := `{"period":"today","entries":[{"id":"a","momentId":"missed-evening","kind":"brief_missed","text":"old","target":{"kind":"today"}},{"id":"b","momentId":"missed-morning","kind":"brief_missed","text":"old","target":{"kind":"today"}},{"id":"c","kind":"brief_missed","text":"old","target":{"kind":"today"}}]}`
	var out struct {
		Entries []struct {
			Day   string `json:"briefDay"`
			Slot  string `json:"briefSlot"`
			Time  string `json:"briefTime"`
			Cause string `json:"briefCause"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(app.mainChatCaptureProjector(ctx)(mainChatStatusTool, input)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 3 {
		t.Fatalf("entries = %+v", out.Entries)
	}
	if got := out.Entries[0]; got.Day != "2026-11-01" || got.Slot != "evening" || got.Time != "18:00" || got.Cause != "asleep" {
		t.Fatalf("evening line = %+v", got)
	}
	if got := out.Entries[1]; got.Day != "2026-11-02" || got.Slot != "morning" || got.Time != "07:30" || got.Cause != "" {
		t.Fatalf("morning line = %+v", got)
	}
	if got := out.Entries[2]; got.Day != "" {
		t.Fatalf("a line with no moment gained a day: %+v", got)
	}
}
