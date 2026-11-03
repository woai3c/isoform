package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

func topicRound2Send(t *testing.T, a *App, tag string) {
	t.Helper()
	r := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = r
	if _, err := a.SendMainChatWithTopic("topic question", "atlas"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.prompt:
	case <-time.After(5 * time.Second):
		t.Fatal("missing topic runner")
	}
	waitForTagIdle(t, a, tag)
}
func topicRound2OrdinaryTurn(t *testing.T, a *App, tag, session string) *agent.TaskInput {
	t.Helper()
	r := &topicHistoryCaptureRunner{upcomingCaptureRunner: upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}, checked: make(chan error, 1)}
	a.runner = r
	if err := a.executePrompt("ordinary question", "", "chosen-model", session, tag, nil, false, false, "en", nil, func(text, language string) string { return "composed: " + text }, "", "", promptOriginMac); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing ordinary runner")
	}
	input := <-r.inputs
	waitForTagIdle(t, a, tag)
	return input
}
func topicRound2OrdinaryRows(t *testing.T, a *App, tag string) {
	t.Helper()
	rows := []storage.StreamItemRow{
		{ID: "pre-summary", Kind: "assistant", Content: "represented by summary"},
		{ID: "summary", Kind: "compact_summary", Content: "ordinary compaction summary"},
		{ID: "boundary", Kind: "compact_boundary", Content: `{"summary_id":"summary"}`},
		{ID: "old-ordinary", Kind: "assistant", Content: "ordinary context older than thirty rows"},
		{ID: "old-tool", Kind: "tool_use", ToolName: "Read", ToolUseID: "old-call", ToolInput: `{"file_path":"fixture"}`},
		{ID: "old-result", Kind: "tool_result", ToolUseID: "old-call", Content: "ordinary tool context"},
	}
	for i := 0; i < 40; i++ {
		rows = append(rows, storage.StreamItemRow{ID: fmt.Sprint("ordinary-", i), Kind: "assistant", Content: fmt.Sprint("ordinary answer ", i)})
	}
	for _, row := range rows {
		if _, err := a.store.AppendStreamItem(t.Context(), tag, row); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTopicRound2PreservesOrdinaryHistoryAndCompaction(t *testing.T) {
	a, tag := topicChatTestApp(t)
	topicRound2OrdinaryRows(t, a, tag)
	topicRound2Send(t, a, tag)
	topicRound2OrdinaryTurn(t, a, tag, "native-session")
	// This is feat's native loader configuration. The recent topic is valid,
	// so adding its gate must leave every ordinary byte and compaction intact.
	baseline := &streamItemHistoryLoader{store: a.store, sourceRows: a.mobileOfferHistoryRows}
	expected, err := baseline.LoadHistory(tag)
	if err != nil {
		t.Fatal(err)
	}
	actual := &streamItemHistoryLoader{store: a.store, loadRows: a.modelChatHistoryRows}
	got, err := actual.LoadHistory(tag)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("native history changed after one topic: got %d entries, feat %d", len(got), len(expected))
	}
	raw, _ := json.Marshal(got)
	for _, text := range []string{"ordinary compaction summary", "ordinary context older than thirty rows", "ordinary tool context"} {
		if !strings.Contains(string(raw), text) {
			t.Fatalf("lost %q", text)
		}
	}
}
func TestTopicRound2PreservesSessionAcrossOrdinaryTurns(t *testing.T) {
	a, tag := topicChatTestApp(t)
	topicRound2Send(t, a, tag)
	for i := 0; i < 2; i++ {
		input := topicRound2OrdinaryTurn(t, a, tag, "native-todo-and-plan-session")
		if input.SessionID != "native-todo-and-plan-session" {
			t.Fatalf("turn %d reset native todo/plan identity: %q", i, input.SessionID)
		}
	}
}
func TestTopicRound2StaleSessionRotatesOnce(t *testing.T) {
	a, tag := topicChatTestApp(t)
	topicRound2Send(t, a, tag)
	writeTodayContextMemory(t, "atlas", "# Atlas\nCorrected after the topic run")
	first := topicRound2OrdinaryTurn(t, a, tag, "native-session")
	if first.SessionID == "" || first.SessionID == "native-session" {
		t.Fatalf("withdrawn session must rotate to a reusable identity: %q", first.SessionID)
	}
	// A stale renderer/phone still passing the old identity cannot reset state
	// or resume that withdrawn session on the next turn.
	second := topicRound2OrdinaryTurn(t, a, tag, "native-session")
	third := topicRound2OrdinaryTurn(t, a, tag, first.SessionID)
	if second.SessionID != first.SessionID || third.SessionID != first.SessionID {
		t.Fatalf("same withdrawal rotated repeatedly: %q %q %q", first.SessionID, second.SessionID, third.SessionID)
	}
	if first.RequireNativeDispatch || strings.Contains(first.History, "topic question") {
		t.Fatal("stale context constrained route or reached history")
	}
}
func TestTopicRound2NoTopicMatchesFeat(t *testing.T) {
	for _, session := range []string{"", "existing-native-session"} {
		t.Run(session, func(t *testing.T) {
			a, tag := topicChatTestApp(t)
			topicRound2OrdinaryRows(t, a, tag)
			rows, err := a.store.LoadStreamItems(t.Context(), tag)
			if err != nil {
				t.Fatal(err)
			}
			input := topicRound2OrdinaryTurn(t, a, tag, session)
			expectedHistory := ""
			if session != "" {
				expectedHistory = formatConversationHistory(recentChatConversationRows(rows, 30))
			}
			if input.Prompt != "composed: ordinary question" || input.History != expectedHistory || input.SessionID != session || input.RequireNativeDispatch {
				t.Fatalf("ordinary composition differs from feat: prompt=%q historyEqual=%v session=%q bound=%v", input.Prompt, input.History == expectedHistory, input.SessionID, input.RequireNativeDispatch)
			}
			baseline := &streamItemHistoryLoader{store: a.store, sourceRows: a.mobileOfferHistoryRows}
			expected, err := baseline.LoadHistory(tag)
			if err != nil {
				t.Fatal(err)
			}
			actual := &streamItemHistoryLoader{store: a.store, loadRows: a.modelChatHistoryRows}
			got, err := actual.LoadHistory(tag)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(expected)
			after, _ := json.Marshal(got)
			if string(before) != string(after) {
				t.Fatal("no-topic native history differs byte-for-byte from feat")
			}
		})
	}
}

func TestTopicRound2AgedTopicKeepsOrdinaryHistoryAndSession(t *testing.T) {
	a, tag := topicChatTestApp(t)
	topicRound2Send(t, a, tag)
	topicRound2OrdinaryRows(t, a, tag)
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	expected := []storage.StreamItemRow{}
	for _, row := range rows {
		if topicChatRowProof(row) == "" {
			expected = append(expected, row)
		}
	}
	ctx, cancel, err := a.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	got, bound, err := a.sourceBoundChatHistory(ctx, tag)
	if err != nil || bound {
		t.Fatalf("aged topic changed route: %v %v", bound, err)
	}
	for _, row := range got {
		if topicChatRowProof(row) != "" {
			t.Fatal("aged topic remained in model history")
		}
	}
	// Context-status rows are display-only children of the excluded topic user.
	clean := func(rows []storage.StreamItemRow) []storage.StreamItemRow {
		out := []storage.StreamItemRow{}
		for _, row := range rows {
			if row.Kind != "context_status" {
				out = append(out, row)
			}
		}
		return out
	}
	if !reflect.DeepEqual(clean(got), clean(expected)) {
		t.Fatalf("ordinary history shrank with an aged topic: got=%d ordinary=%d", len(got), len(expected))
	}
	if topicTurnFromContext(ctx).history.collections != 0 {
		t.Fatal("aged topic consulted sources")
	}
	a.finishChatPreparation(tag, cancel)
	writeTodayContextMemory(t, "atlas", "# Atlas\nChanged after this exchange aged out")
	input := topicRound2OrdinaryTurn(t, a, tag, "native-session")
	if input.SessionID != "native-session" {
		t.Fatalf("aging rotated a valid session: %q", input.SessionID)
	}
}

type topicRound2WithdrawalRunner struct {
	upcomingCaptureRunner
	withdraw func() error
	result   chan error
}

func (r *topicRound2WithdrawalRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, _ agent.StreamCallback) (string, error) {
	if err := r.withdraw(); err != nil {
		r.result <- err
		return "", err
	}
	err := input.BeforeDispatch(ctx)
	r.result <- err
	return "", err
}
func TestTopicRound2LateWithdrawalPersistsRotationBeforeRefusal(t *testing.T) {
	a, tag := topicChatTestApp(t)
	topicRound2Send(t, a, tag)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	r := &topicRound2WithdrawalRunner{withdraw: func() error {
		return os.WriteFile(filepath.Join(dir, "atlas.md"), []byte("# Atlas\nWithdrawn after runner initialization"), 0600)
	}, result: make(chan error, 1)}
	a.runner = r
	if err := a.executePrompt("ordinary question", "", "chosen-model", "native-session", tag, nil, false, false, "en", nil, func(text, lang string) string { return text }, "", "", promptOriginMac); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.result:
		if err != errChatSourceAccessChanged {
			t.Fatalf("initialized request used withdrawn state: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing dispatch guard")
	}
	waitForTagIdle(t, a, tag)
	persisted, err := a.store.ResolveTopicChatSession(t.Context(), tag, "native-session", nil)
	if err != nil || persisted == "" || persisted == "native-session" {
		t.Fatalf("dispatch refusal did not persist replacement: %q %v", persisted, err)
	}
	// Rotation cannot depend on discovering the same stale rows again.
	for i := 0; i < 30; i++ {
		if _, err := a.store.AppendStreamItem(t.Context(), tag, storage.StreamItemRow{ID: fmt.Sprint("later-", i), Kind: "assistant", Content: "later ordinary answer"}); err != nil {
			t.Fatal(err)
		}
	}
	next := topicRound2OrdinaryTurn(t, a, tag, "native-session")
	if next.SessionID != persisted {
		t.Fatalf("aging out lost replacement identity: %q want %q", next.SessionID, persisted)
	}
}

func TestTopicRound2DispatchContextSurvivesTurnCompletion(t *testing.T) {
	a, tag := topicChatTestApp(t)
	topicRound2Send(t, a, tag)
	input := topicRound2OrdinaryTurn(t, a, tag, "native-session")
	// The runner has finished and the turn binding is no longer in the App.
	// A fallback still carries the frozen proof, with its own live context.
	if err := input.BeforeDispatch(t.Context()); err != nil {
		t.Fatalf("valid fallback inherited completed turn cancellation: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := input.BeforeDispatch(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("dispatch cancellation lost: %v", err)
	}
	writeTodayContextMemory(t, "atlas", "# Atlas\nWithdrawn after turn completion")
	if err := input.BeforeDispatch(t.Context()); !errors.Is(err, errChatSourceAccessChanged) {
		t.Fatalf("fallback reused withdrawn topic authority: %v", err)
	}
	persisted, err := a.store.ResolveTopicChatSession(t.Context(), tag, "native-session", nil)
	if err != nil || persisted == "" || persisted == "native-session" {
		t.Fatalf("late fallback did not persist replacement: %q %v", persisted, err)
	}
}
