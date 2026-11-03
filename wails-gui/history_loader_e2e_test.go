package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
	"yansu-agent/agent/native"

	"wails-gui/storage"
)

// Source dispatch tests use the real isolated offer admission and stores, then
// a task recorder at the runner boundary. No provider or application is started.
func admittedSourceDispatchFixture(t *testing.T) (*mobileBridge, mobileOffer) {
	t.Helper()
	bridge := mobileTestOfferBridge(t)
	turns := mobileTestOfferRunner(t, bridge)
	offer := mobileTestOffers(t, bridge)[0]
	refs := []mobileOfferReference{{ID: offer.ID, Version: offer.Version}}
	response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "source-dispatch-first", offer.Prompt, refs)
	if response.Code != http.StatusAccepted {
		t.Fatalf("admission: %d %s", response.Code, response.Body.String())
	}
	mobileTestWaitFor(t, "source turn completes", func() bool {
		return turns.count() == 1 && !bridge.chatRunning("own-chat")
	})
	return bridge, offer
}

func TestSourceBoundChatDispatchRefusesCachedProviderHistoryAfterWithdrawal(t *testing.T) {
	bridge, _ := admittedSourceDispatchFixture(t)
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	bridge.app.runner = runner
	if err := bridge.app.activity.store.DeleteSession("idea-source-2"); err != nil {
		t.Fatal(err)
	}
	err := bridge.app.executePrompt("Continue", t.TempDir(), "m", "provider-with-cached-source", "own-chat", nil, false, false, "", nil,
		func(prompt, _ string) string { return prompt }, "", "", promptOriginMac)
	if !errors.Is(err, errChatSourceAccessChanged) {
		t.Fatalf("withdrawn cached-provider admission = %v", err)
	}
	select {
	case <-runner.inputs:
		t.Fatal("withdrawn source reached the provider's cached session")
	default:
	}
}

func TestSourceBoundChatDispatchRechecksAfterDeferredComposition(t *testing.T) {
	bridge, _ := admittedSourceDispatchFixture(t)
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	bridge.app.runner = runner
	composed := make(chan error, 1)
	err := bridge.app.executePrompt("Continue", t.TempDir(), "m", "cached-provider", "own-chat", nil, false, false, "", nil,
		func(prompt, _ string) string {
			composed <- bridge.app.activity.store.DeleteSession("idea-source-2")
			return prompt
		}, "", "", promptOriginMac)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-composed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deferred composition was not resolved before dispatch")
	}
	waitForTagIdle(t, bridge.app, "own-chat")
	select {
	case <-runner.inputs:
		t.Fatal("source withdrawn during composition reached ExecuteTask")
	default:
	}
}

func TestSourceBoundChatDispatchRechecksAfterRunnerInitialization(t *testing.T) {
	bridge, _ := admittedSourceDispatchFixture(t)
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	bridge.app.runner = nil
	bridge.app.readyCh = make(chan struct{})
	if err := bridge.app.executePrompt("Continue", t.TempDir(), "m", "cached-provider", "own-chat", nil, false, false, "", nil,
		func(prompt, _ string) string { return prompt }, "", "", promptOriginMac); err != nil {
		t.Fatal(err)
	}
	if err := bridge.app.activity.store.DeleteSession("idea-source-2"); err != nil {
		t.Fatal(err)
	}
	bridge.app.mu.Lock()
	bridge.app.runner = runner
	close(bridge.app.readyCh)
	bridge.app.mu.Unlock()
	waitForTagIdle(t, bridge.app, "own-chat")
	select {
	case <-runner.inputs:
		t.Fatal("source withdrawn during runner initialization reached ExecuteTask")
	default:
	}
}

