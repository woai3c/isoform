package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The rows a turn's events build are named by the backend. Every renderer
// that shows the turn (the app window, and a browser preview over the bridge)
// stores the rows itself, and a renderer can join in the middle of a reply: a
// preview tab opened during it, or a window reloaded. So emitAgentEvent stamps
// each event of an assistant, thinking or tool row with that row's id
// (ItemID), and assistant and thinking chunks with their place in the row
// (ItemSeq, from 1). A renderer that saw a row from chunk 1 on, without a gap,
// stores and grows it; one that did not shows what it has and leaves the
// stored text alone. When a row ends, the backend writes its whole text once
// more, which only ever grows a stored row (GrowStreamItemContent), so a reply
// a reloaded window could not finish still ends up complete.

// agentSegmentWriteDelay lets the renderers' own writes of a row land before
// the backend writes the row's whole text; a test seam.
var agentSegmentWriteDelay = 2 * time.Second

// agentEventSegment is one assistant or thinking row being streamed.
type agentEventSegment struct {
	id      string
	seq     int
	content strings.Builder
}

// agentEventTurn is the open rows of one chat (tag).
type agentEventTurn struct {
	assistant *agentEventSegment
	thinking  *agentEventSegment
	// tools maps a tool_use_id from tool_use_start to the row id its tool_use
	// reuses.
	tools map[string]string
}

// agentEventItems holds the open rows of every chat with a turn in flight,
// and the backend's writes of rows that ended. The zero value is ready to
// use.
type agentEventItems struct {
	mu    sync.Mutex
	turns map[string]*agentEventTurn
	// writes are the scheduled writes of ended rows, by timer; drain runs
	// them before the store closes.
	writes   map[*time.Timer][]finishedSegment
	inflight sync.WaitGroup
	// drained is set once the store is about to close: nothing more is
	// written.
	drained bool
}

// finishedSegment is a row whose last chunk has gone out.
type finishedSegment struct {
	tag     string
	id      string
	content string
}

// agentEventOpensTurn reports whether an event type belongs to a turn's rows;
// only these start tracking a chat, so an event outside a turn (say
// available_commands) leaves nothing behind.
func agentEventOpensTurn(eventType string) bool {
	switch eventType {
	case "run_created", "assistant", "thinking", "tool_use_start", "tool_use":
		return true
	}
	return false
}

// stamp names the row evt belongs to and, when write is set, schedules the
// write of the rows evt ends. Both happen under one lock, so a flush cannot
// fall between a row ending and its write being scheduled. It also returns the
// assistant row completed by done, for recording that turn's deliverables.
func (items *agentEventItems) stamp(evt *AgentEvent, write func([]finishedSegment)) *finishedSegment {
	if evt.Tag == "" {
		return nil
	}
	items.mu.Lock()
	defer items.mu.Unlock()
	turn := items.turns[evt.Tag]
	if turn == nil {
		if !agentEventOpensTurn(evt.Type) {
			return nil
		}
		if items.turns == nil {
			items.turns = map[string]*agentEventTurn{}
		}
		turn = &agentEventTurn{tools: map[string]string{}}
		items.turns[evt.Tag] = turn
	}

	var finished []finishedSegment
	end := func(seg **agentEventSegment) *finishedSegment {
		if *seg == nil {
			return nil
		}
		ended := finishedSegment{tag: evt.Tag, id: (*seg).id, content: (*seg).content.String()}
		finished = append(finished, ended)
		*seg = nil
		return &ended
	}
	grow := func(seg **agentEventSegment) {
		if *seg == nil {
			*seg = &agentEventSegment{id: uuid.NewString()}
		}
		(*seg).seq++
		(*seg).content.WriteString(evt.Content)
		evt.ItemID = (*seg).id
		evt.ItemSeq = (*seg).seq
	}

	var completedAssistant *finishedSegment
	switch evt.Type {
	case "assistant":
		// Assistant text ends the thinking row, as in the renderer.
		end(&turn.thinking)
		grow(&turn.assistant)
	case "thinking":
		// The renderer makes no row for an empty thinking chunk, so it
		// does not count.
		if evt.Content != "" {
			grow(&turn.thinking)
		}
	case "tool_use_start":
		end(&turn.assistant)
		end(&turn.thinking)
		if evt.ToolUseID != "" {
			evt.ItemID = uuid.NewString()
			turn.tools[evt.ToolUseID] = evt.ItemID
		}
	case "tool_use":
		end(&turn.assistant)
		end(&turn.thinking)
		if id, ok := turn.tools[evt.ToolUseID]; ok && evt.ToolUseID != "" {
			evt.ItemID = id
			delete(turn.tools, evt.ToolUseID)
		} else {
			evt.ItemID = uuid.NewString()
		}
	case "run_created", "permission_request", "plan_complete":
		end(&turn.assistant)
		end(&turn.thinking)
	case "done":
		completedAssistant = end(&turn.assistant)
		end(&turn.thinking)
		delete(items.turns, evt.Tag)
	case "error", "close":
		end(&turn.assistant)
		end(&turn.thinking)
		delete(items.turns, evt.Tag)
	}
	if write != nil {
		if segs := agentSegmentsToStore(finished); len(segs) > 0 {
			items.scheduleLocked(segs, write)
		}
	}
	return completedAssistant
}

