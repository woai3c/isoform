package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"wails-gui/storage"
)

// Regression: with the app window and a browser preview both open, each
// renderer stored its own copy of every reply row, so a main chat reply
// showed twice. One emit now carries one event id to every consumer (the
// window, the bridge and in-process listeners), and each renderer names the
// row it stores after it, so storage keeps one row.
func TestEmitAgentEventGivesEveryConsumerOneEventID(t *testing.T) {
	app := &App{ctx: context.Background(), eventBroadcaster: NewEventBroadcaster()}
	client := app.eventBroadcaster.AddClient("agent-event-id-test")
	defer app.eventBroadcaster.RemoveClient("agent-event-id-test")
	var heard []AgentEvent
	app.onAgentEvent(func(evt AgentEvent) { heard = append(heard, evt) })

	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "main", Content: "Hi"})
	app.emitAgentEvent(AgentEvent{Type: "done", Tag: "main", EventID: "kept"})

	if len(heard) != 2 {
		t.Fatalf("listener heard %d events, want 2", len(heard))
	}
	if heard[0].EventID == "" {
		t.Fatal("emit left the event without an id")
	}
	if heard[1].EventID != "kept" {
		t.Fatalf("emit replaced a given id: %q", heard[1].EventID)
	}

	for i, want := range heard {
		select {
		case payload := <-client:
			var msg struct {
				Event string       `json:"event"`
				Args  []AgentEvent `json:"args"`
			}
			if err := json.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("bridge payload %d: %v", i, err)
			}
			if msg.Event != "agent:event" || len(msg.Args) != 1 {
				t.Fatalf("bridge payload %d: %s", i, payload)
			}
			if msg.Args[0].EventID != want.EventID {
				t.Fatalf("bridge event %d id = %q, listener saw %q", i, msg.Args[0].EventID, want.EventID)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("bridge never got event %d", i)
		}
	}
}

// The backend names each row a turn builds, so a renderer that joins mid-reply
// grows the same row: a reply's chunks share one item id and count up, a tool's
// start and use share one, and a new row starts after a tool or a new run.
func TestEmitAgentEventNamesTheRowsOfATurn(t *testing.T) {
	app := &App{}
	var heard []AgentEvent
	app.onAgentEvent(func(evt AgentEvent) { heard = append(heard, evt) })

	for _, evt := range []AgentEvent{
		{Type: "run_created", Tag: "main", RunID: "r1"},
		{Type: "thinking", Tag: "main", Content: ""},
		{Type: "thinking", Tag: "main", Content: "Hmm"},
		{Type: "assistant", Tag: "main", Content: "I'll "},
		{Type: "assistant", Tag: "main", Content: "check."},
		{Type: "tool_use_start", Tag: "main", ToolUseID: "t1"},
		{Type: "tool_use", Tag: "main", ToolUseID: "t1"},
		{Type: "tool_use", Tag: "main", ToolUseID: "t2"},
		{Type: "tool_result", Tag: "main", ToolUseID: "t1"},
		{Type: "assistant", Tag: "main", Content: "Done."},
		{Type: "assistant", Tag: "other", Content: "Elsewhere"},
		{Type: "done", Tag: "main"},
		{Type: "assistant", Tag: "main", Content: "Next run"},
	} {
		app.emitAgentEvent(evt)
	}

	type stamp struct {
		id  string
		seq int
	}
	got := make([]stamp, len(heard))
	for i, evt := range heard {
		got[i] = stamp{evt.ItemID, evt.ItemSeq}
	}
	if got[0].id != "" || got[1].id != "" || got[8].id != "" || got[11].id != "" {
		t.Fatalf("run_created, an empty thinking chunk, tool_result and done name no row: %+v", got)
	}
	if got[2].id == "" || got[2].seq != 1 {
		t.Fatalf("thinking row: %+v", got[2])
	}
	if got[3].id == "" || got[3].id != got[4].id || got[3].seq != 1 || got[4].seq != 2 || got[3].id == got[2].id {
		t.Fatalf("assistant chunks: %+v %+v", got[3], got[4])
	}
	if got[5].id == "" || got[5].id != got[6].id || got[7].id == "" || got[7].id == got[5].id {
		t.Fatalf("tool rows: %+v %+v %+v", got[5], got[6], got[7])
	}
	if got[9].id == got[3].id || got[9].seq != 1 {
		t.Fatalf("assistant after a tool starts a new row: %+v", got[9])
	}
	if got[10].id == got[9].id || got[10].seq != 1 {
		t.Fatalf("another chat's row: %+v", got[10])
	}
	if got[12].id == got[9].id || got[12].seq != 1 {
		t.Fatalf("a new run starts a new row: %+v", got[12])
	}
}

