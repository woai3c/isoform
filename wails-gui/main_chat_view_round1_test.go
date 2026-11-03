package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMainChatRound1DigestStopsWaitingAfterChildrenResolve(t *testing.T) {
	a := mainChatTestApp(t)
	children := []mainChatProactive{{MomentID: "reply-a", Kind: "waiting_on_reply", State: "pending", ActionIDs: []string{"dismiss"}}, {MomentID: "reply-b", Kind: "waiting_on_reply", State: "opened", ActionIDs: []string{"dismiss"}}}
	id := a.postProactiveItem(mainChatProactive{MomentID: "bundle", Kind: "digest", State: "pending", Items: children}, "bundle")
	for i, child := range children {
		before, err := a.GetMainChatOverview()
		if err != nil || before.Waiting != 1 {
			t.Fatalf("before %d: %+v %v", i, before, err)
		}
		if _, err = a.ResolveMainChatProactiveAction(child.MomentID, "dismiss"); err != nil {
			t.Fatal(err)
		}
	}
	overview, err := a.GetMainChatOverview()
	if err != nil || overview.Waiting != 0 {
		t.Fatalf("all children resolved: %+v %v", overview, err)
	}
	page, err := a.ListMainChatWaitingItems("", 50)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("resolved digest in waiting page %+v %v", page, err)
	}
	row, err := a.store.LoadStreamItem(t.Context(), id)
	if err != nil || mainChatRowClass(*row, time.Now()) != MainChatRowOther {
		t.Fatalf("resolved digest class %+v %v", row, err)
	}
	// A stored legacy pending wrapper also derives its state from its children.
	closed := mainChatViewCard(t, "legacy-digest", "digest", "pending", time.Now())
	closed.ToolInput = mainChatViewPayload(t, mainChatProactive{Kind: "digest", State: "pending", Items: []mainChatProactive{{Kind: "waiting_on_reply", State: "dismissed"}, {Kind: "waiting_on_reply", State: "deferred"}}})
	if mainChatRowClass(closed, time.Now()) != MainChatRowOther {
		t.Fatal("legacy wrapper state overrode completed children")
	}
}

func TestMainChatRound1TagRevisionMissKeepsChatReadable(t *testing.T) {
	for _, change := range []string{"activity", "main"} {
		t.Run(change, func(t *testing.T) {
			a, session, topic := mainChatRowTopicFixture(t)
			id := a.postProactiveItem(mainChatRowTopicCard("tagged"), "tagged")
			if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), session, storage.StreamItemRow{ID: "explicit", Kind: "user", Content: "My words", TopicID: topic}); err != nil {
				t.Fatal(err)
			}
			a.mainChatRowTopicsBeforeReadPublish = func() {
				if change == "activity" {
					if _, err := a.activity.store.db.Exec(`UPDATE activities SET updated_at=updated_at+1 WHERE id='atlas'`); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := a.store.SetUIState(t.Context(), "round1-write", "changed"); err != nil {
						t.Fatal(err)
					}
				}
			}
			page, err := a.LoadStreamItemsPage(session, -1, 50)
			if err != nil {
				t.Fatalf("tag revision miss failed chat: %v", err)
			}
			found := false
			for _, item := range page.Items {
				if item.ID == id {
					found = true
					if len(item.TopicIDs) != 0 {
						t.Fatal("unchecked derived tag survived fallback")
					}
				}
				if item.ID == "explicit" && !reflect.DeepEqual(item.TopicIDs, []string{topic}) {
					t.Fatal("fallback removed explicit user membership")
				}
			}
			if !found {
				t.Fatal("fallback lost the original chat row")
			}
			if tags := mainChatRowStoredTags(t, a, session, id); len(tags) != 1 {
				t.Fatal("fallback deleted persisted membership")
			}
		})
	}
}

