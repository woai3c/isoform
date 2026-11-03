package main

import (
	"context"
	"testing"
	"wails-gui/internal/handoff"
)

func TestBackgroundFinishCannotCrossHandoffAttempts(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "captured_completion", true: "pending_resume"}[pending], func(t *testing.T) {
			app, _, calls := newBgShellTestApp(t)
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore = store
			e, err := store.Save(handoff.Entry{ID: "owned", ChatSessionID: "chat-1", AttemptID: "first", Status: handoff.StatusInProgress})
			if err != nil {
				t.Fatal(err)
			}
			captured := bgShellSessionInfo{Tag: "chat-1", ShellID: "old", HandoffID: e.ID, AttemptID: e.AttemptID}
			if _, err := store.MutateIfStatus(e.ID, []handoff.Status{handoff.StatusInProgress}, func(e *handoff.Entry) { e.AttemptID = "second" }); err != nil {
				t.Fatal(err)
			}
			if pending {
				app.bgShells.AddPending(bgShellFinish{info: captured})
				app.resumeBgShells("chat-1")
			} else {
				app.onBgShellFinish(captured, 0, "completed", "old command")
			}
			if len(*calls) != 0 {
				t.Fatalf("old completion resumed a newer attempt: %+v", *calls)
			}
			rows, err := app.store.LoadStreamItems(context.Background(), "chat-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 0 {
				t.Fatalf("old completion published new-attempt history: %+v", rows)
			}
		})
	}
}
