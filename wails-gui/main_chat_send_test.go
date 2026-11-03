package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"

	"github.com/posthog/posthog-go"
)

type panelAskCaptureRunner struct {
	*topicCaptureRunner
	inputs chan *agent.TaskInput
}

func (r *panelAskCaptureRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	r.inputs <- input
	return r.topicCaptureRunner.ExecuteTask(ctx, input, emit)
}

func TestPanelAsksCannotUseActionTools(t *testing.T) {
	a, tag := topicChatTestApp(t)
	r := &panelAskCaptureRunner{topicCaptureRunner: &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}, inputs: make(chan *agent.TaskInput, 1)}
	a.runner = r
	for _, send := range []func() (MainChatTopicSendResult, error){
		func() (MainChatTopicSendResult, error) { return a.AskMainChat("What happened?") },
		func() (MainChatTopicSendResult, error) {
			return a.AskMainChatWithContext("What happened?", TodayChatContextRef{SourceKind: todayChatPausedKind, SourceID: "observation"})
		},
		func() (MainChatTopicSendResult, error) {
			return a.AskMainChatWithContext("What happened?", TodayChatContextRef{SourceKind: "topic", SourceID: "atlas"})
		},
	} {
		if _, err := send(); err != nil {
			t.Fatal(err)
		}
		select {
		case input := <-r.inputs:
			if !input.NoTools || input.ProgrammaticToolCalling || len(input.ExtraTools) != 0 {
				t.Fatalf("panel Ask acquired tools: noTools=%v programmatic=%v extra=%d", input.NoTools, input.ProgrammaticToolCalling, len(input.ExtraTools))
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runner not called")
		}
		<-r.prompt
		waitForTagIdle(t, a, tag)
	}
	if _, err := a.AskMainChatWithContext("What happened?", TodayChatContextRef{}); err == nil {
		t.Fatal("a source-bound Ask must reject a missing source")
	}
}

// A plain send from Today's panel lands in the main chat as the user's own
// words, with no topic tag and no topic context in the prompt.
func TestSendMainChatCarriesNoTopicOrContext(t *testing.T) {
	a, tag := topicChatTestApp(t)
	r := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = r
	sent, err := a.SendMainChat("Plain words for the main chat")
	if err != nil {
		t.Fatal(err)
	}
	if sent.SessionID != tag || sent.UserItemID == "" || sent.SortOrder < 0 {
		t.Fatalf("sent=%+v", sent)
	}
	select {
	case prompt := <-r.prompt:
		if !strings.Contains(prompt, "Plain words for the main chat") || strings.Contains(prompt, "Review ready") || strings.Contains(prompt, "Ship") {
			t.Fatalf("prompt carried topic context: %s", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner not called")
	}
	waitForTagIdle(t, a, tag)
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == sent.UserItemID {
			found = true
			if row.Kind != "user" || row.Content != "Plain words for the main chat" || row.TopicID != "" {
				t.Fatalf("bubble=%+v", row)
			}
		}
	}
	if !found {
		t.Fatal("missing user row")
	}
	page, err := a.ListMainChatTopicItems("atlas", "", 50)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("a plain send must not show under a topic: page=%+v err=%v", page, err)
	}
	if _, err := a.SendMainChat("   "); err == nil {
		t.Fatal("empty words must be refused")
	}
}

