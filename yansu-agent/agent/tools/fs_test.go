package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent/tools/guard"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadTool_Basic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.txt")
	writeFile(t, p, "alpha\nbeta\ngamma\n")

	tool := NewReadTool()
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Content, "     1\talpha") {
		t.Errorf("missing line 1: %q", res.Content)
	}
	if !strings.Contains(res.Content, "     3\tgamma") {
		t.Errorf("missing line 3: %q", res.Content)
	}
}

func TestReadTool_RejectRelativePath(t *testing.T) {
	tool := NewReadTool()
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": "relative.txt"}})
	if err == nil {
		t.Fatal("expected error for relative path")
	}
}

func TestReadTool_MissingFile(t *testing.T) {
	tool := NewReadTool()
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": "/nope/does/not/exist"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadTool_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.txt")
	writeFile(t, p, "")

	tool := NewReadTool()
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Content, "empty") {
		t.Errorf("expected empty-file sentinel, got %q", res.Content)
	}
}

func TestReadTool_Binary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bin.dat")
	writeFile(t, p, string([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0xFF, 0xFE}))

	tool := NewReadTool()
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p}})
	if err == nil {
		t.Fatal("expected error for binary file")
	}
}

func TestReadTool_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	writeFile(t, target, "hello")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	tool := NewReadTool()
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": link}})
	if err == nil {
		t.Fatal("expected error for symlink")
	}
}

func TestFilesystemToolsRejectPathsOutsideWorkDir(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "read", run: func() error {
			_, err := NewReadTool().Execute(context.Background(), ToolCall{
				WorkDir: workDir,
				Input:   map[string]any{"path": outsidePath},
			})
			return err
		}},
		{name: "write", run: func() error {
			_, err := NewWriteTool(nil).Execute(context.Background(), ToolCall{
				WorkDir: workDir,
				Input: map[string]any{
					"path":    filepath.Join(outside, "created.txt"),
					"content": "blocked",
				},
			})
			return err
		}},
		{name: "edit", run: func() error {
			_, err := NewEditTool(nil).Execute(context.Background(), ToolCall{
				WorkDir: workDir,
				Input: map[string]any{
					"path":       outsidePath,
					"old_string": "original",
					"new_string": "changed",
				},
			})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Fatal("filesystem tool accepted a path outside WorkDir")
			}
		})
	}
	data, err := os.ReadFile(outsidePath)
	if err != nil || string(data) != "original" {
		t.Fatalf("outside file changed: data=%q err=%v", data, err)
	}
}

func TestReadTool_OffsetLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line\n")
	}
	writeFile(t, p, b.String())

	tool := NewReadTool()
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path":   p,
		"offset": 3.0,
		"limit":  2.0,
	}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Content, "     3\tline") {
		t.Errorf("missing line 3: %q", res.Content)
	}
	if !strings.Contains(res.Content, "     4\tline") {
		t.Errorf("missing line 4: %q", res.Content)
	}
	if strings.Contains(res.Content, "     5\t") {
		t.Errorf("line 5 should be past limit: %q", res.Content)
	}
}

func TestReadTool_LargeFileTruncate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "huge.txt")
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		b.WriteString("line\n")
	}
	writeFile(t, p, b.String())

	tool := NewReadTool()
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Content, "3000 lines") {
		t.Errorf("expected truncation note with total, got: %q", truncate(res.Content))
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func TestWriteTool_CreateNew(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new", "file.txt")
	w := NewWriteTool(nil)
	_, err := w.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p, "content": "hi"}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hi" {
		t.Errorf("content = %q, want 'hi'", string(data))
	}
}

func TestWriteTool_Overwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	writeFile(t, p, "old")
	w := NewWriteTool(nil)
	res, err := w.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p, "content": "new"}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(res.Content, "overwritten") {
		t.Errorf("expected overwritten message, got %q", res.Content)
	}
}

func TestWriteTool_CancelCleansUpTmp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cancel.txt")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	w := NewWriteTool(nil)
	_, err := w.Execute(ctx, ToolCall{Input: map[string]any{"path": p, "content": "nope"}})
	if err == nil {
		t.Fatal("expected error on cancelled ctx")
	}
	// The target path must not exist.
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("write target should not exist after cancel, err = %v", err)
	}
	// Give any async cleanup a moment (synchronous here, but belt and
	// braces).
	time.Sleep(10 * time.Millisecond)
	matches, _ := filepath.Glob(p + ".tmp-*")
	if len(matches) > 0 {
		t.Errorf("tmp files leaked: %v", matches)
	}
}

func TestWriteTool_RejectsNullByte(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "null.txt")
	w := NewWriteTool(nil)
	_, err := w.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p, "content": "ok\x00bad"}})
	if err == nil {
		t.Fatal("expected error on NUL byte")
	}
}

func TestWriteTool_ConcurrentSerialized(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "race.txt")
	locks := guard.NewFileLockManager(0)
	w := NewWriteTool(locks)

	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		content := strings.Repeat(string(rune('a'+i)), 100)
		go func(c string) {
			_, err := w.Execute(context.Background(), ToolCall{Input: map[string]any{"path": p, "content": c}})
			done <- err
		}(content)
	}
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent write %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 100 {
		t.Errorf("final file size = %d, want 100 (one writer wins cleanly)", len(data))
	}
}

func TestWriteTool_TooLarge(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	w := NewWriteTool(nil)
	_, err := w.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path":    p,
		"content": strings.Repeat("x", writeMaxBytes+1),
	}})
	if err == nil {
		t.Fatal("expected error for oversize content")
	}
}