func TestSourceBoundChatDispatchCarriesOnlyGuardedHistory(t *testing.T) {
	bridge, offer := admittedSourceDispatchFixture(t)
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	bridge.app.runner = runner
	if err := bridge.app.executePrompt("Continue", t.TempDir(), "m", "cached-provider", "own-chat", nil, false, false, "", nil,
		func(prompt, _ string) string { return prompt }, "", "", promptOriginMac); err != nil {
		t.Fatal(err)
	}
	var dispatched *agent.TaskInput
	select {
	case input := <-runner.inputs:
		dispatched = input
		if input.PromptFunc != nil || input.BeforeDispatch == nil || !input.RequireNativeDispatch || input.Prompt != "Continue" || !strings.Contains(input.History, offer.Prompt) {
			t.Fatalf("source context was not guarded before dispatch: prompt=%q deferred=%v history=%q", input.Prompt, input.PromptFunc != nil, input.History)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readable source did not dispatch")
	}
	waitForTagIdle(t, bridge.app, "own-chat")
	if err := bridge.app.activity.store.DeleteSession("idea-source-2"); err != nil {
		t.Fatal(err)
	}
	if err := dispatched.BeforeDispatch(t.Context()); !errors.Is(err, errChatSourceAccessChanged) {
		t.Fatalf("provider boundary reused stale source authority: %v", err)
	}
}

func TestSourceBoundChatDispatchRefusesNewlySensitiveHistoryAndCurrentText(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprint(current), func(t *testing.T) {
			bridge, _ := admittedSourceDispatchFixture(t)
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
			if err != nil || len(rows) == 0 {
				t.Fatalf("admitted rows: %v %v", rows, err)
			}
			text := "Use the credential sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890"
			if current {
				if _, err := bridge.app.store.AppendStreamItem(t.Context(), "own-chat", storage.StreamItemRow{
					ID: "current-source", Kind: "user", Content: "Continue", AutomationContext: rows[0].AutomationContext,
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := bridge.app.store.AppendStreamItem(t.Context(), "own-chat", storage.StreamItemRow{
					ID: "derived-sensitive", Kind: "assistant", Content: text,
				}); err != nil {
					t.Fatal(err)
				}
				text = "Continue"
			}
			err = bridge.app.executePrompt(text, t.TempDir(), "m", "cached-provider", "own-chat", nil, false, false, "", nil,
				func(prompt, _ string) string { return prompt }, "", "", promptOriginMac)
			if !errors.Is(err, errChatSourceAccessChanged) {
				t.Fatalf("newly sensitive source context admitted: %v", err)
			}
		})
	}
}

// ─── helpers ───────────────────────────────────────────────────────────

func newE2EStore(t *testing.T, sessions ...string) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.New(filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	if err := s.SaveProject(ctx, storage.ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	for _, sid := range sessions {
		if err := s.SaveSession(ctx, storage.SessionRow{LocalID: sid, ProjectID: "p1", Label: sid, Model: "m"}); err != nil {
			t.Fatalf("SaveSession(%s): %v", sid, err)
		}
	}
	return s
}

func persistItems(t *testing.T, s *storage.Store, sessionID string, items []storage.StreamItemRow) {
	t.Helper()
	ctx := context.Background()
	for _, it := range items {
		if _, err := s.AppendStreamItem(ctx, sessionID, it); err != nil {
			t.Fatalf("AppendStreamItem(%s): %v", it.ID, err)
		}
	}
}

func loadAndBuild(t *testing.T, s *storage.Store, sessionID string) []native.Message {
	t.Helper()
	loader := &streamItemHistoryLoader{store: s}
	history, err := loader.LoadHistory(sessionID)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	msgs, err := native.BuildMessages(history)
	if err != nil {
		t.Fatalf("BuildMessages: %v", err)
	}
	return msgs
}

func blocks(m native.Message) []native.ContentBlock {
	b, ok := m.Content.([]native.ContentBlock)
	if !ok {
		return nil
	}
	return b
}

// ─── E2E: full pipeline tests ──────────────────────────────────────────

// Single-turn tool_use/tool_result: persist → load → build → verify that
// the final Anthropic message array has properly paired tool blocks.
func TestE2E_SingleToolCallPairing(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "read /tmp/hello.txt"},
		{ID: "a1", Kind: "assistant", Content: "I'll read that file."},
		{ID: "t1", Kind: "tool_use", ToolName: "Read", ToolInput: `{"path":"/tmp/hello.txt"}`, ToolUseID: "toolu_read01"},
		{ID: "r1", Kind: "tool_result", Content: "Hello, world!", ToolName: "Read", ToolUseID: "toolu_read01"},
		{ID: "a2", Kind: "assistant", Content: "The file contains: Hello, world!"},
		{ID: "u2", Kind: "user", Content: "thanks"}, // trailing, dropped by loader
	})

	msgs := loadAndBuild(t, s, "s1")

	// Expected: user → assistant(text + tool_use) → user(tool_result) → assistant
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(msgs), msgs)
	}

	// msg[0]: user "read /tmp/hello.txt"
	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want user", msgs[0].Role)
	}
	b0 := blocks(msgs[0])
	if len(b0) != 1 || b0[0].Type != "text" || b0[0].Text != "read /tmp/hello.txt" {
		t.Errorf("msgs[0] blocks = %+v", b0)
	}

	// msg[1]: assistant with text "I'll read..." + tool_use block
	if msgs[1].Role != "assistant" {
		t.Errorf("msgs[1].Role = %q, want assistant", msgs[1].Role)
	}
	b1 := blocks(msgs[1])
	if len(b1) != 2 {
		t.Fatalf("msgs[1] should have 2 blocks (text + tool_use), got %d: %+v", len(b1), b1)
	}
	if b1[0].Type != "text" || b1[0].Text != "I'll read that file." {
		t.Errorf("msgs[1][0] = %+v, want text", b1[0])
	}
	if b1[1].Type != "tool_use" || b1[1].ID != "toolu_read01" || b1[1].Name != "Read" {
		t.Errorf("msgs[1][1] = %+v, want tool_use Read/toolu_read01", b1[1])
	}

	// msg[2]: user with tool_result block
	if msgs[2].Role != "user" {
		t.Errorf("msgs[2].Role = %q, want user", msgs[2].Role)
	}
	b2 := blocks(msgs[2])
	if len(b2) != 1 || b2[0].Type != "tool_result" || b2[0].ToolUseID != "toolu_read01" {
		t.Errorf("msgs[2] blocks = %+v, want tool_result with toolu_read01", b2)
	}
	if b2[0].Content != "Hello, world!" {
		t.Errorf("tool_result content = %q, want %q", b2[0].Content, "Hello, world!")
	}

	// msg[3]: assistant final reply
	if msgs[3].Role != "assistant" {
		t.Errorf("msgs[3].Role = %q, want assistant", msgs[3].Role)
	}
	b3 := blocks(msgs[3])
	if len(b3) != 1 || b3[0].Text != "The file contains: Hello, world!" {
		t.Errorf("msgs[3] blocks = %+v", b3)
	}
}

