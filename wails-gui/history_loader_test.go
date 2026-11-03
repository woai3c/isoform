package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

// newHistoryTestStore is a minimal temp-dir Store seeded with a project
// and a session so AppendStreamItem won't trip over the session_local_id
// foreign key constraint.
func newHistoryTestStore(t *testing.T, sessionLocalID string) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	if err := s.SaveProject(ctx, storage.ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := s.SaveSession(ctx, storage.SessionRow{LocalID: sessionLocalID, ProjectID: "p1", Label: "S", Model: "m"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	return s
}

func appendItems(t *testing.T, s *storage.Store, tag string, items []storage.StreamItemRow) {
	t.Helper()
	ctx := context.Background()
	for _, it := range items {
		if _, err := s.AppendStreamItem(ctx, tag, it); err != nil {
			t.Fatalf("AppendStreamItem(%s): %v", it.ID, err)
		}
	}
}

// Multi-turn conversation: user -> assistant -> user -> assistant -> user
// (the final user row is the current turn's prompt the frontend just
// persisted before calling SendPrompt). The loader must return the first
// two complete turns and drop the trailing current-turn user row so the
// loop doesn't re-append it and duplicate the user message in the request.
func TestStreamItemHistoryLoader_DropsTrailingCurrentUserTurn(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "write fib.py"},
		{ID: "a1", Kind: "assistant", Content: "done, wrote /tmp/fib.py"},
		{ID: "u2", Kind: "user", Content: "now test it"},
		{ID: "a2", Kind: "assistant", Content: "pytest passes"},
		{ID: "u3", Kind: "user", Content: "thanks"}, // current turn
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	wantContents := []string{"write fib.py", "done, wrote /tmp/fib.py", "now test it", "pytest passes"}
	if len(got) != len(wantContents) {
		t.Fatalf("got %d items, want %d: %+v", len(got), len(wantContents), got)
	}
	for i, h := range got {
		if h.Content != wantContents[i] {
			t.Errorf("item[%d].Content = %q, want %q", i, h.Content, wantContents[i])
		}
	}
	if got[0].Kind != "user" || got[1].Kind != "assistant" || got[2].Kind != "user" || got[3].Kind != "assistant" {
		t.Errorf("kinds out of order: %+v", got)
	}
}

// Legacy tool_use/tool_result rows without a tool_use_id, as well as
// thinking / system / error rows, are silently dropped. They can't be
// paired and would cause 400s from the API.
func TestStreamItemHistoryLoader_SkipsLegacyToolAndNonTextKinds(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "hi"},
		{ID: "th1", Kind: "thinking", Content: "let me think"},
		{ID: "t1", Kind: "tool_use", ToolName: "Bash", ToolInput: `{"cmd":"ls"}`}, // no ToolUseID -> skipped
		{ID: "tr1", Kind: "tool_result", Content: "file1\nfile2"},                 // no ToolUseID -> skipped
		{ID: "a1", Kind: "assistant", Content: "here you go"},
		{ID: "sys1", Kind: "system", Content: "run created"},
		{ID: "err1", Kind: "error", Content: "network blip"},
		{ID: "u2", Kind: "user", Content: "current"}, // trailing, dropped
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2 (user + assistant); actual=%+v", len(got), got)
	}
	if got[0].Content != "hi" || got[0].Kind != "user" {
		t.Errorf("item[0] = %+v, want user/hi", got[0])
	}
	if got[1].Content != "here you go" || got[1].Kind != "assistant" {
		t.Errorf("item[1] = %+v, want assistant/\"here you go\"", got[1])
	}
}

// tool_use/tool_result rows that have a tool_use_id are included in
// the history so BuildMessages can reconstruct properly paired tool
// blocks for the Anthropic API.
func TestStreamItemHistoryLoader_IncludesToolWithUseID(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "read /tmp/x"},
		{ID: "a1", Kind: "assistant", Content: "ok"},
		{ID: "t1", Kind: "tool_use", ToolName: "Read", ToolInput: `{"path":"/tmp/x"}`, ToolUseID: "toolu_abc123"},
		{ID: "tr1", Kind: "tool_result", Content: "file contents", ToolUseID: "toolu_abc123"},
		{ID: "a2", Kind: "assistant", Content: "here you go"},
		{ID: "u2", Kind: "user", Content: "thanks"}, // trailing, dropped
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d items, want 5; actual=%+v", len(got), got)
	}
	if got[0].Kind != "user" || got[0].Content != "read /tmp/x" {
		t.Errorf("item[0] = %+v", got[0])
	}
	if got[1].Kind != "assistant" || got[1].Content != "ok" {
		t.Errorf("item[1] = %+v", got[1])
	}
	if got[2].Kind != "tool_use" || got[2].ToolName != "Read" || got[2].ToolUseID != "toolu_abc123" {
		t.Errorf("item[2] = %+v, want tool_use Read with toolu_abc123", got[2])
	}
	if got[3].Kind != "tool_result" || got[3].Content != "file contents" || got[3].ToolUseID != "toolu_abc123" {
		t.Errorf("item[3] = %+v, want tool_result with toolu_abc123", got[3])
	}
	if got[4].Kind != "assistant" || got[4].Content != "here you go" {
		t.Errorf("item[4] = %+v", got[4])
	}
}

