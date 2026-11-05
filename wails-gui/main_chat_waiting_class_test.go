package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMainChatWaitingClassificationMatchesDisplay(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	cards := []mainChatProactive{
		{MomentID: "question", Kind: "decision_due", State: "pending", Headline: "Reply?"},
		{MomentID: "withdrawn", Kind: "decision_due", State: "pending", ActivityOutcomeSource: "missing", Headline: "Reply?"},
		{MomentID: "person", Kind: "person_reappeared", State: "pending"},
		{MomentID: "weekly", Kind: "weekly_recap", State: "pending"},
		{MomentID: "meeting", Kind: "meeting_prep", State: "pending", ExpiresAt: now.Add(-time.Hour).UnixMilli()},
		{MomentID: "first-read-digest", Kind: "digest", State: "pending", Items: []mainChatProactive{{MomentID: "first", Kind: "first_read", State: "corrected"}, {MomentID: "routine", Kind: "brief_ready", State: "pending"}}},
		{MomentID: "digest", Kind: "digest", State: "pending", Items: []mainChatProactive{{MomentID: "child", Kind: "decision_due", State: "pending", ActivityOutcomeSource: "missing", Headline: "Reply?"}}},
	}
	for _, card := range cards {
		row := storage.StreamItemRow{ID: card.MomentID, Kind: "system", ToolName: mainChatProactiveTool, ToolInput: mainChatViewPayload(t, card), CreatedAt: now.UnixMilli()}
		ctx := withMainChatReadMemo(context.Background())
		full := app.projectMainChatRows(ctx, []storage.StreamItemRow{row})[0]
		light := app.projectMainChatWaitingClass(ctx, row.ToolInput)
		if got, want := mainChatRowClassForDisplay(row, light, now), mainChatRowClassForDisplay(row, full.ToolInput, now); got != want || mainChatWithdrawnThreadUpdate(light) != mainChatWithdrawnThreadUpdate(full.ToolInput) {
			t.Errorf("%s: class %s, want %s; withdrawal mismatch=%t", card.MomentID, got, want, mainChatWithdrawnThreadUpdate(light) != mainChatWithdrawnThreadUpdate(full.ToolInput))
		}
	}
}
