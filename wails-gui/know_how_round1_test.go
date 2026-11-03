package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

func TestKnowHowRound1BackfillStaysOnLearningRunner(t *testing.T) {
	for _, pick := range []string{"claude-code", "codex"} {
		t.Run(pick, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			native := &selfDreamFakeRunner{response: `{"earlier-fix":"fix"}`}
			assistant := &selfDreamFakeRunner{response: `{"earlier-fix":"fix"}`}
			router := agent.NewOrderedRoutingAgentRunner(native,
				map[string]agent.AgentRunner{pick: assistant},
				agent.RoutingPolicy{
					ManagedOrder: []string{pick},
					Assistants: map[string]agent.AssistantState{
						pick: {SignedIn: true, Available: true, ControlProfile: true},
					},
				},
			)
			// Confirm that this policy really chooses the signed-in assistant
			// for chat before testing the learning boundary with the same router.
			if _, err := router.ExecuteTask(t.Context(), &agent.TaskInput{
				Prompt: "Chat control", BackendHint: agent.BackendHintChat,
			}, func(*agent.StreamEvent) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if len(assistant.inputs) != 1 || len(native.inputs) != 0 {
				t.Fatalf("chat control: assistant=%d native=%d", len(assistant.inputs), len(native.inputs))
			}
			app := newTranslationTestApp(t, &memoryTranslationFakeRunner{})
			app.runner = router
			kinds, err := app.classifyKnowHowKinds(context.Background(), []knowHowBackfillInput{{
				ID: "earlier-fix", Title: "Socket workaround", Description: "Restart the daemon",
				Points: []string{"ECONNREFUSED: restart the service"},
			}})
			if err != nil || kinds["earlier-fix"] != "fix" {
				t.Fatalf("classification=%v err=%v", kinds, err)
			}
			if len(assistant.inputs) != 1 || len(native.inputs) != 1 {
				t.Fatalf("know-how escaped the learning runner: assistant=%d native=%d", len(assistant.inputs), len(native.inputs))
			}
			if input := native.inputs[0]; input.BackendHint != agent.BackendHintLearning || !input.NoTools || !input.NoSystem || !input.Ephemeral || !input.SuppressTelemetry {
				t.Fatalf("learning task lost its routing or execution restrictions: %+v", input)
			}
		})
	}
}

func TestKnowHowRound1CardsReadPersistedUseCounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := newTranslationTestApp(t, &memoryTranslationFakeRunner{})
	for _, id := range []string{"reused", "unused"} {
		writeKnowHowFixture(t, id, buildOKFKnowledgeMarkdown(id, "Socket workaround", "Restart the daemon", time.Now(), "conversation", nil, nil, nil, "fix"))
	}
	// Exercise the real C5 queue and database. One batch avoids making an
	// assumption that separate best-effort admissions cannot be dropped.
	app.store.RecordContextUseBatch([]storage.ContextUse{
		{Kind: "knowledge", ID: "reused", Surface: "chat", Ref: "chat-session"},
		{Kind: "knowledge", ID: "reused", Surface: "assistant_tool", Ref: "tool-session"},
	})
	awaitEntryUses(t, app, "knowledge", "reused", 2)
	check := func(want int) {
		t.Helper()
		cards, err := app.ListKnowHow()
		if err != nil {
			t.Fatal(err)
		}
		if len(cards) != 2 {
			t.Fatalf("owner entries changed: %+v", cards)
		}
		for _, card := range cards {
			expected := 0
			if card.ID == "reused" {
				expected = want
			}
			if card.ReuseCount != expected {
				t.Errorf("%s reuse count=%d want %d", card.ID, card.ReuseCount, expected)
			}
		}
	}
	check(2)
	app.closedEyes.enabled.Store(true)
	check(0)
	app.closedEyes.enabled.Store(false)
	check(2)
}

func TestKnowHowRound1DismissalIsBrowserStateChange(t *testing.T) {
	const method = "DismissKnowHowMatch"
	if !browserRPCAllowlist[method] {
		t.Fatal("browser preview lost the Not the same action")
	}
	if browserRPCStateChangers[method] == "" {
		t.Error("dismissal writes ui_state but has no browser state-change reason")
	}
	for _, read := range browserRPCReadOnly {
		if read == method {
			t.Error("dismissal is incorrectly classified as changing nothing on disk")
		}
	}
}
