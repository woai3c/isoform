package main

import (
	"os"
	"path/filepath"
	"testing"

	"wails-gui/internal/handoff"
)

func TestTodayNeedCheckHasThreeStatesForHandoffs(t *testing.T) {
	rig := newHandoffTestRig(t)
	for _, entry := range []handoff.Entry{
		{ID: "blocked", Title: "Answer the question", Status: handoff.StatusBlocked},
		{ID: "done", Title: "Finished", Status: handoff.StatusCompleted},
		{ID: "unreadable", Title: "Broken", Status: handoff.StatusActive},
	} {
		if _, err := rig.handoffs.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := rig.app.GetTodayNeedCheck("blocked", "work_blocked", "handoff", "blocked"); err != nil || got.Kind != "" {
		t.Fatalf("open = %+v, %v", got, err)
	}
	for _, id := range []string{"done", "gone"} {
		if got, err := rig.app.GetTodayNeedCheck(id, "review", "handoff", id); err != nil || got.Kind != "resolved" {
			t.Fatalf("%s = %+v, %v", id, got, err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(rig.handoffs.Dir(), "unreadable*"))
	if len(matches) == 0 {
		t.Fatal("missing stored handoff")
	}
	for _, path := range matches {
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := rig.app.GetTodayNeedCheck("unreadable", "decision_due", "handoff", "unreadable"); err == nil || got.Kind == "resolved" {
		t.Fatalf("an unreadable source claimed the item was gone: %+v, %v", got, err)
	}
	if _, err := rig.app.GetTodayNeedCheck("blocked", "work_blocked", "handoff", "other"); err == nil {
		t.Fatal("a mismatched target was checked")
	}
}

func TestTodayNeedCheckHasThreeStatesForChatItems(t *testing.T) {
	app := newMainChatReadMemoFixture(t).app
	card := mainChatProactive{MomentID: "decision-moment", Kind: "decision_due", Headline: "Pick the venue", Body: "Pick the venue.", State: "pending",
		Target: mainChatTarget{Kind: "mail", ID: "message-decision"}, ActionIDs: []string{"not_now", "dismiss"}, Evidence: []mainChatEvidenceRef{}}
	if app.postProactiveItem(card, "need-check:decision") == "" {
		t.Fatal("card was not posted")
	}
	if got, err := app.GetTodayNeedCheck(card.MomentID, "decision_due", "chat_item", "row"); err != nil || got.Kind != "" {
		t.Fatalf("open = %+v, %v", got, err)
	}
	if got, err := app.GetTodayNeedCheck(card.MomentID, "work_blocked", "chat_item", "row"); err == nil || got.Kind == "resolved" {
		t.Fatalf("a mismatched kind = %+v, %v", got, err)
	}
	if got, err := app.GetTodayNeedCheck("never-posted", "decision_due", "chat_item", "row"); err != nil || got.Kind != "resolved" {
		t.Fatalf("gone = %+v, %v", got, err)
	}
	app.closedEyes.enabled.Store(true)
	if got, err := app.GetTodayNeedCheck(card.MomentID, "decision_due", "chat_item", "row"); err == nil || got.Kind == "resolved" {
		t.Fatalf("paused source = %+v, %v", got, err)
	}
	app.closedEyes.enabled.Store(false)
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	if got, err := app.GetTodayNeedCheck(card.MomentID, "decision_due", "chat_item", "row"); err != nil || got.Kind != "resolved" {
		t.Fatalf("dismissed = %+v, %v", got, err)
	}
}
