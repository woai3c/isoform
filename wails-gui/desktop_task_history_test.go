package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestDesktopFinishedHistoryIncludesOlderRunsAndUnboundedChat(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	now := time.Now()
	if err := app.store.SaveChatDeliverable(ctx, storage.ChatDeliverableRow{ID: "older-chat", Title: "Old file", Files: `["/tmp/old.md"]`}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		if err := app.store.SaveChatDeliverable(ctx, storage.ChatDeliverableRow{ID: fmt.Sprintf("empty-%03d", i), Title: "No file", Files: `[]`}); err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range []storage.CronJobRow{
		{ID: "weekly", Name: "Weekly update", SurfaceKind: "scheduled"},
		{ID: "internal", Name: "Internal", SurfaceKind: "system"},
	} {
		if err := app.store.SaveCronJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	old := now.AddDate(0, -3, 0).UnixMilli()
	for _, run := range []storage.CronRunRow{
		{ID: "old", JobID: "weekly", StartedAt: old, FinishedAt: old + 1000, Status: "ok", ResultSummary: "Report ready"},
		{ID: "failed", JobID: "weekly", StartedAt: old + 2000, Status: "error"},
		{ID: "private", JobID: "internal", StartedAt: old, Status: "ok"},
	} {
		if err := app.store.InsertCronRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	got, err := app.desktopFinishedDeliverables(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]DeliverableEntry{}
	for _, item := range got {
		seen[item.ID] = item
	}
	if len(seen) != 2 || seen["chat:older-chat"].Title != "Old file" || seen["automation:weekly:old"].Title != "Weekly update" {
		t.Fatalf("history = %+v", got)
	}
}
