package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// TodoList is the shared in-memory store of TODO items. Each session
// has one. The loop flushes it to the frontend as a stream_item so the
// UI has its own rendering path.
type TodoList struct {
	mu    sync.Mutex
	items []TodoItem
}

// TodoItem mirrors Claude Code's TodoWrite entry shape.
type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"` // pending | in_progress | completed
	ActiveForm string `json:"activeForm"`
}

// NewTodoList returns an empty TODO list.
func NewTodoList() *TodoList { return &TodoList{} }

// Replace swaps the entire list. Used by the TodoWrite tool.
func (l *TodoList) Replace(items []TodoItem) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items[:0], items...)
}

// Items returns a snapshot of the current list.
func (l *TodoList) Items() []TodoItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]TodoItem, len(l.items))
	copy(out, l.items)
	return out
}

// TodoWriteTool persists the model's TODO list for the current session.
type TodoWriteTool struct {
	list *TodoList
}

// NewTodoWriteTool binds the tool to a session's shared TodoList.
func NewTodoWriteTool(list *TodoList) *TodoWriteTool {
	return &TodoWriteTool{list: list}
}

func (*TodoWriteTool) Name() string { return "TodoWrite" }

func (*TodoWriteTool) Description() string {
	return "Replace the current session's TODO list. Each item has content, status, and activeForm."
}

func (*TodoWriteTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"todos"},
		"properties": map[string]any{
			"todos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []any{"content", "status"},
					"properties": map[string]any{
						"content":    map[string]any{"type": "string"},
						"status":     map[string]any{"type": "string", "enum": []any{"pending", "in_progress", "completed"}},
						"activeForm": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

func (*TodoWriteTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *TodoWriteTool) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	raw, ok := call.Input["todos"].([]any)
	if !ok {
		return ToolResult{}, fmt.Errorf("`todos` must be an array")
	}
	items := make([]TodoItem, 0, len(raw))
	inProgress := 0
	for i, v := range raw {
		obj, _ := v.(map[string]any)
		if obj == nil {
			return ToolResult{}, fmt.Errorf("todos[%d] must be an object", i)
		}
		ti := TodoItem{
			Content:    asString(obj["content"]),
			Status:     asString(obj["status"]),
			ActiveForm: asString(obj["activeForm"]),
		}
		if ti.Content == "" || ti.Status == "" {
			return ToolResult{}, fmt.Errorf("todos[%d]: content and status are required", i)
		}
		if ti.Status == "in_progress" {
			inProgress++
		}
		items = append(items, ti)
	}
	t.list.Replace(items)

	warnings := []string{}
	if inProgress > 1 {
		warnings = append(warnings, "more than one TODO is in_progress — consider focusing on one at a time")
	}

	enc, _ := json.Marshal(items)
	return ToolResult{
		Content:  fmt.Sprintf("Recorded %d todo items.\n%s", len(items), enc),
		Warnings: warnings,
		Metadata: map[string]any{"count": len(items)},
	}, nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
