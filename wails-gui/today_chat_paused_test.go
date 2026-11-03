package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

// While observation is paused (Closed Eyes), Today chat reads no sources:
// every item reference is refused, the batch prepares nothing, and only the
// paused reference, which attaches no observed context, reaches the model.
func TestTodayChatReadsNothingWhileObservationIsPaused(t *testing.T) {
	app, tag, ref := todaySendTestApp(t)
	if _, err := app.PrepareTodayChatDraft(ref); err != nil {
		t.Fatalf("readable control: %v", err)
	}
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })

	if _, err := app.PrepareTodayChatDraft(ref); !errors.Is(err, errTodayChatObservationPaused) {
		t.Fatalf("item read while paused: %v", err)
	}
	for i, result := range app.PrepareTodayChatDrafts([]TodayChatContextRef{ref, {SourceKind: "brief", SourceID: "revision-1", BriefID: "brief-1"}}) {
		if result.Draft != nil || result.Error != "paused" {
			t.Fatalf("batch result %d while paused = %+v", i, result)
		}
	}
	if _, err := app.PrepareTodayChatTurn(tag, "Help with the launch plan.", ref); err == nil {
		t.Fatal("item turn prepared while paused")
	}

	paused := TodayChatContextRef{SourceKind: todayChatPausedKind, SourceID: "observation"}
	got, err := app.buildTodayChatContext(context.Background(), paused)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "Observation is paused") || !strings.Contains(got.Text, "Do not look up their activity") || strings.Contains(got.Text, "release review") || got.Counts != (TodayChatContextCounts{}) {
		t.Fatalf("paused context: %+v\n%s", got.Counts, got.Text)
	}

	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	app.runner = runner
	app.tagCancels = map[string]context.CancelFunc{}
	app.sessionTempDirs = map[string]string{}
	app.streams = map[string]context.CancelFunc{}
	app.runIDByTag = map[string]string{}
	app.pendingTraceIDs = map[string]string{}
	const visible = "Help me decide what to do next about this item."
	if _, err := app.SendPromptWithTodayContext(visible, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "paused-user", Kind: "user"}, paused); err != nil {
		t.Fatal(err)
	}
	select {
	case input := <-runner.inputs:
		prompt, _ := input.PromptFunc()
		if !strings.Contains(prompt, "observation is paused") || strings.Contains(prompt, "release review") || strings.Contains(prompt, "<memory>") || strings.Contains(prompt, "<recent-activity-context>") || !strings.HasSuffix(prompt, visible) {
			t.Fatalf("paused turn prompt:\n%s", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("paused turn did not reach the model")
	}
	waitForTagIdle(t, app, tag)
	page, err := app.store.LoadStreamItemsLatest(context.Background(), tag, 100)
	if err != nil {
		t.Fatal(err)
	}
	admitted := false
	for _, row := range page.Items {
		if row.ToolName == "today_context" {
			t.Fatal("a paused turn posted a context receipt")
		}
		admitted = admitted || row.ID == "paused-user"
	}
	if !admitted {
		t.Fatal("paused turn was not admitted to history")
	}

	// Resumed: items prepare as usual again.
	app.closedEyes.enabled.Store(false)
	if _, err := app.PrepareTodayChatDraft(ref); err != nil {
		t.Fatalf("item stayed refused after resume: %v", err)
	}
}
