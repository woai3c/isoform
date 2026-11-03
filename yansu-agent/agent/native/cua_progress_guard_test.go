package native

import (
	"strings"
	"testing"
)

func cuaBlock(content string) ContentBlock {
	return ContentBlock{Type: "tool_result", Content: content}
}

func cuaInput(name string) string {
	return `{"name":"` + name + `"}`
}

// Action → identical snapshot, twice, trips the no-progress guard.
func TestRecordCuaProgress_NoProgressAfterActions(t *testing.T) {
	st := &CuaProgressState{}
	snap := `Window: "Inbox - Gmail"  URL: mail.google.com/mail/u/0/#inbox`

	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap)); got != 0 {
		t.Fatalf("initial snapshot: got %d, want 0", got)
	}
	// action 1 → snapshot unchanged → stuck 1
	RecordCuaProgress(st, "cua_driver", cuaInput("click"), cuaBlock("clicked ref e10"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap)); got != 1 {
		t.Fatalf("round 1: got %d, want 1", got)
	}
	// action 2 → snapshot still unchanged → stuck 2 (>= threshold)
	RecordCuaProgress(st, "cua_driver", cuaInput("press_key"), cuaBlock("pressed return"))
	got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap))
	if got != 2 || got < CuaNoProgressThreshold {
		t.Fatalf("round 2: got %d, want 2 (>= CuaNoProgressThreshold=%d)", got, CuaNoProgressThreshold)
	}
}

// A committing navigation changes the fingerprint and resets the counter.
func TestRecordCuaProgress_ProgressResetsOnWindowChange(t *testing.T) {
	st := &CuaProgressState{}
	a := `Window: "Inbox - Gmail" URL: mail.google.com/#inbox`
	b := `Window: "Search results - Gmail" URL: mail.google.com/#search/after:2026/6/1`

	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(a))
	RecordCuaProgress(st, "cua_driver", cuaInput("click"), cuaBlock("clicked"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(a)); got != 1 {
		t.Fatalf("stuck round: got %d, want 1", got)
	}
	// navigation finally commits — fingerprint changes → reset to 0
	RecordCuaProgress(st, "cua_driver", cuaInput("type_text"), cuaBlock("typed"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(b)); got != 0 {
		t.Fatalf("after progress: got %d, want 0", got)
	}
}

// Nudged (set by the loop once a streak trips) must stay set through a
// continued stall — so the loop does not re-spam the same nudge — and reset
// only when real progress occurs, re-arming for a genuinely new stall.
func TestRecordCuaProgress_NudgeReArmsOnlyOnProgress(t *testing.T) {
	st := &CuaProgressState{}
	a := `Window: "Inbox - Gmail" URL: mail.google.com/#inbox`
	b := `Window: "Compose - Gmail" URL: mail.google.com/#compose`

	// Baseline at window a, then simulate the loop having nudged.
	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(a))
	st.Nudged = true

	// Continued stall (same window): Nudged must stay set.
	RecordCuaProgress(st, "cua_driver", cuaInput("click"), cuaBlock("clicked"))
	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(a))
	if !st.Nudged {
		t.Error("a continued stall must keep Nudged set so the loop does not re-nudge")
	}

	// Navigation commits (window changes) → progress → re-arm.
	RecordCuaProgress(st, "cua_driver", cuaInput("click"), cuaBlock("clicked"))
	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(b))
	if st.Nudged {
		t.Error("real progress should re-arm the nudge (Nudged=false)")
	}
}

// scroll is a read, not an action: an unchanged window/URL after scrolling a
// long list must NOT be mistaken for a stuck loop.
func TestRecordCuaProgress_ScrollNotCountedAsStuck(t *testing.T) {
	st := &CuaProgressState{}
	snap := `Window: "Inbox - Gmail" URL: mail.google.com/#inbox`

	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap))
	RecordCuaProgress(st, "cua_driver", cuaInput("scroll"), cuaBlock("scrolled"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap)); got != 0 {
		t.Fatalf("scroll read: got %d, want 0 (scroll must not trip no-progress)", got)
	}
}