// Multi-turn with multiple tool calls in the same assistant turn.
func TestE2E_MultipleToolCallsSameTurn(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "list files then read README"},
		{ID: "a1", Kind: "assistant", Content: "Sure, I'll do both."},
		{ID: "t1", Kind: "tool_use", ToolName: "Bash", ToolInput: `{"command":"ls"}`, ToolUseID: "toolu_ls01"},
		{ID: "t2", Kind: "tool_use", ToolName: "Read", ToolInput: `{"path":"README.md"}`, ToolUseID: "toolu_read02"},
		{ID: "r1", Kind: "tool_result", Content: "file1\nfile2\nREADME.md", ToolName: "Bash", ToolUseID: "toolu_ls01"},
		{ID: "r2", Kind: "tool_result", Content: "# Project\nA description.", ToolName: "Read", ToolUseID: "toolu_read02"},
		{ID: "a2", Kind: "assistant", Content: "Found 3 files. README says: A description."},
		{ID: "u2", Kind: "user", Content: "got it"}, // trailing
	})

	msgs := loadAndBuild(t, s, "s1")

	// Expected: user → assistant(text + 2 tool_use) → user(2 tool_result) → assistant
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(msgs), msgs)
	}

	// msg[1]: assistant with text + 2 tool_use blocks
	b1 := blocks(msgs[1])
	if len(b1) != 3 {
		t.Fatalf("msgs[1] blocks = %d, want 3 (text + 2 tool_use): %+v", len(b1), b1)
	}
	if b1[1].Type != "tool_use" || b1[1].ID != "toolu_ls01" {
		t.Errorf("msgs[1][1] = %+v, want tool_use ls01", b1[1])
	}
	if b1[2].Type != "tool_use" || b1[2].ID != "toolu_read02" {
		t.Errorf("msgs[1][2] = %+v, want tool_use read02", b1[2])
	}

	// msg[2]: user with 2 tool_result blocks
	b2 := blocks(msgs[2])
	if len(b2) != 2 {
		t.Fatalf("msgs[2] blocks = %d, want 2 tool_results: %+v", len(b2), b2)
	}
	if b2[0].Type != "tool_result" || b2[0].ToolUseID != "toolu_ls01" {
		t.Errorf("msgs[2][0] = %+v, want tool_result ls01", b2[0])
	}
	if b2[1].Type != "tool_result" || b2[1].ToolUseID != "toolu_read02" {
		t.Errorf("msgs[2][1] = %+v, want tool_result read02", b2[1])
	}
}

