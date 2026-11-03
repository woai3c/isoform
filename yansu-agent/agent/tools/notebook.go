package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"yansu-agent/agent/tools/guard"
)

// NotebookEditTool edits a single cell inside a Jupyter .ipynb file.
// It keeps the nbformat version intact and only touches the targeted
// cell's source (optionally its type).
type NotebookEditTool struct {
	locks *guard.FileLockManager
}

// NewNotebookEditTool constructs a NotebookEdit tool.
func NewNotebookEditTool(locks *guard.FileLockManager) *NotebookEditTool {
	if locks == nil {
		locks = guard.NewFileLockManager(0)
	}
	return &NotebookEditTool{locks: locks}
}

func (*NotebookEditTool) Name() string { return "NotebookEdit" }

func (*NotebookEditTool) Description() string {
	return "Edit a specific cell in a Jupyter .ipynb file. Specify the cell_index and the new_source. " +
		"Optionally change cell_type between 'code' and 'markdown'."
}

func (*NotebookEditTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"path", "cell_index", "new_source"},
		"properties": map[string]any{
			"path":       map[string]any{"type": "string"},
			"cell_index": map[string]any{"type": "integer"},
			"new_source": map[string]any{"type": "string"},
			"cell_type":  map[string]any{"type": "string", "enum": []any{"code", "markdown", "raw"}},
		},
	}
}

func (*NotebookEditTool) NeedsPermission(input map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

func (t *NotebookEditTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	if err := CheckProtectedPaths(t.Name(), call.Input, call.WorkDir); err != nil {
		return ToolResult{}, err
	}
	if err := enforcePersistenceApproval(ctx, t.Name(), call); err != nil {
		return ToolResult{}, err
	}
	rawPath, _ := call.Input["path"].(string)
	path, err := guard.ValidateAbsolutePath(rawPath)
	if err != nil {
		return ToolResult{}, err
	}
	// Confine notebook edits to the session WorkDir, as Write and Edit are.
	if call.WorkDir != "" {
		if err := guard.WithinAllowed(path, []string{call.WorkDir}); err != nil {
			return ToolResult{}, fmt.Errorf("%w (workspace: %s)", err, call.WorkDir)
		}
	}
	if !strings.HasSuffix(path, ".ipynb") {
		return ToolResult{}, fmt.Errorf("path must end in .ipynb")
	}
	idx, _ := asInt(call.Input["cell_index"])
	newSource, _ := call.Input["new_source"].(string)
	cellType, _ := call.Input["cell_type"].(string)

	lock := t.locks.Lock(path)
	lock.Lock()
	defer lock.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("read: %w", err)
	}
	var nb map[string]any
	if err := json.Unmarshal(data, &nb); err != nil {
		return ToolResult{}, fmt.Errorf("parse notebook: %w", err)
	}
	cellsRaw, ok := nb["cells"].([]any)
	if !ok {
		return ToolResult{}, fmt.Errorf("notebook has no cells array")
	}
	if idx < 0 || idx >= len(cellsRaw) {
		return ToolResult{}, fmt.Errorf("cell_index %d out of range (0..%d)", idx, len(cellsRaw)-1)
	}
	cell, ok := cellsRaw[idx].(map[string]any)
	if !ok {
		return ToolResult{}, fmt.Errorf("cell %d is not an object", idx)
	}
	// source is typically []any of strings, one per line (Jupyter convention).
	lines := splitNotebookSource(newSource)
	cell["source"] = lines
	if cellType != "" {
		cell["cell_type"] = cellType
		if cellType == "code" {
			if _, has := cell["outputs"]; !has {
				cell["outputs"] = []any{}
			}
			if _, has := cell["execution_count"]; !has {
				cell["execution_count"] = nil
			}
		}
	}

	out, err := json.MarshalIndent(nb, "", " ")
	if err != nil {
		return ToolResult{}, err
	}
	// Write through a temp file and rename, so a hard link to a file outside
	// the workspace is replaced rather than changed in place.
	tmp, err := tempPath(path)
	if err != nil {
		return ToolResult{}, err
	}
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return ToolResult{}, fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return ToolResult{}, fmt.Errorf("rename: %w", err)
	}
	return ToolResult{
		Content:  fmt.Sprintf("Updated cell %d in %s", idx, path),
		Metadata: map[string]any{"cell_index": idx, "cell_type": cell["cell_type"]},
	}, nil
}

// splitNotebookSource preserves the "one string per line with trailing
// newline" convention Jupyter uses. The last line keeps its newline only
// if the source ends with one.
func splitNotebookSource(s string) []any {
	if s == "" {
		return []any{}
	}
	parts := strings.SplitAfter(s, "\n")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
