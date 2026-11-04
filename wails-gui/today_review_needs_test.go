package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestTodayNeedsOrdering(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	items := []TodayNeed{
		{ID: "recent-undated", Kind: "work_blocked", CreatedAt: at(-time.Hour)},
		{ID: "later", Kind: "commitment_slipping", CreatedAt: at(-4 * time.Hour), DueAt: at(4 * time.Hour)},
		{ID: "old-undated", Kind: "waiting_on_reply", CreatedAt: at(-4 * time.Hour)},
		{ID: "call", Kind: "call_promises", CreatedAt: at(-90 * time.Minute)},
		{ID: "soon", Kind: "decision_due", CreatedAt: at(-time.Hour), DueAt: at(time.Hour)},
		{ID: "old-call", Kind: "call_promises", CreatedAt: at(-3 * time.Hour)},
		{ID: "unknown", Kind: "work_blocked"},
	}
	orderTodayNeeds(items, now)
	got := make([]string, len(items))
	for i := range items {
		got[i] = items[i].ID
	}
	want := []string{"call", "soon", "later", "old-undated", "old-call", "recent-undated", "unknown"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("waiting order = %v, want %v", got, want)
	}
}

func TestGetNeedsYouCountsAllWaitingHandoffsAndReturnsExactTargets(t *testing.T) {
	rig := newHandoffTestRig(t)
	for _, entry := range []handoff.Entry{
		{ID: "review", Title: "Review the report", Status: handoff.StatusNeedsReview},
		{ID: "blocked", Title: "Answer the question", Status: handoff.StatusBlocked},
		{ID: "active", Title: "Approve the task", Status: handoff.StatusActive},
		{ID: "done", Title: "Finished", Status: handoff.StatusCompleted},
	} {
		if _, err := rig.handoffs.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	all, err := rig.app.GetNeedsYou(0)
	if err != nil || all.Total != 3 || len(all.Items) != 3 {
		t.Fatalf("all waiting = %+v, %v", all, err)
	}
	preview, err := rig.app.GetNeedsYou(2)
	if err != nil || preview.Total != 3 || len(preview.Items) != 2 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	for i, item := range preview.Items {
		if !reflect.DeepEqual(item, all.Items[i]) || item.TargetKind != "handoff" || item.TargetID != item.ID {
			t.Fatalf("preview lost its authoritative target: %+v", item)
		}
	}
}

func TestGetNeedsYouRespectsTaskNotNow(t *testing.T) {
	rig := newHandoffTestRig(t)
	entry := handoff.Entry{ID: "waiting", Title: "Waiting task", Status: handoff.StatusBlocked,
		Thread: &handoff.Thread{NotNowUntil: time.Now().Add(time.Hour).UnixMilli()}}
	if _, err := rig.handoffs.Save(entry); err != nil {
		t.Fatal(err)
	}
	page, err := rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 0 {
		t.Fatalf("deferred task still needs attention: %+v, %v", page, err)
	}
	if _, err := rig.handoffs.MutateThread(entry.ID, func(thread *handoff.Thread) { thread.NotNowUntil = time.Now().Add(-time.Second).UnixMilli() }); err != nil {
		t.Fatal(err)
	}
	page, err = rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 1 || page.Items[0].TargetID != entry.ID {
		t.Fatalf("task did not return after deferral: %+v, %v", page, err)
	}
}

func TestTodayNeedFromChatKeepsTheExactWaitingItem(t *testing.T) {
	now := time.Now()
	card := mainChatProactive{MomentID: "moment", Kind: "waiting_on_reply", State: "pending", Headline: "A reply is waiting", Localized: map[string]mainChatProactiveCopy{"zh-CN": {Body: "等待回复"}}, Target: mainChatTarget{Kind: mainChatTargetHandoff, ID: "work"}}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	row := StreamItemData{ID: "chat-row", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(raw), CreatedAt: now.Add(-time.Hour).UnixMilli()}
	need, handoffID, ok := todayNeedFromChatItem(row, "main", now)
	if !ok || need.TargetKind != "chat_item" || need.TargetID != "chat-row" || need.ChatID != "main" || handoffID != "work" || need.Item == nil || need.Item.ID != "chat-row" || need.SentenceLocalized["zh-CN"] != "等待回复" {
		t.Fatalf("waiting item = %+v, handoff = %q, ok = %v", need, handoffID, ok)
	}
	card.State = "acted"
	raw, _ = json.Marshal(card)
	row.ToolInput = string(raw)
	if _, _, ok := todayNeedFromChatItem(row, "main", now); ok {
		t.Fatal("acted card remained waiting")
	}
	card.State, card.Kind = "pending", "suggestion"
	raw, _ = json.Marshal(card)
	row.ToolInput = string(raw)
	if _, _, ok := todayNeedFromChatItem(row, "main", now); ok {
		t.Fatal("retired suggestion acquired authority")
	}
}

func TestTodayNeedUsesMomentDeadlineNotExpiry(t *testing.T) {
	app, _, _ := todaySendTestApp(t)
	now := time.Now()
	deadline := now.Add(time.Hour).UnixMilli()
	_, err := app.store.CreateEngagementMoment(context.Background(), storage.EngagementMomentRow{
		ID: "due-moment", Kind: "decision_due", DedupeKey: "due-moment", DueAt: deadline, ExpiresAt: now.Add(12 * time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	need := TodayNeed{ID: "due-moment"}
	if err := app.todayNeedAddDeadline(context.Background(), &need); err != nil {
		t.Fatal(err)
	}
	if need.DueAt != deadline {
		t.Fatalf("deadline = %d, want %d", need.DueAt, deadline)
	}
}

func TestTodayNeedsExpandsDigestIntoExactWaitingCards(t *testing.T) {
	now := time.Now()
	digest := mainChatProactive{Kind: "digest", State: "pending", Items: []mainChatProactive{
		{MomentID: "one", Kind: "waiting_on_reply", State: "pending", Headline: "First reply"},
		{MomentID: "two", Kind: "work_blocked", State: "pending", Headline: "Second question"},
		{MomentID: "done", Kind: "work_finished", State: "pending", Headline: "Finished"},
	}}
	raw, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	row := StreamItemData{ID: "digest-row", ToolName: mainChatProactiveTool, ToolInput: string(raw), CreatedAt: now.UnixMilli()}
	needs := todayNeedsFromChatItem(row, "main", now)
	if len(needs) != 2 || needs[0].need.ID != "one" || needs[1].need.ID != "two" {
		t.Fatalf("needs = %+v", needs)
	}
	for _, item := range needs {
		if item.need.TargetID != row.ID || item.need.Item == nil || item.need.Item.ToolInput != row.ToolInput {
			t.Fatalf("lost digest target: %+v", item.need)
		}
	}
}

func TestTodayReviewChangeEventsCarryNoContent(t *testing.T) {
	app := mainChatTestApp(t)
	events := app.eventBroadcaster.AddClient("today-review-events")
	defer app.eventBroadcaster.RemoveClient("today-review-events")
	app.emitBrowserEvent("handoff:updated", map[string]string{"title": "Private result", "name": "Private person"})
	needs := awaitBrowserEvent(t, events, "today:needs-changed")
	done := awaitBrowserEvent(t, events, "today:done-changed")
	if strings.Contains(needs+done, "Private result") || strings.Contains(needs+done, "Private person") {
		t.Fatal("content leaked into Today change events")
	}
}

func TestGetNeedsYouKeepsTasksTheSystemStoppedAndLeavesOutYours(t *testing.T) {
	rig := newHandoffTestRig(t)
	for _, entry := range []handoff.Entry{
		{ID: "no-ai", Title: "Draft the memo", Status: handoff.StatusAborted, StoppedBy: handoff.StoppedBySystem, StopCause: handoff.CauseNoAssistant},
		{ID: "restarted", Title: "Sort the files", Status: handoff.StatusBlocked, BlockedReason: handoff.BlockedInterrupted},
		{ID: "mine", Title: "Stopped by me", Status: handoff.StatusAborted, StoppedBy: handoff.StoppedByPerson},
		{ID: "legacy", Title: "Old stop", Status: handoff.StatusAborted},
		{ID: "stale", Title: "Old suggestion", Status: handoff.StatusStale},
	} {
		if _, err := rig.handoffs.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	page, err := rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 2 {
		t.Fatalf("needs you = %+v, %v", page, err)
	}
	causes := map[string]string{}
	for _, item := range page.Items {
		causes[item.TargetID] = item.StopCause
	}
	if causes["no-ai"] != handoff.CauseNoAssistant || causes["restarted"] != handoff.CauseRestarted {
		t.Fatalf("causes = %v", causes)
	}
}

func TestLeftOffOnlyWaitsWhenItAsksAQuestion(t *testing.T) {
	now := time.Now()
	card := mainChatProactive{MomentID: "left-off", Kind: "decision_due", State: "pending", Target: mainChatTarget{Kind: mainChatTargetThread}, Body: "You researched a launch."}
	row := StreamItemData{ID: "row", ToolName: mainChatProactiveTool}
	for _, tc := range []struct {
		body    string
		waiting bool
	}{{"You researched a launch.", false}, {"Would you like to continue?", true}} {
		card.Body = tc.body
		raw, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		row.ToolInput = string(raw)
		_, _, got := todayNeedFromChatItem(row, "main", now)
		if got != tc.waiting || (mainChatProactiveRowClass(card, now) == MainChatRowWaiting) != tc.waiting {
			t.Fatalf("%q: needs=%v class=%v", tc.body, got, mainChatProactiveRowClass(card, now))
		}
	}
}
