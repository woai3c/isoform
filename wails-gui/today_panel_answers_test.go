package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

type panelAnswerStreamRunner struct {
	upcomingCaptureRunner
	app *App
	tag string
}

func (r *panelAnswerStreamRunner) ExecuteTask(_ context.Context, _ *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	if err := emit(&agent.StreamEvent{Type: "run_created", RunID: "panel-run"}); err != nil {
		return "", err
	}
	if err := emit(&agent.StreamEvent{Type: "assistant", RunID: "panel-run", Content: "A saved response."}); err != nil {
		return "", err
	}
	segment := r.app.answerChipSegment(r.tag)
	if segment == nil {
		return "", errTodayChatItemUnavailable
	}
	if _, err := r.app.store.AppendStreamItem(context.Background(), r.tag, storage.StreamItemRow{ID: segment.id, Kind: "assistant", RunID: "panel-run", Content: segment.content}); err != nil {
		return "", err
	}
	return "panel-run", nil
}

func TestPanelAskSavesOnlyAfterTheReplyIsDurable(t *testing.T) {
	rig, _, brief := cardRig(t)
	app := rig.app
	app.activeTags, app.eventBroadcaster = map[string]bool{}, NewEventBroadcaster()
	app.tagCancels, app.sessionTempDirs = map[string]context.CancelFunc{}, map[string]string{}
	app.streams, app.runIDByTag, app.pendingTraceIDs = map[string]context.CancelFunc{}, map[string]string{}, map[string]string{}
	view, err := app.GetLatestBrief()
	if err != nil || view.Brief == nil {
		t.Fatalf("brief: %v", err)
	}
	card := cardFor(t, *view.Brief, "Release memo")
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID, GeneratedMessage: card.Message}
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app.runner = &panelAnswerStreamRunner{app: app, tag: main.LocalID}
	if _, err := app.AskMainChatWithContext(card.Message, ref); err != nil {
		t.Fatal(err)
	}
	// A second click immediately after admission waits for the durable
	// answer instead of seeing an empty pointer and starting a second Ask.
	answer, err := app.GetTodayPanelAnswer(ref)
	if err != nil || answer == nil || answer.Answer != "A saved response." {
		t.Fatalf("answer=%+v err=%v", answer, err)
	}
}

func TestPanelAnswerReopensChatRowsAndWithdrawsWithSource(t *testing.T) {
	rig, _, brief := cardRig(t)
	app := rig.app
	view, err := app.GetLatestBrief()
	if err != nil || view.Brief == nil {
		t.Fatalf("brief: %v", err)
	}
	card := cardFor(t, *view.Brief, "Release memo")
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID, GeneratedMessage: card.Message}
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = app.admitTodayChatTurn(t.Context(), main.LocalID, storage.StreamItemRow{ID: "panel-user", Kind: "user", Content: card.Message}, ref, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.BindAnswerChipUserRun(t.Context(), main.LocalID, "panel-user", "panel-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "panel-answer", Kind: "assistant", RunID: "panel-run", Content: "Here is the answer."}); err != nil {
		t.Fatal(err)
	}
	app.savePanelAskAnswer(main.LocalID, "panel-user", "panel-run", "panel-answer", "Here is the answer.")
	got, err := app.GetTodayPanelAnswer(ref)
	if err != nil || got == nil || got.Question != card.Message || got.Answer != "Here is the answer." {
		t.Fatalf("saved answer: %+v %v", got, err)
	}
	if err := app.handoffStore.Delete("memo"); err != nil {
		t.Fatal(err)
	}
	got, err = app.GetTodayPanelAnswer(ref)
	if err != nil || got != nil {
		t.Fatalf("withdrawn source exposed answer: %+v %v", got, err)
	}
}

func TestPanelAnswerWaitIsReleasedOnCancellationAndReplacement(t *testing.T) {
	app := &App{}
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: "card", BriefID: "brief"}
	app.beginPanelAskAnswer(ref, "first")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	completed := make(chan error, 1)
	go func() { completed <- app.waitForPanelAnswerSave(ctx, ref) }()
	app.beginPanelAskAnswer(ref, "second")
	app.cancelPanelAskAnswer("first")
	app.cancelPanelAskAnswer("second")
	if err := <-completed; err != nil {
		t.Fatalf("wait remained blocked: %v", err)
	}
}

func TestBriefDraftUsesItsOwnSourceCheckedAnswer(t *testing.T) {
	rig, _, brief := cardRig(t)
	app := rig.app
	view, err := app.GetLatestBrief()
	if err != nil || view.Brief == nil {
		t.Fatalf("brief: %v", err)
	}
	card := cardFor(t, *view.Brief, "Release memo")
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID, GeneratedMessage: card.Message, BriefDraft: true}
	result, err := app.buildTodayChatContext(t.Context(), ref)
	if err != nil || !strings.Contains(result.Text, "Write only the requested draft") {
		t.Fatalf("draft context=%q err=%v", result.Text, err)
	}
	ask := ref
	ask.BriefDraft = false
	if panelAnswerKey(ref) == panelAnswerKey(ask) {
		t.Fatal("a draft shared the saved answer for an Ask")
	}
	ref.SourceKind = "calendar"
	if todayChatRefWithinBounds(ref) {
		t.Fatal("accepted a brief draft without a brief card")
	}
}