// A reply a renderer could not hold whole (a window reloaded mid-reply) still
// ends up stored complete: when the row ends, the backend writes its text, and
// that write only ever grows the stored row.
func TestEmitAgentEventStoresAFinishedRowsText(t *testing.T) {
	prev := agentSegmentWriteDelay
	agentSegmentWriteDelay = 0
	t.Cleanup(func() { agentSegmentWriteDelay = prev })

	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.SaveProject(ctx, storage.ProjectRow{ID: "p1", Name: "P", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "spotlight_1"} {
		if err := st.SaveSession(ctx, storage.SessionRow{LocalID: id, ProjectID: "p1", Label: id, Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{store: st}
	var heard []AgentEvent
	app.onAgentEvent(func(evt AgentEvent) { heard = append(heard, evt) })

	app.emitAgentEvent(AgentEvent{Type: "run_created", Tag: "main", RunID: "r1"})
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "main", Content: "I'll "})
	// The renderer stored the row's start, and nothing more before reloading.
	if _, err := st.AppendStreamItem(ctx, "main", storage.StreamItemRow{ID: heard[1].ItemID, Kind: "assistant", Content: "I'll "}); err != nil {
		t.Fatal(err)
	}
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "main", Content: "check."})
	app.emitAgentEvent(AgentEvent{Type: "done", Tag: "main"})

	// Scheduled runs and spotlight chats store their own rows.
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "spotlight_1", Content: "Hi"})
	if _, err := st.AppendStreamItem(ctx, "spotlight_1", storage.StreamItemRow{ID: heard[4].ItemID, Kind: "assistant", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	app.emitAgentEvent(AgentEvent{Type: "done", Tag: "spotlight_1"})
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "cron", Content: "Hi", Persisted: true})
	if heard[6].ItemID != "" {
		t.Fatalf("a scheduled run's event was named: %+v", heard[6])
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := st.LoadStreamItems(ctx, "main")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].Content == "I'll check." {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stored row never got the whole text: %+v", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	rows, err := st.LoadStreamItems(ctx, "spotlight_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Content != "x" {
		t.Fatalf("a spotlight row was written: %+v", rows)
	}
}

// Quitting cancels the turns in flight and closes the store: the backend's
// pending writes, and the text of rows still open, are written first (early
// in shutdown, after StopAgent, and right before the store closes), and
// nothing is written after.
func TestDrainAgentSegmentWritesBeforeTheStoreCloses(t *testing.T) {
	prev := agentSegmentWriteDelay
	agentSegmentWriteDelay = time.Hour
	t.Cleanup(func() { agentSegmentWriteDelay = prev })

	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.SaveProject(ctx, storage.ProjectRow{ID: "p1", Name: "P", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ended", "open"} {
		if err := st.SaveSession(ctx, storage.SessionRow{LocalID: id, ProjectID: "p1", Label: id, Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{store: st}
	var heard []AgentEvent
	app.onAgentEvent(func(evt AgentEvent) { heard = append(heard, evt) })
	store := func(tag string, evt AgentEvent) {
		if _, err := st.AppendStreamItem(ctx, tag, storage.StreamItemRow{ID: evt.ItemID, Kind: "assistant", Content: evt.Content}); err != nil {
			t.Fatal(err)
		}
	}

	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "ended", Content: "Done "})
	store("ended", heard[0])
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "ended", Content: "here."})
	app.emitAgentEvent(AgentEvent{Type: "done", Tag: "ended"})
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "open", Content: "Half "})
	store("open", heard[3])
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "open", Content: "way"})

	// Early in shutdown, and after StopAgent: the rows so far are written and
	// stay open.
	app.flushAgentSegmentWrites(false)
	rows, err := st.LoadStreamItems(ctx, "open")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Content != "Half way" {
		t.Fatalf("open row after a flush = %+v", rows)
	}
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "open", Content: " there"})

	// Right before the store closes.
	app.flushAgentSegmentWrites(true)

	for tag, want := range map[string]string{"ended": "Done here.", "open": "Half way there"} {
		rows, err := st.LoadStreamItems(ctx, tag)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Content != want {
			t.Fatalf("%s rows = %+v, want %q", tag, rows, want)
		}
	}

	// A turn cancelled after the drain writes nothing to the closed store.
	st.Close()
	app.emitAgentEvent(AgentEvent{Type: "assistant", Tag: "late", Content: "x"})
	app.emitAgentEvent(AgentEvent{Type: "done", Tag: "late"})
	app.agentEventItems.mu.Lock()
	pending := len(app.agentEventItems.writes)
	app.agentEventItems.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d writes scheduled after the drain", pending)
	}
}

// Only a turn's own events start tracking a chat; an event outside a turn
// leaves nothing behind.
func TestEmitAgentEventOutsideATurnLeavesNoEntry(t *testing.T) {
	app := &App{}
	for _, evt := range []AgentEvent{
		{Type: "available_commands", Tag: "idle"},
		{Type: "close", Tag: "idle"},
		{Type: "compact_end", Tag: "idle"},
	} {
		app.emitAgentEvent(evt)
	}
	app.emitAgentEvent(AgentEvent{Type: "run_created", Tag: "busy", RunID: "r1"})
	app.agentEventItems.mu.Lock()
	defer app.agentEventItems.mu.Unlock()
	if _, ok := app.agentEventItems.turns["idle"]; ok || len(app.agentEventItems.turns) != 1 {
		t.Fatalf("turns = %v", app.agentEventItems.turns)
	}
}

// The early shutdown flush and the one after StopAgent run while turns still
// stream: they write what they have and return, and new writes keep being
// scheduled (no wait that a new write could race).
func TestFlushAgentSegmentWritesWhileTurnsStream(t *testing.T) {
	prev := agentSegmentWriteDelay
	agentSegmentWriteDelay = time.Millisecond
	t.Cleanup(func() { agentSegmentWriteDelay = prev })

	var mu sync.Mutex
	written := 0
	items := &agentEventItems{}
	write := func(segs []finishedSegment) {
		mu.Lock()
		written += len(segs)
		mu.Unlock()
	}

	stop := make(chan struct{})
	streamed := make(chan struct{})
	go func() {
		defer close(streamed)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tag := fmt.Sprintf("chat-%d", i%4)
			items.stamp(&AgentEvent{Type: "assistant", Tag: tag, Content: "x"}, write)
			items.stamp(&AgentEvent{Type: "tool_use", Tag: tag, ToolUseID: "t"}, write)
		}
	}()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		items.flush(write, false)
	}
	close(stop)
	<-streamed
	items.flush(write, true)

	mu.Lock()
	defer mu.Unlock()
	if written == 0 {
		t.Fatal("nothing was written")
	}
}
