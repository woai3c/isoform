package main

import (
	"encoding/json"
	"testing"
	"time"

	"wails-gui/storage"
)

// A thread update whose source is gone is routine, never waiting: its state is expired, so the phone can never
// label it "Waiting on you" and it cannot count toward waitingCount.
func TestWithdrawnThreadUpdateIsRoutineNotWaiting(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	card := mainChatProactive{MomentID: "withdrawn", Kind: "decision_due", State: "pending",
		ActivityOutcomeSource: "missing-source", Headline: "Old update", Body: "Old update",
		ActionIDs: []string{"open"}}
	if !app.projectThreadOutcomeItem(t.Context(), &card, "en") {
		t.Fatal("unreadable source was not projected")
	}
	if card.Kind != "thread_update_unavailable" || card.State != "expired" || len(card.ActionIDs) != 0 {
		t.Fatalf("withdrawn update kept an actionable state: %+v", card)
	}
	if class := mainChatProactiveRowClass(card, now); class != MainChatRowRoutine {
		t.Fatalf("withdrawn update classified as %s, want routine", class)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	withdrawn := storage.StreamItemRow{ID: "withdrawn", Kind: "system", ToolName: mainChatProactiveTool,
		ToolInput: string(raw), SortOrder: 0, CreatedAt: now.UnixMilli()}
	view, keep := mobileChatItemFromRow(withdrawn)
	if !keep || view.Proactive == nil || view.Proactive.State == "pending" {
		t.Fatalf("phone item for a withdrawn update = %+v, keep %v; want a routine item", view.Proactive, keep)
	}
}