// An answer prepared from the brief goes with its Today item: a paused draft
// is admitted with no sources read, and an item of no kind is refused.
func TestSendMainChatWithContextChecksTheItem(t *testing.T) {
	a, tag := topicChatTestApp(t)
	r := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = r
	if _, err := a.SendMainChatWithContext("Hold Thursday", TodayChatContextRef{}); err == nil {
		t.Fatal("an item of no kind must be refused")
	}
	sent, err := a.SendMainChatWithContext("Hold Thursday", TodayChatContextRef{SourceKind: todayChatPausedKind, SourceID: "observation"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case prompt := <-r.prompt:
		if !strings.Contains(prompt, "Hold Thursday") || strings.Contains(prompt, "Review ready") {
			t.Fatalf("prompt=%s", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner not called")
	}
	waitForTagIdle(t, a, tag)
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == sent.UserItemID {
			found = true
			if row.Content != "Hold Thursday" || row.TopicID != "" {
				t.Fatalf("bubble=%+v", row)
			}
		}
	}
	if !found {
		t.Fatal("missing user row")
	}
	// A topic item takes the topic path, tagged like a topic send.
	topicSent, err := a.SendMainChatWithContext("Topic words", TodayChatContextRef{SourceKind: "topic", SourceID: "atlas"})
	if err != nil {
		t.Fatal(err)
	}
	<-r.prompt
	waitForTagIdle(t, a, tag)
	page, err := a.ListMainChatTopicItems("atlas", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	tagged := false
	for _, item := range page.Items {
		if item.Message != nil && item.Message.ID == topicSent.UserItemID {
			tagged = true
		}
	}
	if !tagged {
		t.Fatalf("topic words not under the topic: page=%+v", page)
	}
}

func TestTodayChipAndTypedTurnTelemetry(t *testing.T) {
	a, tag := topicChatTestApp(t)
	telemetry, client := newRecordingTelemetry(t)
	a.telemetry = telemetry
	runner := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 2)}
	a.runner = runner
	if _, err := a.AskMainChatWithContext("private brief words", TodayChatContextRef{SourceKind: todayChatPausedKind, SourceID: "observation", TelemetryAction: "T10"}); err != nil {
		t.Fatal(err)
	}
	<-runner.prompt
	waitForTagIdle(t, a, tag)
	if _, err := a.SendMainChat("private typed words"); err != nil {
		t.Fatal(err)
	}
	<-runner.prompt
	waitForTagIdle(t, a, tag)
	if _, err := a.SendMainChatFromTodayChip("private answer chip"); err != nil {
		t.Fatal(err)
	}
	<-runner.prompt
	waitForTagIdle(t, a, tag)
	client.mu.Lock()
	defer client.mu.Unlock()
	var turns []posthog.Capture
	for _, message := range client.messages {
		if event, ok := message.(posthog.Capture); ok && event.Event == "chat.turn_sent" {
			turns = append(turns, event)
		}
	}
	if len(turns) != 3 {
		t.Fatalf("turn count=%d", len(turns))
	}
	source, ok := turns[0].Properties["source"].(map[string]any)
	if !ok || source["surface"] != "today" || source["action"] != "T10" {
		t.Fatalf("chip source=%#v", turns[0].Properties["source"])
	}
	if _, ok := turns[1].Properties["source"]; ok {
		t.Fatalf("typed source=%#v", turns[1].Properties["source"])
	}
	chipSource, ok := turns[2].Properties["source"].(map[string]any)
	if !ok || chipSource["surface"] != "today" || chipSource["action"] != "T68" {
		t.Fatalf("answer chip source=%#v", turns[2].Properties["source"])
	}
	for _, turn := range turns {
		if strings.Contains(fmt.Sprint(turn.Properties), "private") {
			t.Fatalf("private content in telemetry: %#v", turn.Properties)
		}
	}
}

// The Today panel's box attaches like the Chat page's (chat-review v1.0.3
// #4): the model reads the file's text, the bubble keeps only the words.
func TestSendMainChatWithAttachmentsReadsTheFile(t *testing.T) {
	a, tag := topicChatTestApp(t)
	r := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = r
	file := FileData{FileName: "pt-notes.txt", MediaType: "text/plain", DataBase64: base64.StdEncoding.EncodeToString([]byte("Stretch twice a day")), FileSize: 19}
	sent, err := a.SendMainChatWithAttachments("What does this say?", nil, []FileData{file})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case prompt := <-r.prompt:
		if !strings.Contains(prompt, "<attached-file") || !strings.Contains(prompt, "Stretch twice a day") || !strings.Contains(prompt, "What does this say?") {
			t.Fatalf("prompt lacks the attachment: %s", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner not called")
	}
	waitForTagIdle(t, a, tag)
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == sent.UserItemID && row.Content != "What does this say?" {
			t.Fatalf("bubble=%q", row.Content)
		}
	}
}
