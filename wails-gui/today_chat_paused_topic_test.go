package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPausedTopicUsesOnlyProvenFrozenRegistrySnapshot(t *testing.T) {
	a, _, now := registryChatTestAppAt(t, time.Now().Add(-time.Minute))
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v, %v", views, err)
	}
	ref := TodayChatContextRef{SourceKind: "topic", SourceID: views[0].ID}
	a.saveObservationPauses([]observationPause{{Start: now.Add(time.Second).UnixMilli()}})
	a.closedEyes.enabled.Store(true)
	ctx := context.WithValue(t.Context(), pausedTopicReadKey{}, true)
	item, sources, err := a.loadTodayChatContextItem(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	result := buildTodayChatContextSections(item, sources)
	if !strings.Contains(result.Text, "Atlas") || !strings.Contains(result.Text, "frozen topic") || result.Counts.Activities != 0 || result.Counts.Memories != 0 {
		t.Fatalf("wrong paused context: %+v", result)
	}
	if _, _, err := a.loadTodayChatContextItem(t.Context(), ref); err != errTodayChatObservationPaused {
		t.Fatalf("ordinary read bypassed pause: %v", err)
	}
	metadata, err := a.topicChatAdmissionMetadataFromSources(ctx, item.topicRefs, sources)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadata), &fields); err != nil {
		t.Fatal(err)
	}
	raw := string(fields["topicContext"])
	binding := &topicTurnBinding{}
	binding.pausedAt.Store(now.Add(time.Second).UnixMilli())
	proofCtx := context.WithValue(t.Context(), topicTurnContextKey{}, binding)
	if !a.topicChatProofResults(proofCtx, map[string]bool{raw: true})[raw] {
		t.Fatal("the admitted source proof could not be revalidated")
	}
	if err := a.activity.store.DeleteSession("session-atlas"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.loadTodayChatContextItem(ctx, ref); err == nil {
		t.Fatal("forgotten source authorized a frozen topic")
	}
	if a.topicChatProofResults(proofCtx, map[string]bool{raw: true})[raw] {
		t.Fatal("forgotten source authorized the running turn")
	}
}

func TestPausedTopicRequiresRetainedPauseAfterSnapshot(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v, %v", views, err)
	}
	a.closedEyes.enabled.Store(true)
	ctx := context.WithValue(t.Context(), pausedTopicReadKey{}, true)
	ref := TodayChatContextRef{SourceKind: "topic", SourceID: views[0].ID}
	for _, pauses := range [][]observationPause{nil, {{Start: now.Add(-time.Second).UnixMilli()}}} {
		a.saveObservationPauses(pauses)
		if _, _, err := a.loadTodayChatContextItem(ctx, ref); err == nil {
			t.Fatalf("accepted snapshot without a later active pause: %+v", pauses)
		}
	}
}

func TestPausedTopicSendStaysInTopicAndPhoneRemainsPaused(t *testing.T) {
	a, tag, now := registryChatTestAppAt(t, time.Now().Add(-time.Minute))
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v, %v", views, err)
	}
	id := views[0].ID
	a.saveObservationPauses([]observationPause{{Start: now.Add(time.Second).UnixMilli()}})
	a.closedEyes.enabled.Store(true)
	runner := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = runner
	if _, err := a.sendMainChatWithTopic("Phone question", id, promptOriginPhone, nil); err == nil {
		t.Fatal("phone bypassed its pause policy")
	}
	sent, err := a.SendMainChatWithTopic("What happened?", id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case prompt := <-runner.prompt:
		if !strings.Contains(prompt, "Atlas") || !strings.Contains(prompt, "frozen topic") {
			t.Fatalf("missing frozen topic context: %s", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner not called")
	}
	waitForTagIdle(t, a, tag)
	page, err := a.ListMainChatTopicItems(id, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.Message != nil && item.Message.ID == sent.UserItemID {
			found = true
		}
	}
	if !found {
		t.Fatalf("sent message absent from topic: %+v", page)
	}
	if err := a.activity.store.DeleteSession("session-atlas"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ListMainChatTopicItems(id, "", 50); err == nil {
		t.Fatal("paused topic page survived withdrawal")
	}
}
