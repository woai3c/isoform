package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/openanswer"
	"wails-gui/internal/pii"
	"wails-gui/storage"
	"yansu-agent/agent"
)

func TestOpenAnswerStreamNeverEmitsStructuredBlock(t *testing.T) {
	input := `The answer.<open_answer>{"question":"private"}</open_answer> Done.`
	for split := 0; split <= len(input); split++ {
		app := &App{}
		var heard []AgentEvent
		app.onAgentEvent(func(evt AgentEvent) { heard = append(heard, evt) })
		var stream openAnswerStream
		stream.emit(AgentEvent{Type: "assistant", Tag: "main", Content: input[:split]}, app.emitAgentEvent)
		stream.emit(AgentEvent{Type: "assistant", Tag: "main", Content: input[split:]}, app.emitAgentEvent)
		stream.emit(AgentEvent{Type: "done", Tag: "main"}, app.emitAgentEvent)
		var text strings.Builder
		for _, evt := range heard {
			if evt.Type == "assistant" {
				text.WriteString(evt.Content)
			}
		}
		if text.String() != "The answer. Done." {
			t.Fatalf("split %d: %q", split, text.String())
		}
	}
}

func TestOpenAnswerStreamNestedBlockAcrossToolBoundary(t *testing.T) {
	block := `<open_answer>outer<open_answer>inner</open_answer>private</open_answer>`
	for split := 1; split < len(block); split++ {
		var stream openAnswerStream
		var visible strings.Builder
		emit := func(evt AgentEvent) {
			if evt.Type == "assistant" {
				visible.WriteString(evt.Content)
			}
		}
		stream.emit(AgentEvent{Type: "assistant", Content: "Answer." + block[:split]}, emit)
		stream.emit(AgentEvent{Type: "tool_result"}, emit)
		stream.emit(AgentEvent{Type: "assistant", Content: block[split:] + " Done."}, emit)
		stream.emit(AgentEvent{Type: "done"}, emit)
		if got := visible.String(); got != "Answer. Done." {
			t.Fatalf("split %d: visible %q", split, got)
		}
		if stream.completedCandidate() != nil {
			t.Fatalf("split %d: malformed block retained", split)
		}
	}
}