// Empty content rows (assistant placeholder before any delta, or a pure
// image-only user message) must not become empty HistoryItems — the
// loop builder expects non-empty text. Image-only user rows with an
// Images JSON payload, however, DO need to survive so vision context
// carries over.
func TestStreamItemHistoryLoader_DropsEmptyAndPreservesImages(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	imgJSON := `[{"mediaType":"image/png","dataBase64":"iVBORw0KGgo="}]`
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "", Images: imgJSON}, // image-only -> kept
		{ID: "a1", Kind: "assistant", Content: ""},             // empty -> dropped
		{ID: "u2", Kind: "user", Content: "text turn"},
		{ID: "a2", Kind: "assistant", Content: "ack"},
		{ID: "u3", Kind: "user", Content: "trailing"}, // dropped
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3: %+v", len(got), got)
	}
	if got[0].Kind != "user" || got[0].Images != imgJSON {
		t.Errorf("item[0] = %+v, want image-only user", got[0])
	}
	if got[1].Kind != "user" || got[1].Content != "text turn" {
		t.Errorf("item[1] = %+v, want user text turn", got[1])
	}
	if got[2].Kind != "assistant" || got[2].Content != "ack" {
		t.Errorf("item[2] = %+v, want assistant ack", got[2])
	}
}

// If EVERY persisted item is a trailing user message (brand-new session,
// first prompt), the loader should return an empty slice — the loop
// will then just send cfg.Prompt alone.
func TestStreamItemHistoryLoader_FirstPromptOfSession(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "hello"},
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty history on first prompt, got %+v", got)
	}
}

// A conversation that ends on an assistant message (e.g. the frontend's
// current-turn user persist is still in-flight) must return everything
// — the trailing-user drop rule should not strip the terminal assistant.
func TestStreamItemHistoryLoader_TrailingAssistantKept(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "q"},
		{ID: "a1", Kind: "assistant", Content: "a"},
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %+v", got)
	}
}

// Multiple trailing user rows — shouldn't happen in practice but the
// loop would double/triple-send if we only dropped one. Verify all get
// stripped down to the last assistant anchor.
func TestStreamItemHistoryLoader_DropsAllTrailingUsers(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "q"},
		{ID: "a1", Kind: "assistant", Content: "a"},
		{ID: "u2", Kind: "user", Content: "bad1"},
		{ID: "u3", Kind: "user", Content: "bad2"},
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 2 || got[1].Kind != "assistant" {
		t.Fatalf("expected trailing user rows stripped, got %+v", got)
	}
}

// The frontend persists tool items with kind "tool" (not "tool_use") and
// result items with kind "result" (not "tool_result"). The history loader
// must accept both spellings so interactive-session history round-trips.
func TestStreamItemHistoryLoader_AcceptsFrontendToolAndResultKinds(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "read /tmp/x"},
		{ID: "a1", Kind: "assistant", Content: "ok"},
		{ID: "t1", Kind: "tool", ToolName: "Read", ToolInput: `{"path":"/tmp/x"}`, ToolUseID: "toolu_fe1"},
		{ID: "r1", Kind: "result", Content: "file contents", ToolName: "Read", ToolUseID: "toolu_fe1"},
		{ID: "a2", Kind: "assistant", Content: "done"},
		{ID: "u2", Kind: "user", Content: "trailing"}, // dropped
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d items, want 5; actual=%+v", len(got), got)
	}
	// "tool" -> "tool_use" for the API
	if got[2].Kind != "tool_use" || got[2].ToolName != "Read" || got[2].ToolUseID != "toolu_fe1" {
		t.Errorf("item[2] = %+v, want tool_use Read with toolu_fe1", got[2])
	}
	// "result" -> "tool_result" for the API
	if got[3].Kind != "tool_result" || got[3].Content != "file contents" || got[3].ToolUseID != "toolu_fe1" {
		t.Errorf("item[3] = %+v, want tool_result with toolu_fe1", got[3])
	}
}

// Defensive: a nil loader / nil store / empty key must return (nil, nil)
// so app startup races or misconfigured call sites don't panic.
func TestStreamItemHistoryLoader_NilAndEmptyInputs(t *testing.T) {
	var nilLoader *streamItemHistoryLoader
	if h, err := nilLoader.LoadHistory("anything"); err != nil || h != nil {
		t.Errorf("nil receiver: got (%v, %v), want (nil, nil)", h, err)
	}

	empty := &streamItemHistoryLoader{store: nil}
	if h, err := empty.LoadHistory("anything"); err != nil || h != nil {
		t.Errorf("nil store: got (%v, %v), want (nil, nil)", h, err)
	}

	s := newHistoryTestStore(t, "s1")
	loader := &streamItemHistoryLoader{store: s}
	if h, err := loader.LoadHistory(""); err != nil || h != nil {
		t.Errorf("empty key: got (%v, %v), want (nil, nil)", h, err)
	}
}