// Multi-turn conversation: turn 1 has tool calls, turn 2 has more tool calls.
// Verifies history survives across turns and message array alternates correctly.
func TestE2E_MultiTurnWithToolCalls(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		// Turn 1
		{ID: "u1", Kind: "user", Content: "read config.yaml"},
		{ID: "a1", Kind: "assistant", Content: "reading"},
		{ID: "t1", Kind: "tool_use", ToolName: "Read", ToolInput: `{"path":"config.yaml"}`, ToolUseID: "toolu_t1"},
		{ID: "r1", Kind: "tool_result", Content: "port: 8080", ToolName: "Read", ToolUseID: "toolu_t1"},
		{ID: "a2", Kind: "assistant", Content: "Config says port 8080"},
		// Turn 2
		{ID: "u2", Kind: "user", Content: "change port to 9090"},
		{ID: "a3", Kind: "assistant", Content: "updating"},
		{ID: "t2", Kind: "tool_use", ToolName: "Edit", ToolInput: `{"path":"config.yaml","content":"port: 9090"}`, ToolUseID: "toolu_t2"},
		{ID: "r2", Kind: "tool_result", Content: "done", ToolName: "Edit", ToolUseID: "toolu_t2"},
		{ID: "a4", Kind: "assistant", Content: "Updated to port 9090"},
		// Current turn (trailing, dropped)
		{ID: "u3", Kind: "user", Content: "verify it"},
	})

	msgs := loadAndBuild(t, s, "s1")

	// Should produce 8 messages total:
	// user → assistant(text+tool_use) → user(tool_result) → assistant →
	// user → assistant(text+tool_use) → user(tool_result) → assistant
	if len(msgs) != 8 {
		t.Fatalf("got %d messages, want 8: %+v", len(msgs), msgs)
	}

	// Verify role alternation
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = m.Role
	}
	wantRoles := []string{"user", "assistant", "user", "assistant", "user", "assistant", "user", "assistant"}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Errorf("roles = %v, want %v", roles, wantRoles)
	}

	// Turn 1 tool pairing
	b1t := blocks(msgs[1])
	if len(b1t) < 2 || b1t[len(b1t)-1].Type != "tool_use" || b1t[len(b1t)-1].ID != "toolu_t1" {
		t.Errorf("turn 1 assistant missing tool_use: %+v", b1t)
	}
	b1r := blocks(msgs[2])
	if len(b1r) != 1 || b1r[0].Type != "tool_result" || b1r[0].ToolUseID != "toolu_t1" {
		t.Errorf("turn 1 tool_result wrong: %+v", b1r)
	}

	// Turn 2 tool pairing
	b2t := blocks(msgs[5])
	if len(b2t) < 2 || b2t[len(b2t)-1].Type != "tool_use" || b2t[len(b2t)-1].ID != "toolu_t2" {
		t.Errorf("turn 2 assistant missing tool_use: %+v", b2t)
	}
	b2r := blocks(msgs[6])
	if len(b2r) != 1 || b2r[0].Type != "tool_result" || b2r[0].ToolUseID != "toolu_t2" {
		t.Errorf("turn 2 tool_result wrong: %+v", b2r)
	}
}

// Frontend persists kind="tool" and kind="result" (not "tool_use"/"tool_result").
// The full pipeline must normalize these and produce valid Anthropic messages.
func TestE2E_FrontendKindAliases(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "run ls"},
		{ID: "a1", Kind: "assistant", Content: "ok"},
		{ID: "t1", Kind: "tool", ToolName: "Bash", ToolInput: `{"command":"ls"}`, ToolUseID: "toolu_fe_01"},
		{ID: "r1", Kind: "result", Content: "a.txt\nb.txt", ToolName: "Bash", ToolUseID: "toolu_fe_01"},
		{ID: "a2", Kind: "assistant", Content: "Found a.txt and b.txt"},
		{ID: "u2", Kind: "user", Content: "done"}, // trailing
	})

	msgs := loadAndBuild(t, s, "s1")

	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(msgs), msgs)
	}

	// tool_use block should exist in assistant message
	b1 := blocks(msgs[1])
	if len(b1) < 2 || b1[1].Type != "tool_use" || b1[1].ID != "toolu_fe_01" || b1[1].Name != "Bash" {
		t.Errorf("msgs[1] blocks = %+v, want text + tool_use Bash/toolu_fe_01", b1)
	}

	// tool_result block should exist in user message
	b2 := blocks(msgs[2])
	if len(b2) != 1 || b2[0].Type != "tool_result" || b2[0].ToolUseID != "toolu_fe_01" {
		t.Errorf("msgs[2] blocks = %+v, want tool_result toolu_fe_01", b2)
	}
	if b2[0].Content != "a.txt\nb.txt" {
		t.Errorf("tool_result content = %q", b2[0].Content)
	}
}

