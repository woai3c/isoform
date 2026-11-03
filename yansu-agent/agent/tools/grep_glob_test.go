package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepTool_Basic(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package a\nfunc foo() {}\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package b\nfunc bar() {}\n")

	tool := NewGrepTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: dir,
		Input: map[string]any{
			"pattern":     "func",
			"path":        dir,
			"output_mode": "content",
			"-n":          true,
		},
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(res.Content, "foo") {
		t.Errorf("missing foo in output: %q", res.Content)
	}
}

func TestGrepTool_FilesWithMatches(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip()
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "needle\n")
	writeFile(t, filepath.Join(dir, "b.go"), "nothing\n")

	tool := NewGrepTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: dir,
		Input:   map[string]any{"pattern": "needle", "path": dir},
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(res.Content, "a.go") {
		t.Errorf("a.go not found in results: %q", res.Content)
	}
	if strings.Contains(res.Content, "b.go") {
		t.Errorf("b.go shouldn't match: %q", res.Content)
	}
}

func TestGrepTool_NoMatches(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip()
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "alpha\n")

	tool := NewGrepTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: dir,
		Input:   map[string]any{"pattern": "ZZZNOTPRESENT", "path": dir},
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(res.Content, "no matches") {
		t.Errorf("expected no-matches message, got %q", res.Content)
	}
}

func TestGrepTool_RequiresPattern(t *testing.T) {
	tool := NewGrepTool()
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{}})
	if err == nil {
		t.Error("expected error for missing pattern")
	}
}

func TestGlobTool_Basic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "a.go"), "a")
	writeFile(t, filepath.Join(dir, "sub", "b.go"), "b")
	writeFile(t, filepath.Join(dir, "sub", "c.txt"), "c")

	tool := NewGlobTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: dir,
		Input:   map[string]any{"pattern": "**/*.go", "path": dir},
	})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !strings.Contains(res.Content, "a.go") || !strings.Contains(res.Content, "b.go") {
		t.Errorf("missing go files: %q", res.Content)
	}
	if strings.Contains(res.Content, "c.txt") {
		t.Errorf("txt file leaked: %q", res.Content)
	}
}

func TestGlobTool_RejectsDotDot(t *testing.T) {
	tool := NewGlobTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"pattern": "../**", "path": "/tmp"},
	})
	if err == nil {
		t.Error("expected error for `..` pattern")
	}
}

func TestSearchToolsRejectRootsOutsideWorkDir(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "secret")

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "glob", run: func() error {
			_, err := NewGlobTool().Execute(context.Background(), ToolCall{
				WorkDir: workDir,
				Input:   map[string]any{"pattern": "**/*", "path": outside},
			})
			return err
		}},
		{name: "grep", run: func() error {
			_, err := NewGrepTool().Execute(context.Background(), ToolCall{
				WorkDir: workDir,
				Input:   map[string]any{"pattern": "secret", "path": outside},
			})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Fatal("search tool accepted a root outside WorkDir")
			}
		})
	}
}

func TestGlobTool_SkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.go")
	writeFile(t, target, "x")
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unsupported")
	}

	tool := NewGlobTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: dir,
		Input:   map[string]any{"pattern": "*.go", "path": dir},
	})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if strings.Contains(res.Content, "link.go") {
		t.Errorf("symlink leaked: %q", res.Content)
	}
}

func TestTodoWriteTool(t *testing.T) {
	list := NewTodoList()
	tool := NewTodoWriteTool(list)
	todos := []any{
		map[string]any{"content": "do x", "status": "pending", "activeForm": "doing x"},
		map[string]any{"content": "do y", "status": "in_progress", "activeForm": "doing y"},
	}
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"todos": todos}})
	if err != nil {
		t.Fatalf("todo: %v", err)
	}
	items := list.Items()
	if len(items) != 2 || items[0].Content != "do x" {
		t.Errorf("items = %+v", items)
	}
}

func TestTodoWriteTool_WarnsOnMultipleInProgress(t *testing.T) {
	list := NewTodoList()
	tool := NewTodoWriteTool(list)
	todos := []any{
		map[string]any{"content": "a", "status": "in_progress", "activeForm": "doing a"},
		map[string]any{"content": "b", "status": "in_progress", "activeForm": "doing b"},
	}
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"todos": todos}})
	if err != nil {
		t.Fatalf("todo: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning for >1 in_progress")
	}
}

func TestExitPlanModeTool(t *testing.T) {
	state := NewPlanModeState(true)
	tool := NewExitPlanModeTool(state)
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"plan": "step 1"}})
	if err != nil {
		t.Fatalf("exit plan: %v", err)
	}
	if res.Content != "step 1" {
		t.Errorf("plan content = %q", res.Content)
	}
}

func TestExitPlanModeTool_RejectsOutsidePlan(t *testing.T) {
	state := NewPlanModeState(false)
	tool := NewExitPlanModeTool(state)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"plan": "x"}})
	if err == nil {
		t.Error("expected error when not in plan mode")
	}
}