// Sessions are isolated: LoadHistory for one session must not leak
// items from a sibling session persisted in the same DB.
// Compaction boundary: rows before the latest compact_boundary are
// dropped and replaced with a synthetic [user-with-summary,
// assistant-ack] head. Rows after the boundary pass through verbatim.
// This is the resume-after-compaction code path; without it the loop
// would reload the full pre-compact history and balloon context again.
func TestStreamItemHistoryLoader_AppliesCompactBoundary(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		// Pre-compaction conversation (gets dropped).
		{ID: "u_old1", Kind: "user", Content: "old question"},
		{ID: "a_old1", Kind: "assistant", Content: "old answer"},
		{ID: "u_old2", Kind: "user", Content: "second old question"},
		{ID: "a_old2", Kind: "assistant", Content: "second old answer"},
		// The summary that the boundary points at.
		{ID: "sum_1", Kind: "compact_summary", Content: "SUMMARY-OF-OLD-CONVERSATION"},
		// Boundary row carries the summary id in its content JSON.
		{ID: "boundary_1", Kind: "compact_boundary", Content: `{"summary_id":"sum_1","total_tokens":150000,"context_limit":200000}`},
		// Post-compaction conversation (kept verbatim).
		{ID: "u_new1", Kind: "user", Content: "follow-up"},
		{ID: "a_new1", Kind: "assistant", Content: "follow-up answer"},
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	// Expected shape: [synthetic user with summary, synthetic assistant
	// ack, real user follow-up, real assistant answer]. No old rows.
	if len(got) != 4 {
		t.Fatalf("got %d items, want 4; actual=%+v", len(got), got)
	}
	if got[0].Kind != "user" || !contains(got[0].Content, "SUMMARY-OF-OLD-CONVERSATION") {
		t.Errorf("item[0] should embed summary text, got %+v", got[0])
	}
	if got[1].Kind != "assistant" {
		t.Errorf("item[1] should be assistant ack, got %+v", got[1])
	}
	if got[2].Kind != "user" || got[2].Content != "follow-up" {
		t.Errorf("item[2] = %+v, want user follow-up", got[2])
	}
	if got[3].Kind != "assistant" || got[3].Content != "follow-up answer" {
		t.Errorf("item[3] = %+v, want assistant follow-up answer", got[3])
	}
}

// When no boundary exists at all (legacy or never-compacted session),
// LoadHistory must behave exactly like before — return all rows
// unchanged. Regression guard for the most common path.
func TestStreamItemHistoryLoader_NoBoundaryReturnsAll(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "hi"},
		{ID: "a1", Kind: "assistant", Content: "hello"},
	})
	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	// Trailing user is dropped (it would be the current prompt) but
	// here the trailing row is assistant, so both pass through. The
	// shape and order must match the legacy behaviour exactly.
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2; actual=%+v", len(got), got)
	}
	if got[0].Kind != "user" || got[0].Content != "hi" {
		t.Errorf("item[0] = %+v, want user 'hi'", got[0])
	}
	if got[1].Kind != "assistant" || got[1].Content != "hello" {
		t.Errorf("item[1] = %+v, want assistant 'hello'", got[1])
	}
}

// Two boundaries in a session: only the latest matters. The earlier
// one is already represented inside the summary the latest one points
// at, so re-injecting it would duplicate context and waste tokens.
func TestStreamItemHistoryLoader_MultipleBoundariesUseLatest(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "ancient1"},
		{ID: "a1", Kind: "assistant", Content: "ancient2"},
		{ID: "sum_a", Kind: "compact_summary", Content: "ANCIENT-SUMMARY"},
		{ID: "b_a", Kind: "compact_boundary", Content: `{"summary_id":"sum_a"}`},
		{ID: "u2", Kind: "user", Content: "middle question"},
		{ID: "a2", Kind: "assistant", Content: "middle answer"},
		{ID: "sum_b", Kind: "compact_summary", Content: "RECENT-SUMMARY"},
		{ID: "b_b", Kind: "compact_boundary", Content: `{"summary_id":"sum_b"}`},
		{ID: "u3", Kind: "user", Content: "current"},
		{ID: "a3", Kind: "assistant", Content: "current answer"},
	})
	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	// Synthetic head pulls from the LATEST summary only.
	if len(got) == 0 || !contains(got[0].Content, "RECENT-SUMMARY") {
		t.Fatalf("head should embed RECENT-SUMMARY, got %+v", got)
	}
	if contains(got[0].Content, "ANCIENT-SUMMARY") {
		t.Errorf("head should NOT include ancient summary; got %+v", got[0])
	}
	// "middle" rows lived between the two boundaries; they're already
	// rolled into the recent summary, so they must not appear again.
	for _, it := range got {
		if it.Content == "middle question" || it.Content == "middle answer" {
			t.Errorf("middle rows leaked through latest boundary: %+v", it)
		}
	}
}