// Legacy tool rows (no tool_use_id) are dropped by the loader, producing
// text-only messages. Verifies backward compatibility with pre-v38 data.
func TestE2E_LegacyToolRowsDropped(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "do something"},
		{ID: "t1", Kind: "tool_use", ToolName: "Bash", ToolInput: `{"cmd":"ls"}`}, // no ToolUseID
		{ID: "r1", Kind: "tool_result", Content: "output"},                        // no ToolUseID
		{ID: "a1", Kind: "assistant", Content: "done"},
	})

	msgs := loadAndBuild(t, s, "s1")

	// Only user + assistant survive (tools dropped)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (legacy tools dropped): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("roles = %s/%s, want user/assistant", msgs[0].Role, msgs[1].Role)
	}
	b0 := blocks(msgs[0])
	if b0[0].Text != "do something" {
		t.Errorf("user text = %q", b0[0].Text)
	}
	b1 := blocks(msgs[1])
	if b1[0].Text != "done" {
		t.Errorf("assistant text = %q", b1[0].Text)
	}
}

// Mixed legacy and v38 tool rows: legacy (no tool_use_id) dropped,
// v38 (with tool_use_id) preserved. Verifies migration coexistence.
func TestE2E_MixedLegacyAndV38ToolRows(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		// Turn 1: legacy (pre-v38, no tool_use_id)
		{ID: "u1", Kind: "user", Content: "old turn"},
		{ID: "t1", Kind: "tool_use", ToolName: "Bash", ToolInput: `{"cmd":"echo hi"}`},
		{ID: "r1", Kind: "tool_result", Content: "hi"},
		{ID: "a1", Kind: "assistant", Content: "said hi"},
		// Turn 2: v38 (with tool_use_id)
		{ID: "u2", Kind: "user", Content: "new turn"},
		{ID: "a2", Kind: "assistant", Content: "ok"},
		{ID: "t2", Kind: "tool_use", ToolName: "Read", ToolInput: `{"path":"/x"}`, ToolUseID: "toolu_v38"},
		{ID: "r2", Kind: "tool_result", Content: "data", ToolName: "Read", ToolUseID: "toolu_v38"},
		{ID: "a3", Kind: "assistant", Content: "read it"},
		{ID: "u3", Kind: "user", Content: "trailing"}, // dropped
	})

	msgs := loadAndBuild(t, s, "s1")

	// Turn 1: user + assistant (tools dropped)
	// Turn 2: user → assistant(text + tool_use) → user(tool_result) → assistant
	// Total: 6
	if len(msgs) != 6 {
		t.Fatalf("got %d messages, want 6: %+v", len(msgs), msgs)
	}

	// Turn 1: text only
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("turn 1 roles wrong: %s/%s", msgs[0].Role, msgs[1].Role)
	}
	b1 := blocks(msgs[1])
	if len(b1) != 1 || b1[0].Type != "text" {
		t.Errorf("turn 1 assistant should be text only: %+v", b1)
	}

	// Turn 2: has tool blocks
	b3 := blocks(msgs[3])
	if len(b3) < 2 || b3[1].Type != "tool_use" || b3[1].ID != "toolu_v38" {
		t.Errorf("turn 2 assistant missing tool_use: %+v", b3)
	}
	b4 := blocks(msgs[4])
	if len(b4) != 1 || b4[0].Type != "tool_result" || b4[0].ToolUseID != "toolu_v38" {
		t.Errorf("turn 2 tool_result wrong: %+v", b4)
	}
}

// Orphan tool_use (tool_result missing or not persisted) should get a
// synthetic "[interrupted]" tool_result from BuildMessages so the API
// doesn't reject with a 400.
func TestE2E_OrphanToolUseGetsSyntheticResult(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "run something"},
		{ID: "a1", Kind: "assistant", Content: "running"},
		{ID: "t1", Kind: "tool_use", ToolName: "Bash", ToolInput: `{"command":"sleep 999"}`, ToolUseID: "toolu_orphan"},
		// No tool_result — interrupted!
		{ID: "u2", Kind: "user", Content: "cancel that"},
		{ID: "a2", Kind: "assistant", Content: "cancelled"},
	})

	msgs := loadAndBuild(t, s, "s1")

	// BuildMessages should inject a synthetic tool_result for toolu_orphan
	var foundSynthetic bool
	for _, m := range msgs {
		for _, b := range blocks(m) {
			if b.Type == "tool_result" && b.ToolUseID == "toolu_orphan" && b.IsError {
				foundSynthetic = true
				if b.Content != "[interrupted]" {
					t.Errorf("synthetic content = %q, want %q", b.Content, "[interrupted]")
				}
			}
		}
	}
	if !foundSynthetic {
		t.Errorf("orphan tool_use toolu_orphan missing synthetic [interrupted] result: %+v", msgs)
	}

	// Verify final message is the "cancelled" assistant
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		t.Errorf("last message role = %q, want assistant", last.Role)
	}
	lb := blocks(last)
	if len(lb) != 1 || lb[0].Text != "cancelled" {
		t.Errorf("last message = %+v", lb)
	}
}

