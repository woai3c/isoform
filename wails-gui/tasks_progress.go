package main

import (
	"context"
	"encoding/json"

	"wails-gui/internal/handoff"
)

type TaskProgressView struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Label string `json:"label"`
}

func (a *App) populateTaskProgress(entry handoff.Entry, view *HandoffEntryView) {
	if a == nil || entry.Status != handoff.StatusInProgress {
		return
	}
	a.handoffActionMu.Lock()
	run := a.handoffRuns[entry.ID]
	runID := ""
	if run != nil && run.attemptID == entry.AttemptID && run.sessionID == entry.ChatSessionID {
		runID = run.runID
		view.Runner = run.runner
	}
	a.handoffActionMu.Unlock()
	if runID == "" || a.store == nil || entry.ChatSessionID == "" {
		return
	}
	page, err := a.store.LoadStreamItemsLatest(context.Background(), entry.ChatSessionID, 200)
	if err != nil {
		return
	}
	for i := len(page.Items) - 1; i >= 0; i-- {
		item := page.Items[i]
		if item.Kind != "tool" || item.ToolName != "TodoWrite" || item.RunID != runID {
			continue
		}
		view.Progress = taskTodoProgress(item.ToolInput)
		return
	}
}

func taskTodoProgress(input string) *TaskProgressView {
	var snapshot struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	if json.Unmarshal([]byte(input), &snapshot) != nil || len(snapshot.Todos) == 0 {
		return nil
	}
	progress := &TaskProgressView{}
	for _, todo := range snapshot.Todos {
		if todo.Content == "" {
			return nil
		}
		switch todo.Status {
		case "completed":
			progress.Done++
		case "pending":
		case "in_progress":
			if progress.Label == "" {
				progress.Label = firstNonEmpty(todo.ActiveForm, todo.Content)
			}
		default:
			return nil
		}
		progress.Total++
	}
	return progress
}