// Regression: compact_error must persist a boundary too, otherwise
// the in-memory dropOldestHalf takes effect for the active run but
// disappears on restart, immediately re-overflowing the context
// window and re-triggering compaction (which fails the same way) —
// a silent cascade that bricks the session.
//
// This test drives persistCompactionBoundary with the same arguments
// the streaming.go callback uses on a compact_error event (empty
// summary, since the summariser failed to produce one) and asserts
// that LoadHistory afterwards (a) drops the pre-boundary rows and
// (b) injects the fallback truncation notice as a synthetic user
// message so the model isn't told the run started fresh.
func TestStreamItemHistoryLoader_CompactErrorPersistsBoundary(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u_old", Kind: "user", Content: "old question"},
		{ID: "a_old", Kind: "assistant", Content: "old answer"},
	})

	// Simulate the streaming.go path on compact_error: persist the
	// boundary with an empty summary (the summariser produced nothing).
	app := &App{store: s}
	app.persistCompactionBoundary("s1", "")

	// A turn that runs after the compact_error has its rows persisted
	// after the boundary. These must survive the LoadHistory path.
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u_new", Kind: "user", Content: "follow-up"},
		{ID: "a_new", Kind: "assistant", Content: "follow-up answer"},
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	// Expected: [synthetic user with truncation notice, assistant ack,
	//            real user follow-up, real assistant answer].
	if len(got) != 4 {
		t.Fatalf("got %d items, want 4; actual=%+v", len(got), got)
	}
	if got[0].Kind != "user" || !contains(got[0].Content, "truncated") {
		t.Errorf("item[0] should embed truncation notice, got %+v", got[0])
	}
	if got[1].Kind != "assistant" {
		t.Errorf("item[1] should be assistant ack, got %+v", got[1])
	}
	if got[2].Kind != "user" || got[2].Content != "follow-up" {
		t.Errorf("item[2] = %+v, want user follow-up", got[2])
	}
	if got[3].Kind != "assistant" || got[3].Content != "follow-up answer" {
		t.Errorf("item[3] = %+v, want assistant follow-up answer", got[3])
	}
	for _, it := range got {
		if it.Content == "old question" || it.Content == "old answer" {
			t.Errorf("pre-boundary rows leaked through compact_error boundary: %+v", it)
		}
	}
}

// Boundary's referenced summary row missing (e.g. partial write or
// corrupt DB). LoadHistory should fall back to the most recent
// compact_summary before the boundary, not crash and not leak the
// pre-compact rows.
func TestStreamItemHistoryLoader_MissingSummaryFallback(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "old"},
		{ID: "sum_orphan", Kind: "compact_summary", Content: "ORPHAN-SUMMARY"},
		// Boundary refs an ID that doesn't exist; loader should fall
		// back to the latest pre-boundary compact_summary.
		{ID: "b1", Kind: "compact_boundary", Content: `{"summary_id":"does_not_exist"}`},
		{ID: "u2", Kind: "user", Content: "new"},
		{ID: "a2", Kind: "assistant", Content: "answer"},
	})
	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) == 0 || !contains(got[0].Content, "ORPHAN-SUMMARY") {
		t.Fatalf("expected fallback to use pre-boundary summary, got %+v", got)
	}
	for _, it := range got {
		if it.Content == "old" {
			t.Errorf("pre-boundary rows leaked: %+v", it)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		(len(haystack) > 0 && len(needle) > 0 && indexOf(haystack, needle) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestStreamItemHistoryLoader_SessionIsolation(t *testing.T) {
	s := newHistoryTestStore(t, "s1")
	ctx := context.Background()
	if err := s.SaveSession(ctx, storage.SessionRow{LocalID: "s2", ProjectID: "p1", Label: "S2", Model: "m"}); err != nil {
		t.Fatalf("SaveSession(s2): %v", err)
	}
	appendItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "s1-u1", Kind: "user", Content: "from s1"},
		{ID: "s1-a1", Kind: "assistant", Content: "reply s1"},
	})
	appendItems(t, s, "s2", []storage.StreamItemRow{
		{ID: "s2-u1", Kind: "user", Content: "from s2"},
		{ID: "s2-a1", Kind: "assistant", Content: "reply s2"},
	})

	loader := &streamItemHistoryLoader{store: s}
	got, err := loader.LoadHistory("s1")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	for _, h := range got {
		if h.Content == "from s2" || h.Content == "reply s2" {
			t.Errorf("leaked sibling session row: %+v", h)
		}
	}
}