// Non-text kinds (thinking, system, error) are dropped by the loader,
// so BuildMessages never sees them. Verify end-to-end.
func TestE2E_NonTextKindsDropped(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "hi"},
		{ID: "th1", Kind: "thinking", Content: "let me think..."},
		{ID: "sys1", Kind: "system", Content: "system note"},
		{ID: "err1", Kind: "error", Content: "transient error"},
		{ID: "a1", Kind: "assistant", Content: "hello"},
	})

	msgs := loadAndBuild(t, s, "s1")

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (non-text dropped): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("roles = %s/%s", msgs[0].Role, msgs[1].Role)
	}
}

// Session isolation: items from session A must not appear in session B's
// history, even when persisted in the same database.
func TestE2E_SessionIsolation(t *testing.T) {
	s := newE2EStore(t, "s1", "s2")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "s1-u1", Kind: "user", Content: "session 1 msg"},
		{ID: "s1-a1", Kind: "assistant", Content: "session 1 reply"},
		{ID: "s1-t1", Kind: "tool_use", ToolName: "Bash", ToolInput: `{}`, ToolUseID: "toolu_s1"},
		{ID: "s1-r1", Kind: "tool_result", Content: "s1 output", ToolUseID: "toolu_s1"},
		{ID: "s1-a2", Kind: "assistant", Content: "s1 done"},
	})
	persistItems(t, s, "s2", []storage.StreamItemRow{
		{ID: "s2-u1", Kind: "user", Content: "session 2 msg"},
		{ID: "s2-a1", Kind: "assistant", Content: "session 2 reply"},
	})

	msgs1 := loadAndBuild(t, s, "s1")
	msgs2 := loadAndBuild(t, s, "s2")

	// Session 1 should have tool blocks
	if len(msgs1) < 4 {
		t.Fatalf("session 1: got %d messages, want >= 4: %+v", len(msgs1), msgs1)
	}

	// Session 2 should have only text
	if len(msgs2) != 2 {
		t.Fatalf("session 2: got %d messages, want 2: %+v", len(msgs2), msgs2)
	}

	// Verify no cross-contamination
	for _, m := range msgs2 {
		for _, b := range blocks(m) {
			if b.Text == "session 1 msg" || b.Text == "session 1 reply" || b.ToolUseID == "toolu_s1" {
				t.Errorf("session 2 contains session 1 data: %+v", b)
			}
		}
	}
}

// Tool input JSON is preserved through the full pipeline and correctly
// deserialized by BuildMessages into the Input field.
func TestE2E_ToolInputJSONPreserved(t *testing.T) {
	s := newE2EStore(t, "s1")
	input := `{"path":"/tmp/test","options":{"recursive":true,"depth":3}}`
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "deep read"},
		{ID: "a1", Kind: "assistant", Content: "ok"},
		{ID: "t1", Kind: "tool_use", ToolName: "Read", ToolInput: input, ToolUseID: "toolu_json01"},
		{ID: "r1", Kind: "tool_result", Content: "result data", ToolUseID: "toolu_json01"},
		{ID: "a2", Kind: "assistant", Content: "done"},
	})

	msgs := loadAndBuild(t, s, "s1")

	// Find the tool_use block and verify Input was deserialized
	b1 := blocks(msgs[1])
	toolBlock := b1[len(b1)-1]
	if toolBlock.Type != "tool_use" {
		t.Fatalf("expected tool_use, got %s", toolBlock.Type)
	}
	if toolBlock.Input == nil {
		t.Fatal("tool_use Input is nil, expected parsed JSON")
	}
	// Input should be a map
	inputMap, ok := toolBlock.Input.(map[string]any)
	if !ok {
		t.Fatalf("Input type = %T, want map[string]any", toolBlock.Input)
	}
	if inputMap["path"] != "/tmp/test" {
		t.Errorf("Input.path = %v, want /tmp/test", inputMap["path"])
	}
	opts, ok := inputMap["options"].(map[string]any)
	if !ok {
		t.Fatalf("Input.options type = %T, want map", inputMap["options"])
	}
	if opts["recursive"] != true {
		t.Errorf("Input.options.recursive = %v, want true", opts["recursive"])
	}
}