// Non-cua tools and non-snapshot results (list_apps, acks) are ignored.
func TestRecordCuaProgress_IgnoresNonCuaAndNonSnapshot(t *testing.T) {
	st := &CuaProgressState{}
	if got := RecordCuaProgress(st, "Bash", `{"command":"ls"}`, cuaBlock("files")); got != 0 {
		t.Fatalf("non-cua tool: got %d, want 0", got)
	}
	// list_apps result has no window/elements fingerprint → ignored
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("list_apps"), cuaBlock(`{"apps":[]}`)); got != 0 {
		t.Fatalf("list_apps: got %d, want 0", got)
	}
}

// jsonSnap builds a snapshot in yansu's REAL daemon JSON shape (snapToDict),
// the format the guard actually sees — not Codex's "Window: …" text.
func jsonSnap(title string, nElements int) string {
	parts := make([]string, nElements)
	for i := range parts {
		parts[i] = `{"role":"AXButton"}`
	}
	return `{"snapshot_id":"x","window":{"title":"` + title + `","live_title":"` + title + `"},"elements":[` + strings.Join(parts, ",") + `]}`
}

// The real-format case: identical JSON snapshot after two actions trips the
// guard. This is what was BROKEN before — the old regex matched only Codex's
// "Window: …" text, so the fingerprint of a real JSON snapshot was always
// empty and the guard never fired.
func TestRecordCuaProgress_JSONSnapshotNoProgress(t *testing.T) {
	st := &CuaProgressState{}
	snap := jsonSnap("收件箱 - Gmail", 50)

	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap))
	RecordCuaProgress(st, "cua_driver", cuaInput("click"), cuaBlock("clicked"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap)); got != 1 {
		t.Fatalf("round 1: got %d, want 1", got)
	}
	RecordCuaProgress(st, "cua_driver", cuaInput("press_key"), cuaBlock("pressed"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap)); got < CuaNoProgressThreshold {
		t.Fatalf("round 2: got %d, want >= %d", got, CuaNoProgressThreshold)
	}
}

// AX-content signal: a native app with no URL whose title doesn't change, but
// whose element count changes, counts as PROGRESS — the case the old
// title+URL-only fingerprint could not see.
func TestRecordCuaProgress_JSONElementCountChangeIsProgress(t *testing.T) {
	st := &CuaProgressState{}
	a := jsonSnap("Notes", 30)
	b := jsonSnap("Notes", 45) // same title, content changed

	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(a))
	RecordCuaProgress(st, "cua_driver", cuaInput("click"), cuaBlock("clicked"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(a)); got != 1 {
		t.Fatalf("same title+count must be stuck: got %d, want 1", got)
	}
	RecordCuaProgress(st, "cua_driver", cuaInput("type_text"), cuaBlock("typed"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(b)); got != 0 {
		t.Fatalf("element-count change must be progress: got %d, want 0", got)
	}
}

// The first snapshot of a run is prefixed with the injected inventory; the
// fingerprint must strip it and match the same un-prefixed snapshot, so the
// prelude doesn't mask a real stall.
func TestRecordCuaProgress_FingerprintStripsInventoryPrefix(t *testing.T) {
	st := &CuaProgressState{}
	snap := jsonSnap("收件箱 - Gmail", 50)
	prefixed := cuaInventoryPreludeHeader + `{"apps":[]}` + cuaInventoryPreludeSeparator + snap

	RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(prefixed))
	RecordCuaProgress(st, "cua_driver", cuaInput("click"), cuaBlock("clicked"))
	if got := RecordCuaProgress(st, "cua_driver", cuaInput("get_window_state"), cuaBlock(snap)); got != 1 {
		t.Fatalf("inventory prefix must not change the fingerprint: got %d, want 1", got)
	}
}