func TestOpenAnswerStreamCandidatePrivacyFilter(t *testing.T) {
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	for _, tc := range []struct {
		name, question, topic, source, event string
		want                                 bool
	}{
		{"ordinary", "Did Priya reply?", "Replies", "Mail", "thread", true},
		{"sensitive source name", "Did Priya reply?", "Replies", "Diary", "thread", false},
		{"sensitive source bundle", "Did Priya reply?", "Replies", "com.example.diary", "thread", false},
		{"question contains email", "Did priya@example.com reply?", "Replies", "Mail", "thread", false},
		{"topic contains code", "Did Priya reply?", "code 482913", "Mail", "thread", false},
		{"withheld question", "Did [REDACTED:email] reply?", "Replies", "Mail", "thread", false},
		{"event contains email", "Did Priya reply?", "Replies", "Mail", "priya@example.com", false},
		{"event contains code", "Did Priya reply?", "Replies", "Mail", "code 482913", false},
		{"event contains redaction", "Did Priya reply?", "Replies", "Mail", "[REDACTED:email]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, err := json.Marshal(openanswer.Block{Question: tc.question, Topic: tc.topic, Source: tc.source, Settles: "reply", NotYet: true, WatchedEvent: tc.event})
			if err != nil {
				t.Fatal(err)
			}
			stream := openAnswerStream{question: tc.question}
			stream.emit(AgentEvent{Type: "assistant", Content: "Not yet. <open_answer>" + string(block) + "</open_answer>"}, func(AgentEvent) {})
			stream.emit(AgentEvent{Type: "done"}, func(AgentEvent) {})
			if got := stream.completedCandidate() != nil; got != tc.want {
				t.Fatalf("candidate retained = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOpenAnswerStreamRechecksCompletedCandidate(t *testing.T) {
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{})
	now := time.Now()
	block := openanswer.Block{Question: "When is the result?", Topic: "Result", Source: "Results", Settles: "result", NotYet: true,
		WindowStart: now.Add(time.Minute).Format(time.RFC3339), WindowEnd: now.Add(2 * time.Minute).Format(time.RFC3339)}
	raw, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	stream := openAnswerStream{question: block.Question}
	stream.emit(AgentEvent{Type: "assistant", Content: "Not yet. <open_answer>" + string(raw) + "</open_answer>"}, func(AgentEvent) {})
	stream.emit(AgentEvent{Type: "done"}, func(AgentEvent) {})
	first := stream.completedCandidateAt(now)
	if first == nil {
		t.Fatal("expected initially eligible candidate")
	}
	first.Source = "changed by caller"
	if got := stream.completedCandidateAt(now); got == nil || got.Source != block.Source {
		t.Fatalf("caller modified retained candidate: %+v", got)
	}
	if got := stream.completedCandidateAt(now.Add(3 * time.Minute)); got != nil {
		t.Fatalf("expired candidate retained: %+v", got)
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.results": "Results"})
	if got := stream.completedCandidateAt(now); got != nil {
		t.Fatalf("newly protected source retained: %+v", got)
	}
}

func TestOpenAnswerStreamRejectsIneligibleCandidates(t *testing.T) {
	now := time.Now()
	base := openanswer.Block{Question: "When is the result?", Topic: "Result", Source: "Results", Settles: "result", NotYet: true,
		WindowStart: now.Add(time.Hour).Format(time.RFC3339), WindowEnd: now.Add(2 * time.Hour).Format(time.RFC3339)}
	for _, tc := range []struct {
		name   string
		change func(*openanswer.Block)
		want   bool
	}{
		{"valid shape", func(*openanswer.Block) {}, true},
		{"complete answer", func(b *openanswer.Block) { b.NotYet = false }, false},
		{"missing source", func(b *openanswer.Block) { b.Source = "" }, false},
		{"invalid kind", func(b *openanswer.Block) { b.Settles = "forecast" }, false},
		{"two mechanisms", func(b *openanswer.Block) { b.WatchedEvent = "event" }, false},
		{"expired", func(b *openanswer.Block) {
			b.WindowStart = now.Add(-2 * time.Hour).Format(time.RFC3339)
			b.WindowEnd = now.Add(-time.Hour).Format(time.RFC3339)
		}, false},
		{"beyond seven days", func(b *openanswer.Block) {
			b.WindowStart = now.Add(8 * 24 * time.Hour).Format(time.RFC3339)
			b.WindowEnd = b.WindowStart
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := base
			tc.change(&block)
			raw, err := json.Marshal(block)
			if err != nil {
				t.Fatal(err)
			}
			stream := openAnswerStream{question: block.Question}
			stream.emit(AgentEvent{Type: "assistant", Content: "Not yet. <open_answer>" + string(raw) + "</open_answer>"}, func(AgentEvent) {})
			stream.emit(AgentEvent{Type: "done"}, func(AgentEvent) {})
			if got := stream.completedCandidate() != nil; got != tc.want {
				t.Fatalf("candidate retained = %v, want %v", got, tc.want)
			}
		})
	}
}

type openAnswerChatRunner struct{ upcomingCaptureRunner }

func (r *openAnswerChatRunner) ExecuteTask(_ context.Context, _ *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	for _, event := range []*agent.StreamEvent{
		{Type: "run_created", RunID: "answer-run"},
		{Type: "assistant", RunID: "answer-run", Content: "Visible<open_ans"},
		{Type: "assistant", RunID: "answer-run", Content: `wer>{"question":"private"}</open_answer> text`},
		{Type: "done", RunID: "answer-run"},
	} {
		if err := emit(event); err != nil {
			return "answer-run", err
		}
	}
	return "answer-run", nil
}

// A turn need not qualify for answer chips to need the structured-output
// boundary. Check both what renderers hear and what the segment writer saves.
func TestOpenAnswerStreamFiltersMainChatWithoutChipSeed(t *testing.T) {
	a, tag := topicChatTestApp(t)
	a.runner = &openAnswerChatRunner{}
	events := make(chan AgentEvent, 8)
	writeErrors := make(chan error, 1)
	inserted := false
	a.onAgentEvent(func(evt AgentEvent) {
		if evt.Type == "assistant" && !inserted {
			inserted = true
			_, err := a.store.AppendStreamItem(context.Background(), tag, storage.StreamItemRow{ID: evt.ItemID, Kind: "assistant", RunID: evt.RunID, Content: evt.Content})
			writeErrors <- err
		}
		events <- evt
	})
	// No matching user row: answerChipUser must decline this turn.
	if err := a.SendPrompt("A new question", "", "m", "", tag, false, false, "", "", "", "en", ""); err != nil {
		t.Fatal(err)
	}
	var visible strings.Builder
	var itemID string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Type == "assistant" {
				visible.WriteString(evt.Content)
				itemID = evt.ItemID
			}
			if evt.Type == "done" {
				if err := <-writeErrors; err != nil {
					t.Fatal(err)
				}
				a.flushAgentSegmentWrites(false)
				row, err := a.store.LoadStreamItem(t.Context(), itemID)
				if err != nil || row == nil || row.Content != "Visible text" || visible.String() != "Visible text" {
					t.Fatalf("visible=%q row=%+v err=%v", visible.String(), row, err)
				}
				return
			}
		case <-deadline:
			t.Fatal("chat did not finish")
		}
	}
}

func TestOpenAnswerStreamFlushesAtToolBoundary(t *testing.T) {
	var stream openAnswerStream
	var events []AgentEvent
	emit := func(evt AgentEvent) { events = append(events, evt) }
	stream.emit(AgentEvent{Type: "assistant", Content: "Text<open_answer>secret</open_answer>"}, emit)
	stream.emit(AgentEvent{Type: "tool_use_start"}, emit)
	stream.emit(AgentEvent{Type: "assistant", Content: "Next"}, emit)
	stream.emit(AgentEvent{Type: "done"}, emit)
	if len(events) != 4 || events[0].Content != "Text" || events[1].Type != "tool_use_start" || events[2].Content != "Next" || events[3].Type != "done" {
		t.Fatalf("events = %+v", events)
	}
}

func TestOpenAnswerStreamToolBoundaryDoesNotRevealBlockContinuation(t *testing.T) {
	block := `<open_answer>{"question":"Did Priya reply?","topic":"replies","settles":"reply","source":"Mail","notYet":true,"watchedEvent":"thread"}</open_answer>`
	for split := 1; split < len(block); split++ {
		var stream openAnswerStream
		stream.question = "Did Priya reply?"
		var visible strings.Builder
		emit := func(evt AgentEvent) {
			if evt.Type == "assistant" {
				visible.WriteString(evt.Content)
			}
		}
		stream.emit(AgentEvent{Type: "assistant", Content: "Not yet. " + block[:split]}, emit)
		stream.emit(AgentEvent{Type: "tool_use_start"}, emit)
		stream.emit(AgentEvent{Type: "tool_use"}, emit)
		stream.emit(AgentEvent{Type: "assistant", Content: block[split:] + " Done."}, emit)
		stream.emit(AgentEvent{Type: "done"}, emit)
		if got := visible.String(); got != "Not yet.  Done." {
			t.Fatalf("split %d: visible %q", split, got)
		}
		if stream.completedCandidate() != nil {
			t.Fatalf("split %d: interrupted block retained", split)
		}
	}
}

func TestOpenAnswerStreamToolResultInterruptsCandidate(t *testing.T) {
	block := `<open_answer>{"question":"Did Priya reply?","topic":"replies","settles":"reply","source":"Mail","notYet":true,"watchedEvent":"thread"}</open_answer>`
	for split := 1; split < len(block); split++ {
		stream := openAnswerStream{question: "Did Priya reply?"}
		var visible strings.Builder
		emit := func(evt AgentEvent) {
			if evt.Type == "assistant" {
				visible.WriteString(evt.Content)
			}
		}
		stream.emit(AgentEvent{Type: "assistant", Content: "Not yet. " + block[:split]}, emit)
		stream.emit(AgentEvent{Type: "tool_result"}, emit)
		stream.emit(AgentEvent{Type: "assistant", Content: block[split:]}, emit)
		stream.emit(AgentEvent{Type: "done"}, emit)
		if got := visible.String(); got != "Not yet. " {
			t.Fatalf("split %d: visible %q", split, got)
		}
		if stream.completedCandidate() != nil {
			t.Fatalf("split %d: interrupted block retained", split)
		}
	}
}

func TestOpenAnswerStreamUnclosedBlockAcrossToolBoundary(t *testing.T) {
	var stream openAnswerStream
	var visible strings.Builder
	emit := func(evt AgentEvent) {
		if evt.Type == "assistant" {
			visible.WriteString(evt.Content)
		}
	}
	stream.emit(AgentEvent{Type: "assistant", Content: "Visible<open_answer>private"}, emit)
	stream.emit(AgentEvent{Type: "tool_use_start"}, emit)
	stream.emit(AgentEvent{Type: "assistant", Content: "more private"}, emit)
	stream.emit(AgentEvent{Type: "done"}, emit)
	if got := visible.String(); got != "Visible" {
		t.Fatalf("visible %q", got)
	}
}

func TestOpenAnswerStreamDiscardsPartialOpeningAtEnd(t *testing.T) {
	for _, suffix := range []string{"<", "<op", "<open", "<open_ans"} {
		var stream openAnswerStream
		var visible strings.Builder
		emit := func(evt AgentEvent) {
			if evt.Type == "assistant" {
				visible.WriteString(evt.Content)
			}
		}
		stream.emit(AgentEvent{Type: "assistant", Content: "Answer." + suffix}, emit)
		stream.emit(AgentEvent{Type: "done"}, emit)
		if got := visible.String(); got != "Answer." {
			t.Fatalf("suffix %q: visible %q", suffix, got)
		}
	}
}

func TestOpenAnswerStreamCandidateBoundToCompletedTurn(t *testing.T) {
	const valid = `<open_answer>{"question":"Did Priya reply?","topic":"replies","settles":"reply","source":"Mail","notYet":true,"watchedEvent":"thread"}</open_answer>`
	for _, tc := range []struct {
		name     string
		question string
		chunks   []AgentEvent
		want     bool
	}{
		{"bound", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid + " \n"}, {Type: "done"}, {Type: "close"}}, true},
		{"no answer", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: valid}, {Type: "done"}}, false},
		{"block before answer", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: valid + "Not yet."}, {Type: "done"}}, false},
		{"text after block", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "assistant", Content: " More text."}, {Type: "done"}}, false},
		{"partial delimiter after block", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid + "<"}, {Type: "done"}}, false},
		{"assistant after done", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "done"}, {Type: "assistant", Content: "More."}}, false},
		{"wrong question", "Another question", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "done"}}, false},
		{"case alias overwrites question", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: `Not yet. <open_answer>{"question":"Another question","Question":"Did Priya reply?","topic":"replies","settles":"reply","source":"Mail","notYet":true,"watchedEvent":"thread"}</open_answer>`}, {Type: "done"}}, false},
		{"no admitted turn", "", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "done"}}, false},
		{"duplicate", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid + valid}, {Type: "done"}}, false},
		{"unfinished second", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid + "<open_answer>"}, {Type: "done"}}, false},
		{"oversized second", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid + "<open_answer>" + strings.Repeat("x", 32769) + "</open_answer>"}, {Type: "done"}}, false},
		{"tool boundary", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "tool_use_start"}, {Type: "done"}}, false},
		{"tool result after block", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "tool_result"}, {Type: "done"}}, false},
		{"tool result after done", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "done"}, {Type: "tool_result"}}, false},
		{"tool result before answer", "Did Priya reply?", []AgentEvent{{Type: "tool_result"}, {Type: "assistant", Content: "Not yet. " + valid}, {Type: "done"}}, true},
		{"error", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}, {Type: "error"}, {Type: "close"}}, false},
		{"no completion", "Did Priya reply?", []AgentEvent{{Type: "assistant", Content: "Not yet. " + valid}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := openAnswerStream{question: tc.question}
			for _, evt := range tc.chunks {
				stream.emit(evt, func(AgentEvent) {})
			}
			if got := stream.completedCandidate(); (got != nil) != tc.want {
				t.Fatalf("candidate = %+v, want present %v", got, tc.want)
			}
		})
	}
}