// Image-only user messages survive the pipeline. They have no text content
// but carry an Images JSON payload that BuildMessages parses into image blocks.
func TestE2E_ImageOnlyUserMessage(t *testing.T) {
	s := newE2EStore(t, "s1")
	imgJSON := `[{"media_type":"image/png","data_base64":"iVBORw0KGgo="}]`
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "", Images: imgJSON}, // image only
		{ID: "a1", Kind: "assistant", Content: "I see an image"},
	})

	msgs := loadAndBuild(t, s, "s1")

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(msgs), msgs)
	}

	// First message should have image block
	b0 := blocks(msgs[0])
	if len(b0) == 0 {
		t.Fatal("image-only user message has no blocks")
	}
	hasImage := false
	for _, b := range b0 {
		if b.Type == "image" && b.Source != nil && b.Source.MediaType == "image/png" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Errorf("image-only user message missing image block: %+v", b0)
	}
}

// Verify that a complex realistic scenario works: multiple turns with
// interleaved tool calls, image messages, and legacy items.
func TestE2E_RealisticMultiTurnConversation(t *testing.T) {
	s := newE2EStore(t, "s1")
	imgJSON := `[{"media_type":"image/jpeg","data_base64":"AAAA"}]`
	persistItems(t, s, "s1", []storage.StreamItemRow{
		// Turn 1: user sends screenshot, assistant reads a file
		{ID: "u1", Kind: "user", Content: "Here's a screenshot of the error", Images: imgJSON},
		{ID: "a1", Kind: "assistant", Content: "Let me check the code."},
		{ID: "t1", Kind: "tool_use", ToolName: "Read", ToolInput: `{"path":"main.go"}`, ToolUseID: "toolu_r1"},
		{ID: "r1", Kind: "tool_result", Content: "package main\nfunc main() {}", ToolName: "Read", ToolUseID: "toolu_r1"},
		{ID: "a2", Kind: "assistant", Content: "I see the issue. Let me fix it."},
		// Turn 2: assistant edits then runs tests
		{ID: "t2", Kind: "tool_use", ToolName: "Edit", ToolInput: `{"path":"main.go","content":"fixed"}`, ToolUseID: "toolu_e1"},
		{ID: "r2", Kind: "tool_result", Content: "saved", ToolName: "Edit", ToolUseID: "toolu_e1"},
		{ID: "t3", Kind: "tool_use", ToolName: "Bash", ToolInput: `{"command":"go test ./..."}`, ToolUseID: "toolu_b1"},
		{ID: "r3", Kind: "tool_result", Content: "PASS\nok main 0.01s", ToolName: "Bash", ToolUseID: "toolu_b1"},
		{ID: "a3", Kind: "assistant", Content: "Fixed and tests pass!"},
		// Turn 3: user confirms
		{ID: "u3", Kind: "user", Content: "great, commit it"},
		{ID: "a4", Kind: "assistant", Content: "committing"},
		{ID: "t4", Kind: "tool_use", ToolName: "Bash", ToolInput: `{"command":"git commit -am 'fix'"}`, ToolUseID: "toolu_b2"},
		{ID: "r4", Kind: "tool_result", Content: "[main abc123] fix", ToolName: "Bash", ToolUseID: "toolu_b2"},
		{ID: "a5", Kind: "assistant", Content: "Committed as abc123."},
		// Current turn (dropped)
		{ID: "u4", Kind: "user", Content: "push it"},
	})

	msgs := loadAndBuild(t, s, "s1")

	// Verify all messages have valid roles
	for i, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			t.Errorf("msgs[%d].Role = %q, must be user or assistant", i, m.Role)
		}
	}

	// Verify strict alternation (user/assistant/user/assistant...)
	// Adjacent same-role messages violate the Anthropic API contract.
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == msgs[i-1].Role {
			t.Errorf("msgs[%d] and msgs[%d] have same role %q — violates alternation",
				i-1, i, msgs[i].Role)
		}
	}

	// First message should have image block
	b0 := blocks(msgs[0])
	hasImage := false
	for _, b := range b0 {
		if b.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Error("first user message missing image block")
	}

	// Count total tool_use and tool_result blocks
	var toolUseCount, toolResultCount int
	for _, m := range msgs {
		for _, b := range blocks(m) {
			switch b.Type {
			case "tool_use":
				toolUseCount++
			case "tool_result":
				toolResultCount++
			}
		}
	}
	// 4 tool calls: Read, Edit, Bash (test), Bash (commit)
	if toolUseCount != 4 {
		t.Errorf("tool_use count = %d, want 4", toolUseCount)
	}
	if toolResultCount != 4 {
		t.Errorf("tool_result count = %d, want 4", toolResultCount)
	}

	// Every tool_use ID should have a matching tool_result
	toolUseIDs := make(map[string]bool)
	toolResultIDs := make(map[string]bool)
	for _, m := range msgs {
		for _, b := range blocks(m) {
			if b.Type == "tool_use" {
				toolUseIDs[b.ID] = true
			}
			if b.Type == "tool_result" {
				toolResultIDs[b.ToolUseID] = true
			}
		}
	}
	for id := range toolUseIDs {
		if !toolResultIDs[id] {
			t.Errorf("tool_use %s has no matching tool_result", id)
		}
	}
}

