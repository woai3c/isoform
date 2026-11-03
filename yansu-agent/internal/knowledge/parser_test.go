package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteKnowledgeFileWritesOKFAndParseFileReadsIt(t *testing.T) {
	dir := t.TempDir()
	if err := WriteKnowledgeFile(dir, "uuid-1", "Use OKF for knowledge", "Store one concept per Markdown file.", []string{"format", "okf"}, "product"); err != nil {
		t.Fatalf("WriteKnowledgeFile: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasPrefix(string(data), "---\n") || !strings.Contains(string(data), "type: Yansu Knowledge") {
		t.Fatalf("written file is not OKF:\n%s", string(data))
	}

	got, err := ParseFile(files[0])
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got.UUID != "uuid-1" {
		t.Errorf("UUID = %q, want uuid-1", got.UUID)
	}
	if got.Trigger != "Use OKF for knowledge" {
		t.Errorf("Trigger = %q", got.Trigger)
	}
	if got.Content != "Store one concept per Markdown file." {
		t.Errorf("Content = %q", got.Content)
	}
	if got.Path != "product" {
		t.Errorf("Path = %q, want product", got.Path)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "format" || got.Tags[1] != "okf" {
		t.Errorf("Tags = %+v, want [format okf]", got.Tags)
	}
}

func TestLoadAllSkipsOKFReservedFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"index.md": "# Index\n\n- [Real](real.md)\n",
		"log.md":   "# Log\n\n## 2026-06-22\n\n* Update\n",
		"real.md": `---
type: Yansu Knowledge
title: Real
uuid: uuid-real
---

Real content.
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != 1 || got[0].UUID != "uuid-real" {
		t.Fatalf("LoadAll = %+v, want only real.md", got)
	}
}
