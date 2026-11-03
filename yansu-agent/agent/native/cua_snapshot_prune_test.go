package native

import (
	"strings"
	"testing"
)

// bigSnapshot returns a string large enough to clear cuaSnapshotMinFoldBytes.
func bigSnapshot(tag string) string {
	return tag + ": " + strings.Repeat("AXButton ref=e1 label=foo\n", 200)
}

// snapshotToolUse builds an assistant tool_use block for a cua_driver
// sub-tool call.
func snapshotToolUse(id, sub string) ContentBlock {
	return ContentBlock{
		Type:  "tool_use",
		ID:    id,
		Name:  "cua_driver",
		Input: map[string]any{"name": sub, "args": map[string]any{}},
	}
}

func toolResult(useID, body string) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolUseID: useID, Content: body}
}

// convo builds a realistic assistant(tool_use) → user(tool_result) history
// from a list of (id, sub-tool, body) snapshot/action steps.
func convo(steps []struct {
	id, sub, body string
},
) []Message {
	var msgs []Message
	for _, s := range steps {
		msgs = append(msgs,
			Message{Role: "assistant", Content: []ContentBlock{snapshotToolUse(s.id, s.sub)}},
			Message{Role: "user", Content: []ContentBlock{toolResult(s.id, s.body)}},
		)
	}
	return msgs
}

func resultBodyForID(msgs []Message, useID string) (string, bool) {
	for i := range msgs {
		blocks, ok := msgs[i].Content.([]ContentBlock)
		if !ok {
			continue
		}
		for j := range blocks {
			if blocks[j].Type == "tool_result" && blocks[j].ToolUseID == useID {
				s, _ := blocks[j].Content.(string)
				return s, true
			}
		}
	}
	return "", false
}

func TestPruneStaleCuaSnapshots_FoldsOlderKeepsRecent(t *testing.T) {
	msgs := convo([]struct{ id, sub, body string }{
		{"a", "get_window_state", bigSnapshot("first")},
		{"b", "get_window_state", bigSnapshot("second")},
		{"c", "get_window_state", bigSnapshot("third")},
		{"d", "get_window_state", bigSnapshot("fourth")},
	})

	stats := PruneStaleCuaSnapshots(msgs)
	if stats.FoldedCount != 2 {
		t.Fatalf("expected 2 folded (4 snapshots, keep %d), got %d", cuaSnapshotKeepRecent, stats.FoldedCount)
	}
	if stats.BytesFreed <= 0 {
		t.Fatalf("expected positive bytes freed, got %d", stats.BytesFreed)
	}

	// Oldest two folded.
	for _, id := range []string{"a", "b"} {
		got, _ := resultBodyForID(msgs, id)
		if got != cuaStaleSnapshotPlaceholder {
			t.Errorf("snapshot %q should be folded, got %.40q", id, got)
		}
	}
	// Most recent two kept in full.
	for _, id := range []string{"c", "d"} {
		got, _ := resultBodyForID(msgs, id)
		if got == cuaStaleSnapshotPlaceholder {
			t.Errorf("snapshot %q should be kept in full but was folded", id)
		}
	}
}

func TestPruneStaleCuaSnapshots_NoopAtOrBelowKeepRecent(t *testing.T) {
	msgs := convo([]struct{ id, sub, body string }{
		{"a", "get_window_state", bigSnapshot("first")},
		{"b", "get_window_state", bigSnapshot("second")},
	})
	if stats := PruneStaleCuaSnapshots(msgs); stats.Touched() {
		t.Fatalf("expected no folding at keepRecent boundary, got %+v", stats)
	}
}

func TestPruneStaleCuaSnapshots_Idempotent(t *testing.T) {
	msgs := convo([]struct{ id, sub, body string }{
		{"a", "get_window_state", bigSnapshot("first")},
		{"b", "get_window_state", bigSnapshot("second")},
		{"c", "get_window_state", bigSnapshot("third")},
	})
	first := PruneStaleCuaSnapshots(msgs)
	if first.FoldedCount != 1 {
		t.Fatalf("first pass: expected 1 folded, got %d", first.FoldedCount)
	}
	if second := PruneStaleCuaSnapshots(msgs); second.Touched() {
		t.Fatalf("second pass should be a no-op, got %+v", second)
	}
}

func TestPruneStaleCuaSnapshots_LeavesActionAndSmallResults(t *testing.T) {
	msgs := convo([]struct{ id, sub, body string }{
		{"a", "get_window_state", bigSnapshot("first")},
		{"b", "click", "clicked ok"},                  // action tool: never folded
		{"c", "get_window_state", "tiny"},             // below min bytes: not folded
		{"d", "get_window_state", bigSnapshot("big")}, // recent: kept
		{"e", "get_window_state", bigSnapshot("big2")},
	})
	// Snapshots in order: a, c, d, e → keep last 2 (d,e), eligible: a, c.
	// c is below min bytes → only a folds.
	stats := PruneStaleCuaSnapshots(msgs)
	if stats.FoldedCount != 1 {
		t.Fatalf("expected only the large stale snapshot folded, got %d", stats.FoldedCount)
	}
	if body, _ := resultBodyForID(msgs, "a"); body != cuaStaleSnapshotPlaceholder {
		t.Errorf("snapshot a should be folded")
	}
	if body, _ := resultBodyForID(msgs, "b"); body != "clicked ok" {
		t.Errorf("action result b must be untouched, got %.40q", body)
	}
	if body, _ := resultBodyForID(msgs, "c"); body != "tiny" {
		t.Errorf("small snapshot c must be untouched, got %.40q", body)
	}
}