// scheduleLocked runs write(segs) after agentSegmentWriteDelay, unless the
// store is closing. The caller holds items.mu.
func (items *agentEventItems) scheduleLocked(segs []finishedSegment, write func([]finishedSegment)) {
	if items.drained {
		return
	}
	if items.writes == nil {
		items.writes = map[*time.Timer][]finishedSegment{}
	}
	items.inflight.Add(1)
	// The callback takes the lock, so it sees timer set and the entry in
	// writes even with no delay.
	var timer *time.Timer
	timer = time.AfterFunc(agentSegmentWriteDelay, func() {
		defer items.inflight.Done()
		items.mu.Lock()
		due, ok := items.writes[timer]
		delete(items.writes, timer)
		items.mu.Unlock()
		if ok {
			write(due)
		}
	})
	items.writes[timer] = segs
}

// flush writes now every scheduled write and the text of every row still
// open. Rows stay open, so later chunks keep growing them. Closing (the store
// is about to close) also stops all later writes and then waits for writes
// already running; only then, with no new ones possible, is waiting safe.
func (items *agentEventItems) flush(write func([]finishedSegment), closing bool) {
	items.mu.Lock()
	if closing {
		items.drained = true
	}
	var segs []finishedSegment
	for timer, due := range items.writes {
		// A timer that already fired writes its rows itself.
		if timer.Stop() {
			delete(items.writes, timer)
			items.inflight.Done()
			segs = append(segs, due...)
		}
	}
	for tag, turn := range items.turns {
		for _, seg := range []*agentEventSegment{turn.assistant, turn.thinking} {
			if seg != nil {
				segs = append(segs, finishedSegment{tag: tag, id: seg.id, content: seg.content.String()})
			}
		}
	}
	if closing {
		items.turns = nil
	}
	items.mu.Unlock()
	write(segs)
	if closing {
		items.inflight.Wait()
	}
}

// agentSegmentsToStore leaves out rows the backend does not grow: chats that
// store their own rows (spotlight) and empty text.
func agentSegmentsToStore(segs []finishedSegment) []finishedSegment {
	out := segs[:0:0]
	for _, seg := range segs {
		if seg.content == "" || strings.HasPrefix(seg.tag, "spotlight_") {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// writeAgentSegments grows each row to its whole text.
func (a *App) writeAgentSegments(segs []finishedSegment) {
	if a.store == nil {
		return
	}
	for _, seg := range segs {
		if _, err := a.store.GrowStreamItemContent(context.Background(), seg.tag, seg.id, seg.content); err != nil {
			log.Printf("[agent-event] store the text of row %s (tag=%s): %v", seg.id, seg.tag, err)
		}
	}
}

// agentSegmentWriter is the write stamp schedules for ended rows; nil without
// a store.
func (a *App) agentSegmentWriter() func([]finishedSegment) {
	if a.store == nil {
		return nil
	}
	return a.writeAgentSegments
}

// flushAgentSegmentWrites writes the rows the backend still has to grow:
// early in shutdown (stopping the model gateway can take long enough for the
// exit watchdog to fire), after StopAgent cancels the turns in flight, and,
// closing, right before the store closes.
func (a *App) flushAgentSegmentWrites(closing bool) {
	a.agentEventItems.flush(func(segs []finishedSegment) {
		a.writeAgentSegments(agentSegmentsToStore(segs))
	}, closing)
}
