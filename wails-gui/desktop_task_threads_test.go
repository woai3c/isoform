package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func TestDesktopTasksIncludeOldSentDraftsInAllDone(t *testing.T) {
	app, _, moment := outboundSendTestCard(t)
	var err error
	app.handoffStore, err = handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	card := outboundStoredCard(t, app, moment.ID)
	send := outboundRowForMainChatDraft(&card, moment.DedupeKey)
	if _, claimed, err := app.store.ClaimOutboundSend(context.Background(), send, time.Now().UnixMilli()); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	sentAt := time.Now().AddDate(0, -2, 0).UnixMilli()
	if err := app.store.MarkOutboundSendAccepted(context.Background(), moment.ID, card.DraftContent.Revision, sentAt, sentAt+time.Hour.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ResolveMainChatProactiveAction(moment.ID, "send"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if app.desktopTaskBridge != nil {
			app.desktopTaskBridge.Stop()
		}
	})
	page, err := app.ListDesktopTaskThreads()
	if err != nil {
		t.Fatal(err)
	}
	id := mobileDraftTaskID(moment.ID)
	count := 0
	for _, task := range page.Tasks {
		if task.ID == id && task.Group == mobileTaskGroupDone {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("old draft count = %d, tasks = %+v", count, page.Tasks)
	}
	recent, err := app.mobileDraftTasks(context.Background(), mobileGoalsNow())
	if err != nil {
		t.Fatal(err)
	}
	for _, draft := range recent {
		if draft.TaskID == id {
			t.Fatal("old draft leaked into the phone's recent task list")
		}
	}
	if _, err := app.GetDesktopTaskThread(id); err != nil {
		t.Fatalf("old completed draft cannot be opened: %v", err)
	}
}

func TestDesktopMobileTaskChoiceIsSerialized(t *testing.T) {
	bridge, store, _ := mobileTestThreadBridge(t)
	thread := &handoff.Thread{}
	thread.AppendEvent(handoff.Event{ID: "q", Kind: handoff.EventIsoform, Text: "Which?", Choices: []handoff.Choice{{ID: "short", Label: "Keep it short"}}})
	if _, err := store.Save(handoff.Entry{ID: "shared-choice", Title: "FAQ", Status: handoff.StatusActive, Body: "Write the FAQ.", Thread: thread}); err != nil {
		t.Fatal(err)
	}
	app := bridge.app
	t.Cleanup(func() {
		if app.desktopTaskBridge != nil {
			app.desktopTaskBridge.Stop()
		}
	})
	start := make(chan struct{})
	results := make(chan bool, 2)
	go func() {
		<-start
		_, err := app.ActOnDesktopTask("shared-choice", DesktopTaskActionInput{Action: "choose", EventID: "q", ChoiceID: "short"})
		results <- err == nil
	}()
	go func() {
		<-start
		response := mobileTestChatCall(bridge, mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff), http.MethodPost, "/tasks/shared-choice/actions", `{"idempotencyKey":"choice","action":"choose","eventId":"q","choiceId":"short"}`)
		results <- response.Code == http.StatusOK
	}()
	close(start)
	first, second := <-results, <-results
	if first == second {
		t.Fatalf("exactly one choice should succeed: %v %v", first, second)
	}
	saved, err := store.Get("shared-choice")
	if err != nil || len(saved.Thread.Events) != 2 || strings.Count(saved.Body, "Keep it short") != 1 {
		t.Fatalf("choice was not recorded once: %+v, %v", saved, err)
	}
}

func TestDesktopTaskThreadSharesPhoneActions(t *testing.T) {
	bridge, store, now := mobileTestThreadBridge(t)
	entry := handoff.Entry{ID: "desktop-task", Title: "Decide", Status: handoff.StatusNeedsReview, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(), Thread: &handoff.Thread{Origin: &handoff.Origin{Kind: handoff.OriginUpNext, At: now.UnixMilli()}}}
	entry.Thread.AppendEvent(handoff.Event{ID: "question", Kind: handoff.EventIsoform, At: now.UnixMilli(), Text: "Which?", Choices: []handoff.Choice{{ID: "one", Label: "First"}}})
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	app := bridge.app
	t.Cleanup(func() {
		if app.desktopTaskBridge != nil {
			app.desktopTaskBridge.Stop()
		}
	})
	detail, err := app.GetDesktopTaskThread(entry.ID)
	if err != nil || detail.Origin == nil || len(detail.Events) != 1 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	detail, err = app.ActOnDesktopTask(entry.ID, DesktopTaskActionInput{Action: "choose", EventID: "question", ChoiceID: "one"})
	if err != nil || detail.Events[0].ChosenID != "one" || len(detail.Events) != 2 || detail.Events[1].Text != "First" {
		t.Fatalf("choice = %+v, %v", detail, err)
	}
	if _, err := app.ActOnDesktopTask(entry.ID, DesktopTaskActionInput{Action: "choose", EventID: "question", ChoiceID: "one"}); err == nil {
		t.Fatal("choice accepted twice")
	}
	detail, err = app.ReplyToDesktopTask(entry.ID, "A note")
	if err != nil || detail.Events[len(detail.Events)-1].Text != "A note" {
		t.Fatalf("reply = %+v, %v", detail, err)
	}
	detail, err = app.ActOnDesktopTask(entry.ID, DesktopTaskActionInput{Action: "mark_done"})
	if err != nil || detail.Outcome == nil || detail.Outcome.Kind != handoff.OutcomeMarkedDone {
		t.Fatalf("outcome = %+v, %v", detail, err)
	}
}