func TestMainChatRound1TagsCollectOncePerPageAndRevision(t *testing.T) {
	a, session, _ := mainChatRowTopicFixture(t)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("card-%d", i)
		a.postProactiveItem(mainChatRowTopicCard(id), id)
	}
	a.topicsSvc().invalidateChatSources()
	collections := 0
	a.todayChatCollectionObserver = func() { collections++ }
	page, err := a.LoadStreamItemsPage(session, -1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 5 || collections != 1 {
		t.Fatalf("page rows=%d collections=%d want one", len(page.Items), collections)
	}
	if _, err = a.LoadStreamItemsPage(session, -1, 50); err != nil {
		t.Fatal(err)
	}
	if collections != 1 {
		t.Fatalf("same revision recollected sources: %d", collections)
	}
	if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`); err != nil {
		t.Fatal(err)
	}
	page, err = a.LoadStreamItemsPage(session, -1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if collections != 2 {
		t.Fatalf("changed revision collections=%d", collections)
	}
	for _, item := range page.Items {
		if len(item.TopicIDs) != 0 {
			t.Fatal("cache survived source withdrawal")
		}
	}
}

func TestMainChatRound1BackfillCollectsOnceForBatch(t *testing.T) {
	a, session, _ := mainChatRowTopicFixture(t)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("old-%d", i)
		row := mainChatViewCard(t, id, "goal_check_in", "pending", time.Now().Add(-time.Hour))
		row.ToolInput = mainChatViewPayload(t, mainChatRowTopicCard(id))
		if _, err := a.store.AppendStreamItem(t.Context(), session, row); err != nil {
			t.Fatal(err)
		}
	}
	a.topicsSvc().invalidateChatSources()
	collections := 0
	a.todayChatCollectionObserver = func() { collections++ }
	if err := a.backfillMainChatRowTopics(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if collections != 1 {
		t.Fatalf("backfill collections=%d want one", collections)
	}
	for i := 0; i < 5; i++ {
		if len(mainChatRowStoredTags(t, a, session, fmt.Sprintf("old-%d", i))) != 1 {
			t.Fatal("batch lost membership")
		}
	}
}

func TestMainChatRound1UpcomingConfirmationsStayVisible(t *testing.T) {
	a := mainChatTestApp(t)
	for _, status := range []string{"changed", "paused", "resumed", "started", "removed"} {
		a.postUpcomingReport(upcomingTarget{Kind: upcomingKindSchedule, Job: CronJobState{ID: "job", Name: "Review"}}, status, "", "Confirmation")
	}
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := a.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if mainChatRowClass(row, time.Now()) != MainChatRowFinished {
			t.Fatalf("user confirmation folded: %s", row.ToolInput)
		}
	}
	a.postScheduleAPIChange("job", "Review", "paused")
	rows, err = a.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if mainChatRowClass(rows[len(rows)-1], time.Now()) != MainChatRowRoutine {
		t.Fatal("schedule API update is no longer routine")
	}
}

func TestMainChatRound1WaitingCursorAdvancesAcrossExpiredCandidates(t *testing.T) {
	a := mainChatTestApp(t)
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	live := mainChatViewCard(t, "live", "decision_due", "pending", now.Add(-3*time.Minute))
	for _, row := range []storage.StreamItemRow{live,
		{ID: "expired-a", Kind: "system", ToolName: mainChatProactiveTool, CreatedAt: now.Add(-time.Minute).UnixMilli(), ToolInput: mainChatViewPayload(t, mainChatProactive{MomentID: "expired-a", Kind: "decision_due", State: "pending", ExpiresAt: 1})},
		{ID: "expired-b", Kind: "system", ToolName: mainChatProactiveTool, CreatedAt: now.UnixMilli(), ToolInput: mainChatViewPayload(t, mainChatProactive{MomentID: "expired-b", Kind: "decision_due", State: "pending", ExpiresAt: 1})},
	} {
		if _, err = a.store.AppendStreamItem(t.Context(), main.LocalID, row); err != nil {
			t.Fatal(err)
		}
	}
	first, err := a.ListMainChatWaitingItems("", 2)
	if err != nil || len(first.Items) != 0 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("expired scan %+v %v", first, err)
	}
	next, err := a.ListMainChatWaitingItems(first.NextCursor, 2)
	if err != nil || next.HasMore || len(next.Items) != 1 || next.Items[0].ID != "live" {
		t.Fatalf("live card stranded %+v %v", next, err)
	}
}

func TestMainChatRound1DigestIgnoresRoutineAndExpiredChildren(t *testing.T) {
	now := time.Now()
	card := mainChatProactive{Kind: "digest", State: "pending", Items: []mainChatProactive{{Kind: "brief_ready", State: "pending"}, {Kind: "work_finished", State: "pending"}, {Kind: "waiting_on_reply", State: "pending", ExpiresAt: now.UnixMilli()}}}
	if mainChatProactiveRowClass(card, now) != MainChatRowOther {
		t.Fatal("non-actionable children kept digest waiting")
	}
	card.Items = append(card.Items, mainChatProactive{Kind: "digest", State: "opened", Items: []mainChatProactive{{Kind: "decision_due", State: "pending"}}})
	if mainChatProactiveRowClass(card, now) != MainChatRowWaiting {
		t.Fatal("nested permission lost its wait")
	}
}

func TestMainChatRound1UnmarkedLegacyScheduleConfirmationsStayVisible(t *testing.T) {
	for _, status := range []string{"changed", "paused", "resumed", "started", "removed"} {
		if mainChatReportIsRoutine(mainChatReport{Source: mainChatSourceSchedule, Status: status}) {
			t.Fatalf("ambiguous legacy confirmation folded: %s", status)
		}
	}
	if !mainChatReportIsRoutine(mainChatReport{Source: mainChatSourceSchedule, Status: "created"}) {
		t.Fatal("unambiguous legacy API creation lost routine class")
	}
}
