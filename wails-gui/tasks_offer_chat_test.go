package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

func taskOfferChatFixture(t *testing.T) (*App, string, TaskSuggestionDraft) {
	t.Helper()
	bridge := mobileTestOfferBridge(t)
	app := bridge.app
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	session := *main
	session.LocalID = "task-offer-chat"
	session.Kind = ""
	if err := app.store.SaveSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	app.sessionTempDirs = map[string]string{}
	app.streams = map[string]context.CancelFunc{}
	app.runIDByTag = map[string]string{}
	app.pendingTraceIDs = map[string]string{}
	var offer mobileOffer
	for _, candidate := range mobileTestOffers(t, bridge) {
		if candidate.Kind == "topic_find" {
			offer = candidate
			break
		}
	}
	if offer.ID == "" {
		t.Fatal("missing topic find")
	}
	draft, err := app.PrepareTaskSuggestion(offer.ID, offer.Version)
	if err != nil {
		t.Fatal(err)
	}
	return app, session.LocalID, draft
}

func TestTaskOfferPrepareRejectsLegacySuggestion(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	for _, offer := range mobileTestOffers(t, bridge) {
		if offer.Kind == "suggestion" {
			if _, err := bridge.app.PrepareTaskSuggestion(offer.ID, offer.Version); err == nil {
				t.Fatal("prepared legacy suggestion")
			}
			return
		}
	}
	t.Fatal("missing legacy suggestion")
}

func TestTaskOfferPrepareRejectsStaleAndUnavailable(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	var offer mobileOffer
	for _, candidate := range mobileTestOffers(t, bridge) {
		if candidate.Kind == "topic_find" {
			offer = candidate
			break
		}
	}
	if offer.ID == "" {
		t.Fatal("missing topic find")
	}
	for _, ref := range []TaskOfferReference{{ID: offer.ID, Version: "stale"}, {ID: "missing", Version: offer.Version}, {}} {
		if _, err := bridge.app.PrepareTaskSuggestion(ref.ID, ref.Version); err == nil {
			t.Fatalf("prepared invalid reference: %+v", ref)
		}
	}
	if _, err := (&App{}).PrepareTaskSuggestion(offer.ID, offer.Version); err == nil {
		t.Fatal("prepared with no store")
	}
	bridge.app.closedEyes.enabled.Store(true)
	if _, err := bridge.app.PrepareTaskSuggestion(offer.ID, offer.Version); err == nil {
		t.Fatal("prepared a withdrawn offer")
	}
}

func TestTaskOfferSendRejectsBeforeHistoryAndRunner(t *testing.T) {
	for _, change := range []string{"version", "withdrawal", "missing refs"} {
		t.Run(change, func(t *testing.T) {
			app, tag, draft := taskOfferChatFixture(t)
			refs := []TaskOfferReference{draft.Reference}
			switch change {
			case "version":
				refs[0].Version = "stale"
			case "withdrawal":
				app.closedEyes.enabled.Store(true)
			case "missing refs":
				refs = nil
			}
			runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			app.runner = runner
			_, err := app.SendPromptWithOfferReferences(draft.Prompt+" edited", t.TempDir(), "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "offer-user", Kind: "user"}, refs)
			if err == nil || err.Error() != "TASK_OFFER_UNAVAILABLE" {
				t.Fatalf("send error = %v", err)
			}
			page, err := app.store.LoadStreamItemsLatest(context.Background(), tag, 100)
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("rejected turn persisted: %+v %v", page, err)
			}
			select {
			case <-runner.inputs:
				t.Fatal("rejected turn started a runner")
			default:
			}
		})
	}
}

func TestTaskOfferSendPreservesEditedSourceProvenance(t *testing.T) {
	app, tag, draft := taskOfferChatFixture(t)
	if app.isMainChatSession(t.Context(), tag) {
		t.Fatal("fixture must be a normal chat")
	}
	page, err := app.store.LoadStreamItemsLatest(t.Context(), tag, 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("preparation sent a message")
	}
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	app.runner = runner
	text := draft.Prompt + " Focus on the smallest next step."
	_, err = app.SendPromptWithOfferReferences(text, t.TempDir(), "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "offer-user", Kind: "user"}, []TaskOfferReference{draft.Reference})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.inputs:
	case <-time.After(10 * time.Second):
		t.Fatal("admitted turn did not reach runner")
	}
	waitForTagIdle(t, app, tag)
	page, err = app.store.LoadStreamItemsLatest(t.Context(), tag, 100)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("missing admitted row: %v", err)
	}
	row := page.Items[0]
	var provenance mobileOfferChatSources
	if err := json.Unmarshal([]byte(row.AutomationContext), &provenance); err != nil {
		t.Fatal(err)
	}
	if row.Content != text || len(provenance.OfferSources) != 1 || provenance.OfferReferences[0].ID != draft.Reference.ID {
		t.Fatalf("provenance lost: %+v %+v", row, provenance)
	}
	app.closedEyes.enabled.Store(true)
	if got := app.mobileOfferHistoryRows(t.Context(), []storage.StreamItemRow{row}); len(got) != 0 {
		t.Fatal("withdrawn edited source remained in history")
	}
}

func TestTaskOfferSendRejectsWithdrawalDuringPublication(t *testing.T) {
	app, tag, draft := taskOfferChatFixture(t)
	prior := mobileChatOfferCommitHook
	t.Cleanup(func() { mobileChatOfferCommitHook = prior })
	mobileChatOfferCommitHook = func() { app.closedEyes.enabled.Store(true) }
	_, err := app.SendPromptWithOfferReferences(draft.Prompt, t.TempDir(), "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "offer-user", Kind: "user"}, []TaskOfferReference{draft.Reference})
	if err == nil || err.Error() != "TASK_OFFER_UNAVAILABLE" {
		t.Fatalf("commit withdrawal error = %v", err)
	}
	page, err := app.store.LoadStreamItemsLatest(t.Context(), tag, 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("publication raced withdrawal: %+v %v", page, err)
	}
}