func TestDesktopTaskDraftUsesPhoneCheckBeforeSend(t *testing.T) {
	bridge, fake := mobileTestDraftBridge(t)
	app := bridge.app
	t.Cleanup(func() {
		if app.desktopTaskBridge != nil {
			app.desktopTaskBridge.Stop()
		}
	})
	page, err := app.ListDesktopTaskThreads()
	if err != nil || len(page.Tasks) != 1 {
		t.Fatalf("tasks = %+v, %v", page, err)
	}
	detail, err := app.GetDesktopTaskThread(page.Tasks[0].ID)
	if err != nil || detail.Draft == nil || fake.count() != 0 {
		t.Fatalf("draft = %+v, %v", detail, err)
	}
	detail, err = app.ActOnDesktopTask(page.Tasks[0].ID, DesktopTaskActionInput{Action: "send"})
	if err != nil || detail.Draft == nil || detail.Draft.State != "sent" || fake.count() != 1 {
		t.Fatalf("send = %+v, %v, count %d", detail, err, fake.count())
	}
}

func TestDesktopTaskOfferRechecksAndStartsHandoff(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	app := bridge.app
	var err error
	app.handoffStore, err = handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mobileTestOfferRunner(t, bridge)
	offers := mobileTestOffers(t, bridge)
	if len(offers) == 0 {
		t.Fatal("no offers")
	}
	// This fixture has no Wails runtime; session creation must not emit to one.
	app.ctx = nil
	if _, err := app.StartDesktopTaskOffer(offers[0].ID, "stale"); err == nil {
		t.Fatal("accepted stale offer")
	}
	rows, err := app.handoffStore.List()
	if err != nil || len(rows) != 0 {
		t.Fatalf("stale offer created tasks: %+v %v", rows, err)
	}
	result, err := app.StartDesktopTaskOffer(offers[0].ID, offers[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "" || result.Prompt != "" {
		t.Fatalf("start returned source text: %+v", result)
	}
	replay, err := app.StartDesktopTaskOffer(offers[0].ID, offers[0].Version)
	if err != nil || replay.HandoffID != result.HandoffID {
		t.Fatalf("start replay = %+v, %v", replay, err)
	}
	saved, err := app.handoffStore.Get(result.HandoffID)
	if err != nil || saved.Status != handoff.StatusInProgress || saved.Thread == nil || saved.Thread.Origin == nil || saved.Thread.Origin.Kind != handoff.OriginUpNext {
		t.Fatalf("started task = %+v, %v", saved, err)
	}
	if app.desktopTaskBridge != nil {
		mobileTestResetChatRunner(t, app.desktopTaskBridge)
		app.desktopTaskBridge.Stop()
		app.desktopTaskBridge = nil
	}
}

func TestDesktopTaskOfferWithdrawalDoesNotLeaveUnstartedTask(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	app := bridge.app
	var err error
	app.handoffStore, err = handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	turns := mobileTestOfferRunner(t, bridge)
	offers := mobileTestOffers(t, bridge)
	if len(offers) == 0 {
		t.Fatal("no offers")
	}
	app.ctx = nil
	mobileChatOfferDispatchHook = func() {
		if err := app.activity.store.DeleteSession("idea-source-2"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.StartDesktopTaskOffer(offers[0].ID, offers[0].Version); err == nil {
		t.Fatal("started an offer after source withdrawal")
	}
	rows, err := app.handoffStore.List()
	if err != nil || len(rows) != 0 || turns.count() != 0 {
		t.Fatalf("withdrawal left tasks %+v, error %v, turns %d", rows, err, turns.count())
	}
	if app.desktopTaskBridge != nil {
		app.desktopTaskBridge.Stop()
		app.desktopTaskBridge = nil
	}
}
