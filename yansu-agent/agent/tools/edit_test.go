package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEditTool_Basic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "hello world\n")

	tool := NewEditTool(nil)
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "old_string": "world", "new_string": "planet",
	}})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if res.Metadata["match_level"] != "L1" {
		t.Errorf("match_level = %v, want L1", res.Metadata["match_level"])
	}
	data, _ := os.ReadFile(p)
	if string(data) != "hello planet\n" {
		t.Errorf("file = %q", string(data))
	}
}

func TestEditTool_AmbiguousRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	writeFile(t, p, "foo\nfoo\nfoo\n")

	tool := NewEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "old_string": "foo", "new_string": "bar",
	}})
	if err == nil {
		t.Error("expected ambiguity error")
	}
}

func TestEditTool_ReplaceAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	writeFile(t, p, "foo\nfoo\nfoo\n")

	tool := NewEditTool(nil)
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "old_string": "foo", "new_string": "bar", "replace_all": true,
	}})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "bar\nbar\nbar\n" {
		t.Errorf("file = %q", string(data))
	}
	if res.Metadata["replaced_count"].(int) != 3 {
		t.Errorf("replaced_count = %v, want 3", res.Metadata["replaced_count"])
	}
}

func TestEditTool_OldEqualsNew(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	writeFile(t, p, "hello")

	tool := NewEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "old_string": "hello", "new_string": "hello",
	}})
	if err == nil {
		t.Fatal("expected error for no-op edit")
	}
}

func TestEditTool_EmptyOldString(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	writeFile(t, p, "hello")

	tool := NewEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "old_string": "", "new_string": "hi",
	}})
	if err == nil {
		t.Fatal("expected error for empty old_string")
	}
}

func TestEditTool_L4DedentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "go_code.go")
	file := "func foo() {\n    a := 1\n    b := 2\n}\n"
	writeFile(t, p, file)

	tool := NewEditTool(nil)
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path":       p,
		"old_string": "a := 1\nb := 2",
		"new_string": "a := 100\nb := 200",
	}})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if res.Metadata["match_level"] != "L4" {
		t.Errorf("match_level = %v, want L4", res.Metadata["match_level"])
	}
	data, _ := os.ReadFile(p)
	want := "func foo() {\n    a := 100\n    b := 200\n}\n"
	if string(data) != want {
		t.Errorf("file = %q, want %q", string(data), want)
	}
}

func TestEditTool_LinePrefixRejection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	writeFile(t, p, "line1\nline2\n")

	tool := NewEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path":       p,
		"old_string": "     1\tline1",
		"new_string": "     1\tLINE1",
	}})
	if err == nil {
		t.Fatal("expected L0 rejection for line-number prefix")
	}
}

func TestEditTool_NotFound(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	writeFile(t, p, "hello world\n")

	tool := NewEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "old_string": "banana", "new_string": "apple",
	}})
	if err == nil {
		t.Fatal("expected not-found error")
	}
}