func TestMobileOfferAdmittedHistoryRechecksWithdrawalAndCompaction(t *testing.T) {
	for _, withdrawn := range []bool{false, true} {
		name := "readable_control"
		if withdrawn {
			name = "withdrawn_after_admission"
		}
		t.Run(name, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			offer := mobileTestOffers(t, bridge)[0]
			refs := []mobileOfferReference{{ID: offer.ID, Version: offer.Version}}
			response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "history-source", offer.Prompt, refs)
			if response.Code != http.StatusAccepted {
				t.Fatalf("admission = %d %s", response.Code, response.Body.String())
			}
			mobileTestWaitFor(t, "source turn completes", func() bool { return turns.count() == 1 && !bridge.chatRunning("own-chat") })
			appendItems(t, bridge.app.store, "own-chat", []storage.StreamItemRow{
				{ID: "echo", Kind: "assistant", Content: "I used this source: " + offer.Prompt},
				{ID: "tool", Kind: "tool_use", ToolUseID: "source-tool", ToolName: "read", ToolInput: `{}`},
				{ID: "tool-result", Kind: "tool_result", ToolUseID: "source-tool", Content: offer.Prompt},
				{ID: "summary", Kind: "compact_summary", Content: "Earlier idea: " + offer.Prompt},
				{ID: "boundary", Kind: "compact_boundary", Content: `{"summary_id":"summary"}`},
				{ID: "later-user", Kind: "user", Content: "Tell me more"},
				{ID: "later-reply", Kind: "assistant", Content: "More about " + offer.Prompt},
			})
			if withdrawn {
				if err := bridge.app.activity.store.DeleteSession("idea-source-2"); err != nil {
					t.Fatal(err)
				}
			}
			history, err := (&streamItemHistoryLoader{store: bridge.app.store, sourceRows: bridge.app.mobileOfferHistoryRows}).LoadHistory("own-chat")
			if err != nil {
				t.Fatal(err)
			}
			nativeText := ""
			for _, item := range history {
				nativeText += item.Content + item.ToolInput
			}
			if strings.Contains(nativeText, offer.Prompt) == withdrawn {
				t.Fatalf("native history has source=%v withdrawn=%v: %+v", strings.Contains(nativeText, offer.Prompt), withdrawn, history)
			}
			if withdrawn && len(history) != 0 {
				t.Fatalf("dependent suffix/compact summary survived withdrawal: %+v", history)
			}
			read := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/own-chat", "")
			if read.Code != http.StatusOK {
				t.Fatalf("phone read=%d %s", read.Code, read.Body.String())
			}
			var detail mobileTestChatDetail
			if err := json.Unmarshal(mobileTestDecodeReply(t, read).Data, &detail); err != nil {
				t.Fatal(err)
			}
			phoneText := ""
			for _, item := range detail.Items {
				phoneText += item.Text
			}
			if strings.Contains(phoneText, offer.Prompt) == withdrawn {
				t.Fatalf("phone history has source=%v withdrawn=%v", strings.Contains(phoneText, offer.Prompt), withdrawn)
			}
		})
	}
}

func TestMobileOfferAdmittedSourcesOutliveWaitingOfferState(t *testing.T) {
	for _, lifecycle := range []string{"suggestion_dismissed", "suggestion_expired", "find_dismissed", "find_replaced"} {
		t.Run(lifecycle, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			index := 0
			if strings.HasPrefix(lifecycle, "find_") {
				index = 1
			}
			offer := mobileTestOffers(t, bridge)[index]
			refs := []mobileOfferReference{{ID: offer.ID, Version: offer.Version}}
			response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "history-lifecycle", offer.Prompt, refs)
			if response.Code != http.StatusAccepted {
				t.Fatalf("admission=%d %s", response.Code, response.Body.String())
			}
			mobileTestWaitFor(t, "accepted lifecycle turn", func() bool { return turns.count() == 1 && !bridge.chatRunning("own-chat") })
			switch lifecycle {
			case "suggestion_dismissed":
				if _, err := bridge.app.ResolveTodaySuggestion(TodaySuggestionDecision{ID: "idea", Decision: "dismiss"}); err != nil {
					t.Fatal(err)
				}
			case "suggestion_expired":
				if _, err := bridge.app.store.ExpireEngagementMoments(t.Context(), time.Now().Add(2*time.Hour)); err != nil {
					t.Fatal(err)
				}
			case "find_dismissed":
				if err := bridge.app.recordTopicFindFeedback(TopicFindFeedbackInput{FindID: "find-one", Action: "less"}); err != nil {
					t.Fatal(err)
				}
			case "find_replaced":
				if _, err := bridge.app.topicsSvc().store.update(func(state *topicsStateFile) { state.Finds = topicFindsState{} }); err != nil {
					t.Fatal(err)
				}
			}
			for _, current := range mobileTestOffers(t, bridge) {
				if current.ID == offer.ID {
					t.Fatal("fixture left offer waiting")
				}
			}
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
			if err != nil {
				t.Fatal(err)
			}
			filtered := bridge.app.mobileOfferHistoryRows(t.Context(), rows)
			if len(filtered) != len(rows) || len(filtered) != 2 || filtered[0].Content != offer.Prompt {
				t.Fatalf("readable admitted source depended on waiting state: %+v", filtered)
			}
			history, err := (&streamItemHistoryLoader{store: bridge.app.store, sourceRows: bridge.app.mobileOfferHistoryRows}).LoadHistory("own-chat")
			if err != nil || len(history) != 2 || history[0].Content != offer.Prompt {
				t.Fatalf("readable native history: %+v %v", history, err)
			}
		})
	}
}

