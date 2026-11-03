package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMainChatObservationPauseLifecycle(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	start := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	end := start.Add(23 * time.Minute)
	app.recordObservationPause(true, start)
	app.syncMainChatObservationPause(true, start)
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := app.store.LoadStreamItems(context.Background(), main.LocalID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("initial rows: %v %v", rows, err)
	}
	if got := mainChatRowClass(rows[0], end); got != MainChatRowOther {
		t.Fatalf("active class: %s", got)
	}
	app.recordObservationPause(false, end)
	app.syncMainChatObservationPause(false, end)
	rows, err = app.store.LoadStreamItems(context.Background(), main.LocalID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("completed rows: %v %v", rows, err)
	}
	var pause mainChatObservationPause
	if err := json.Unmarshal([]byte(rows[0].ToolInput), &pause); err != nil {
		t.Fatal(err)
	}
	if pause.StartAt != start.UnixMilli() || pause.EndAt != end.UnixMilli() || rows[0].CreatedAt != start.UnixMilli() {
		t.Fatalf("wrong interval or day: %+v %+v", pause, rows[0])
	}
	if got := mainChatRowClass(rows[0], end); got != MainChatRowRoutine {
		t.Fatalf("completed class: %s", got)
	}
}

func TestMainChatObservationPauseUnknownRecovery(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	row := storage.StreamItemRow{Kind: "system", ToolName: mainChatObservationPauseTool, ToolInput: `{"startAt":1793574300000}`}
	projected := app.projectObservationPause(row.ToolInput)
	var pause mainChatObservationPause
	if err := json.Unmarshal([]byte(projected), &pause); err != nil {
		t.Fatal(err)
	}
	if !pause.ResumedUnknown || pause.EndAt != 0 {
		t.Fatalf("invented recovery or retained action: %+v", pause)
	}
	if got := mainChatRowClassForDisplay(row, projected, time.Now()); got != MainChatRowRoutine {
		t.Fatalf("class = %s", got)
	}
}
