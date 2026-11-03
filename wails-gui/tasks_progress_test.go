package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
	"yansu-agent/agent"
)

func TestTaskTodoProgress(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		done, total int
		label       string
	}{
		{"mixed", `{"todos":[{"content":"Read","status":"completed"},{"content":"Test","status":"in_progress","activeForm":"Testing"},{"content":"Ship","status":"pending"}]}`, 1, 3, "Testing"},
		{"content label", `{"todos":[{"content":"Test","status":"in_progress"}]}`, 0, 1, "Test"},
		{"all done", `{"todos":[{"content":"Test","status":"completed"}]}`, 1, 1, ""},
		{"pending", `{"todos":[{"content":"Test","status":"pending"}]}`, 0, 1, ""},
		{"empty", `{"todos":[]}`, 0, 0, ""},
		{"missing", `{}`, 0, 0, ""},
		{"malformed", `{"todos":`, 0, 0, ""},
		{"unknown status", `{"todos":[{"content":"Test","status":"unknown"}]}`, 0, 0, ""},
		{"missing content", `{"todos":[{"status":"completed"}]}`, 0, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := taskTodoProgress(tc.input)
			if tc.total == 0 {
				if got != nil {
					t.Fatalf("unknown progress = %+v", got)
				}
				return
			}
			if got == nil || got.Done != tc.done || got.Total != tc.total || got.Label != tc.label {
				t.Fatalf("progress = %+v", got)
			}
		})
	}
}

func TestTaskHandoffProgressUsesOnlyCurrentRun(t *testing.T) {
	app := mainChatTestApp(t)
	var err error
	app.handoffStore, err = handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := app.createHandoffExecutionSession(handoff.Entry{Title: "Task"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.handoffStore.Save(handoff.Entry{ID: "hf-progress", Title: "Task", Status: handoff.StatusInProgress, ChatSessionID: session, AttemptID: "attempt"})
	if err != nil {
		t.Fatal(err)
	}
	appendTodo := func(id, runID, input string) {
		t.Helper()
		_, err := app.store.AppendStreamItem(t.Context(), session, storage.StreamItemRow{ID: id, Kind: "tool", ToolName: "TodoWrite", ToolInput: input, RunID: runID})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendTodo("old", "old-run", `{"todos":[{"content":"Old","status":"completed"}]}`)
	assertUnknown := func(view HandoffEntryView) {
		t.Helper()
		if view.Progress != nil {
			t.Fatalf("guessed progress: %+v", view)
		}
	}
	assertUnknown(app.productHandoffEntryView(entry))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.registerHandoffRun(&entry, t.TempDir(), cancel); err != nil {
		t.Fatal(err)
	}
	app.noteHandoffRunRoute(&entry, &agent.StreamEvent{Type: "run_created", RunID: "current", Backend: "claude-code"})
	view := app.productHandoffEntryView(entry)
	if view.Runner != "claude_code" {
		t.Fatalf("runner = %q", view.Runner)
	}
	assertUnknown(view)
	appendTodo("current-todo", "current", `{"todos":[{"content":"Read","status":"completed"},{"content":"Test","status":"in_progress","activeForm":"Testing"}]}`)
	appendTodo("other-run", "different", `{"todos":[{"content":"Other","status":"completed"}]}`)
	view = app.productHandoffEntryView(entry)
	if view.Progress == nil || view.Progress.Done != 1 || view.Progress.Total != 2 || view.Progress.Label != "Testing" {
		t.Fatalf("current progress = %+v", view)
	}
	listed, err := app.ListHandoffs()
	if err != nil || len(listed) != 1 || listed[0].Progress == nil {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	old := entry
	old.AttemptID = "older"
	app.noteHandoffRunRoute(&old, &agent.StreamEvent{Type: "run_created", RunID: "late", Backend: "codex"})
	if app.productHandoffEntryView(entry).Runner != "claude_code" {
		t.Fatal("late attempt changed runner")
	}
	assertUnknown(app.productHandoffEntryView(old))
	appendTodo("empty-current", "current", `{"todos":[]}`)
	assertUnknown(app.productHandoffEntryView(entry))
	appendTodo("malformed-current", "current", `not json`)
	assertUnknown(app.productHandoffEntryView(entry))
	for _, backend := range []struct{ raw, want string }{{"codex", "codex"}, {"native", "isoform"}, {"unknown", ""}} {
		app.noteHandoffRunRoute(&entry, &agent.StreamEvent{Type: "run_created", RunID: "new-run", Backend: backend.raw})
		view := app.productHandoffEntryView(entry)
		if view.Runner != backend.want {
			t.Fatalf("backend %q = %q", backend.raw, view.Runner)
		}
		assertUnknown(view)
	}
	completed := entry
	completed.Status = handoff.StatusCompleted
	view = app.productHandoffEntryView(completed)
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), `"progress":`) || strings.Contains(string(raw), `"runner":`) {
		t.Fatalf("inactive task metadata = %s", raw)
	}
	if ctx.Err() != nil {
		t.Fatal("reading progress cancelled work")
	}
	if err := app.store.Close(); err != nil {
		t.Fatal(err)
	}
	assertUnknown(app.productHandoffEntryView(entry))
}