func TestMobileOfferHistoryMissingProofCannotBeCompactedOrReplayed(t *testing.T) {
	for _, metadata := range []string{`{"offerReferences":[{"id":"suggestion.old","version":"missing"}]}`, `{"offerSources":null}`, `{"offerReferences":`, `{"offer\u0052eferences":null}`, `{"offer\u0053ources":`, `garbage {"\u006fferReferences":[]}`, `{"broken":, "\u006fferSources":[]}`, `{"OfferReferences":null}`, `{"OFFERSOURCES":null}`} {
		t.Run(metadata, func(t *testing.T) {
			store := newHistoryTestStore(t, "source-history")
			appendItems(t, store, "source-history", []storage.StreamItemRow{
				{ID: "intro", Kind: "user", Content: "My own message"},
				{ID: "intro-reply", Kind: "assistant", Content: "Hello"},
				{ID: "idea", Kind: "user", Content: "Private source text", AutomationContext: metadata},
				{ID: "echo", Kind: "assistant", Content: "Private source text"},
				{ID: "summary", Kind: "compact_summary", Content: "Private source text"},
				{ID: "boundary", Kind: "compact_boundary", Content: `{"summary_id":"summary"}`},
				{ID: "later-user", Kind: "user", Content: "More"},
				{ID: "later-reply", Kind: "assistant", Content: "Private source text"},
			})
			history, err := (&streamItemHistoryLoader{store: store}).LoadHistory("source-history")
			if err != nil || len(history) != 2 || history[0].Content != "My own message" || history[1].Content != "Hello" {
				t.Fatalf("missing source resolver failed open: %+v %v", history, err)
			}
		})
	}
}