// Verify that the DB round-trip preserves tool_use_id values exactly —
// what goes in must come back out byte-for-byte.
func TestE2E_ToolUseIDRoundTrip(t *testing.T) {
	s := newE2EStore(t, "s1")

	ids := []string{
		"toolu_01J1234567890abcdef",
		"toolu_with-dashes-and_underscores",
		"toolu_abc",
	}

	items := []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "start"},
	}
	for i, id := range ids {
		items = append(items,
			storage.StreamItemRow{
				ID: fmt.Sprintf("a%d", i), Kind: "assistant", Content: fmt.Sprintf("calling tool %d", i),
			},
			storage.StreamItemRow{
				ID: fmt.Sprintf("t%d", i), Kind: "tool_use", ToolName: "Bash",
				ToolInput: `{"command":"echo"}`, ToolUseID: id,
			},
			storage.StreamItemRow{
				ID: fmt.Sprintf("r%d", i), Kind: "tool_result", Content: "ok",
				ToolName: "Bash", ToolUseID: id,
			},
		)
	}
	items = append(items, storage.StreamItemRow{ID: "af", Kind: "assistant", Content: "all done"})

	persistItems(t, s, "s1", items)
	msgs := loadAndBuild(t, s, "s1")

	// Collect all tool_use IDs from the built messages
	var gotIDs []string
	for _, m := range msgs {
		for _, b := range blocks(m) {
			if b.Type == "tool_use" {
				gotIDs = append(gotIDs, b.ID)
			}
		}
	}
	if !reflect.DeepEqual(gotIDs, ids) {
		t.Errorf("tool_use IDs = %v, want %v", gotIDs, ids)
	}

	// Verify matching tool_result IDs
	var gotResultIDs []string
	for _, m := range msgs {
		for _, b := range blocks(m) {
			if b.Type == "tool_result" {
				gotResultIDs = append(gotResultIDs, b.ToolUseID)
			}
		}
	}
	if !reflect.DeepEqual(gotResultIDs, ids) {
		t.Errorf("tool_result IDs = %v, want %v", gotResultIDs, ids)
	}
}

// Empty database produces empty messages (no panic, no error).
func TestE2E_EmptySession(t *testing.T) {
	s := newE2EStore(t, "s1")
	msgs := loadAndBuild(t, s, "s1")
	if len(msgs) != 0 {
		t.Errorf("empty session produced %d messages, want 0: %+v", len(msgs), msgs)
	}
}

// Verify the full pipeline handles the edge case where tool_result
// comes before its tool_use (shouldn't happen but defensive).
func TestE2E_ToolResultBeforeToolUse(t *testing.T) {
	s := newE2EStore(t, "s1")
	persistItems(t, s, "s1", []storage.StreamItemRow{
		{ID: "u1", Kind: "user", Content: "start"},
		{ID: "r1", Kind: "tool_result", Content: "premature result", ToolUseID: "toolu_early"},
		{ID: "a1", Kind: "assistant", Content: "ok"},
		{ID: "t1", Kind: "tool_use", ToolName: "Bash", ToolInput: `{}`, ToolUseID: "toolu_early"},
		{ID: "r2", Kind: "tool_result", Content: "proper result", ToolUseID: "toolu_early"},
		{ID: "a2", Kind: "assistant", Content: "done"},
	})

	msgs := loadAndBuild(t, s, "s1")

	// Should not panic; messages should be produced
	if len(msgs) == 0 {
		t.Fatal("expected non-empty messages")
	}

	// Verify no panic and roles are valid
	for i, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			t.Errorf("msgs[%d].Role = %q", i, m.Role)
		}
	}
}