func TestMobileOfferHistoryRedactsInheritedReplyAndToolText(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	offer := mobileTestOffers(t, bridge)[0]
	text, metadata, err := bridge.app.mobileOfferChatAdmission(t.Context(), offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
	if err != nil {
		t.Fatal(err)
	}
	rows := []storage.StreamItemRow{
		{ID: "source", Kind: "user", Content: text, AutomationContext: metadata},
		{ID: "reply", Kind: "assistant", Content: "Use " + activityPrivacySecret},
		{ID: "tool", Kind: "tool_use", ToolInput: `{"secret":"` + activityPrivacySecret + `"}`},
	}
	filtered := bridge.app.mobileOfferHistoryRows(t.Context(), rows)
	if len(filtered) != len(rows) || strings.Contains(filtered[1].Content, activityPrivacySecret) || strings.Contains(filtered[2].ToolInput, activityPrivacySecret) {
		t.Fatalf("inherited source content was not redacted: %+v", filtered)
	}
}

func TestMobileOfferHistoryRejectsLateSourceAndPolicyChanges(t *testing.T) {
	for _, mutation := range []string{"unchanged", "protect_activity", "main_policy", "source_file"} {
		t.Run(mutation, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			offer := mobileTestOffers(t, bridge)[0]
			text, metadata, err := bridge.app.mobileOfferChatAdmission(t.Context(), offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
			if err != nil {
				t.Fatal(err)
			}
			rows := []storage.StreamItemRow{
				{ID: "plain", Kind: "user", Content: "My own text"},
				{ID: "source", Kind: "user", Content: text, AutomationContext: metadata},
				{ID: "echo", Kind: "assistant", Content: text},
			}
			originalMail := mobileGoalMailRows
			t.Cleanup(func() { mobileGoalMailRows = originalMail })
			mailReads := 0
			mobileGoalMailRows = func(reader *App) ([]storage.MailMessageRow, error) {
				mailReads++
				result, err := originalMail(reader)
				if mailReads == 1 {
					switch mutation {
					case "protect_activity":
						if err := bridge.app.activity.store.UpdateTriageStatus("idea-source-2", "protected"); err != nil {
							t.Fatal(err)
						}
					case "main_policy":
						if err := bridge.app.store.SetUIState(t.Context(), permissionUIStateKey("screen_recording"), "false"); err != nil {
							t.Fatal(err)
						}
					case "source_file":
						dir, err := knowledgeDir()
						if err != nil {
							t.Fatal(err)
						}
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, "late-history-source.md"), []byte("changed"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				return result, err
			}
			filtered := bridge.app.mobileOfferHistoryRows(t.Context(), rows)
			mobileGoalMailRows = originalMail
			want := 1
			if mutation == "unchanged" {
				want = 3
			}
			if len(filtered) != want || mailReads != 1 {
				t.Fatalf("late mutation %s: rows=%+v, mail reads=%d", mutation, filtered, mailReads)
			}
		})
	}
}

func TestMobileOfferHistoryHoldsMailBeforeRedactingDescendants(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	offer := mobileTestOffers(t, bridge)[0]
	text, metadata, err := bridge.app.mobileOfferChatAdmission(t.Context(), offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
	if err != nil {
		t.Fatal(err)
	}
	originalMail := mobileGoalMailRows
	t.Cleanup(func() { mobileGoalMailRows = originalMail })
	mailReads := 0
	mobileGoalMailRows = func(*App) ([]storage.MailMessageRow, error) {
		mailReads++
		return []storage.MailMessageRow{{Subject: "Private mail " + activityPrivacySecret}}, nil
	}
	rows := []storage.StreamItemRow{
		{ID: "source", Kind: "user", Content: text, AutomationContext: metadata},
		{ID: "safe", Kind: "assistant", Content: "Safe reply"},
		{ID: "mail", Kind: "assistant", Content: "Private mail " + activityPrivacySecret},
		{ID: "echo", Kind: "assistant", Content: "Dependent reply"},
	}
	filtered := bridge.app.mobileOfferHistoryRows(t.Context(), rows)
	if len(filtered) != 2 || mailReads != 1 {
		t.Fatalf("mail hold applied after redaction or repeatedly: rows=%+v reads=%d", filtered, mailReads)
	}
}

func TestMobileOfferMetadataClassifiesEscapedKeysWithoutMatchingProse(t *testing.T) {
	for _, raw := range []string{`{"output":"offerReferences"}`, `{"output":"offerSources"}`, `{"handoff":true}`, `{"output":"ordinary","jobId":"job"}`, "", " \n\t", "null"} {
		if mobileOfferMetadataPresent(raw) {
			t.Fatalf("ordinary context became a source dependency: %s", raw)
		}
	}
	for _, raw := range []string{`{"offer\u0052eferences":null}`, `{"offer\u0053ources":`, `{"offerReferences":null}`, `{"offerSources":[]}`, `garbage {"\u006fferReferences":[]}`, `{"broken":, "\u006fferSources":[]}`, `invalid-metadata`, `{"OfferReferences":null}`, `{"OFFERSOURCES":null}`} {
		if !mobileOfferMetadataPresent(raw) {
			t.Fatalf("escaped or malformed source dependency disappeared: %s", raw)
		}
	}
}

func TestMobileOfferHistoryChecksDecodedToolArguments(t *testing.T) {
	const subject = "Quarterly R&D acquisition"
	for _, scenario := range []string{"readable_control", "held_subject", "nested_subject", "subject_key", "escaped_secret", "encoded_subject", "encoded_secret", "malformed", "duplicate_subject", "escaped_duplicate_key", "nested_duplicate_object", "encoded_duplicate_document", "duplicate_secret"} {
		t.Run(scenario, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			offer := mobileTestOffers(t, bridge)[0]
			response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "decoded-tool", offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
			if response.Code != http.StatusAccepted {
				t.Fatalf("admission = %d %s", response.Code, response.Body.String())
			}
			mobileTestWaitFor(t, "accepted source completes", func() bool { return turns.count() == 1 && !bridge.chatRunning("own-chat") })
			if _, err := bridge.app.store.UpsertMailMessages(t.Context(), []storage.MailMessageRow{{ID: "tool-mail", Mailbox: "inbox", Subject: subject, At: time.Now().UnixMilli()}}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			input := `{ "count":9007199254740993, "nested":[{"label":"Safe & sound"}], "payload":"{\"label\": \"Safe & sound\"}" }`
			denied := false
			switch scenario {
			case "held_subject":
				raw, _ := json.Marshal(map[string]any{"subject": subject})
				input = string(raw)
				denied = true
			case "nested_subject":
				raw, _ := json.Marshal(map[string]any{"nested": []any{map[string]any{"subject": subject}}})
				input = string(raw)
				denied = true
			case "subject_key":
				raw, _ := json.Marshal(map[string]any{subject: "value"})
				input = string(raw)
				denied = true
			case "escaped_secret":
				encoded := `\u0073` + activityPrivacySecret[1:]
				input = `{"token":"` + encoded + `","nested":[{"value":"` + encoded + `"}],"count":9007199254740993}`
			case "encoded_subject":
				nested, _ := json.Marshal(map[string]any{"subject": subject})
				raw, _ := json.Marshal(map[string]any{"payload": string(nested)})
				input = string(raw)
				denied = true
			case "encoded_secret":
				nested := `{"token":"\u0073` + activityPrivacySecret[1:] + `"}`
				raw, _ := json.Marshal(map[string]any{"payload": nested, "count": json.Number("9007199254740993")})
				input = string(raw)
			case "malformed":
				input = `{"subject":"Quarterly R\u0026D acquisition"`
				denied = true
			case "duplicate_subject":
				input = `{"subject":"Quarterly R\u0026D acquisition","subject":"Public update"}`
				denied = true
			case "escaped_duplicate_key":
				input = `{"a":"Quarterly R\u0026D acquisition","\u0061":"Public update"}`
				denied = true
			case "nested_duplicate_object":
				input = `{"nested":[{"subject":"Quarterly R\u0026D acquisition","subject":"Public update"}]}`
				denied = true
			case "encoded_duplicate_document":
				nested := `{"a":"Quarterly R\u0026D acquisition","\u0061":"Public update"}`
				raw, _ := json.Marshal(map[string]any{"payload": nested})
				input = string(raw)
				denied = true
			case "duplicate_secret":
				input = `{"token":"\u0073` + activityPrivacySecret[1:] + `","token":"Public token"}`
				denied = true
			}
			appendItems(t, bridge.app.store, "own-chat", []storage.StreamItemRow{
				{ID: "decoded-tool", Kind: "tool", ToolName: "Read", ToolUseID: "decoded-tool-use", ToolInput: input},
				{ID: "decoded-result", Kind: "result", ToolUseID: "decoded-tool-use", Content: "Tool result"},
				{ID: "later", Kind: "assistant", Content: "Later reply"},
			})
			history, err := (&streamItemHistoryLoader{store: bridge.app.store, sourceRows: bridge.app.mobileOfferHistoryRows}).LoadHistory("own-chat")
			if err != nil {
				t.Fatal(err)
			}
			tools := 0
			for _, row := range history {
				if row.Kind != "tool_use" {
					continue
				}
				tools++
				var decoded any
				if err := json.Unmarshal([]byte(row.ToolInput), &decoded); err != nil {
					t.Fatalf("invalid re-encoded arguments: %v", err)
				}
				if strings.Contains(fmt.Sprint(decoded), subject) || strings.Contains(fmt.Sprint(decoded), activityPrivacySecret) {
					t.Fatalf("decoded private argument leaked: %+v", decoded)
				}
				if scenario == "readable_control" && row.ToolInput != input {
					t.Fatal("unchanged arguments were unnecessarily re-encoded")
				}
				if (scenario == "escaped_secret" || scenario == "encoded_secret") && !strings.Contains(row.ToolInput, "9007199254740993") {
					t.Fatal("redaction changed numeric argument precision")
				}
			}
			wantTools := 1
			if denied {
				wantTools = 0
			}
			if tools != wantTools {
				t.Fatalf("tool history count=%d want=%d: %+v", tools, wantTools, history)
			}
			read := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/own-chat", "")
			if read.Code != http.StatusOK {
				t.Fatalf("phone read=%d", read.Code)
			}
			var detail mobileTestChatDetail
			if err := json.Unmarshal(mobileTestDecodeReply(t, read).Data, &detail); err != nil {
				t.Fatal(err)
			}
			phoneTools := 0
			for _, row := range detail.Items {
				if row.Kind == "tool_use" {
					phoneTools++
				}
				if strings.Contains(row.Text, subject) || strings.Contains(row.Text, activityPrivacySecret) {
					t.Fatalf("phone leaked decoded argument: %+v", row)
				}
			}
			if phoneTools != wantTools {
				t.Fatalf("phone tool count=%d want=%d", phoneTools, wantTools)
			}
			if scenario != "readable_control" {
				if _, _, err := bridge.app.sourceBoundChatHistory(t.Context(), "own-chat"); err == nil {
					t.Fatal("changed or private tool history remained dispatchable")
				}
			}
		})
	}
}

func TestMobileOfferHistoryOrdinaryDuplicateToolKeysRemainUnchanged(t *testing.T) {
	for _, input := range []string{`{"a":"first","a":"last"}`, `{"a":"first","\u0061":"last"}`} {
		t.Run(input, func(t *testing.T) {
			store := newHistoryTestStore(t, "ordinary-tool-history")
			appendItems(t, store, "ordinary-tool-history", []storage.StreamItemRow{
				{ID: "question", Kind: "user", Content: "My own request"},
				{ID: "tool", Kind: "tool", ToolName: "Read", ToolUseID: "ordinary-tool", ToolInput: input},
				{ID: "result", Kind: "result", ToolUseID: "ordinary-tool", Content: "Result"},
				{ID: "reply", Kind: "assistant", Content: "Done"},
			})
			history, err := (&streamItemHistoryLoader{store: store}).LoadHistory("ordinary-tool-history")
			if err != nil || len(history) != 4 || history[1].ToolInput != input {
				t.Fatalf("ordinary history changed: %+v %v", history, err)
			}
		})
	}
}
