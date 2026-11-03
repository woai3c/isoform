package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newRejectTestApp returns an App rooted at a temp HOME so memoryDir()
// returns an isolated directory and tests cannot collide with the real
// ~/.yansu-agent/memory contents.
func newRejectTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := &App{}
	configureTestMemoryCanonicalizer(t, app)
	return app
}

// writeMemoryFixture drops a minimal valid memory markdown into the test
// memory dir and returns its entry ID and absolute path.
func writeMemoryFixture(t *testing.T, id, title string) string {
	t.Helper()
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	content := "# " + title + "\n\n" +
		"- **Updated**: 2026-05-20 12:00\n" +
		"- **Source**: activity\n" +
		"- **Dimension**: user_insight\n\n" +
		"## Memory Points\n\n" +
		"- Example point.\n"
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// Also drop a synthetic history snapshot so we can verify cleanup.
	historyDir := filepath.Join(dir, memoryHistoryDirName, id)
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "1.md"), []byte("snap"), 0o600); err != nil {
		t.Fatalf("write history snap: %v", err)
	}
	return path
}

func readRejectionLog(t *testing.T) []MemoryRejectionEntry {
	t.Helper()
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	path := filepath.Join(dir, memoryRejectionsLogName)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open rejections log: %v", err)
	}
	defer f.Close()
	var out []MemoryRejectionEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry MemoryRejectionEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("unmarshal rejection line %q: %v", scanner.Text(), err)
		}
		out = append(out, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan rejections log: %v", err)
	}
	return out
}

func TestRejectMemoryAttribution_DeletesFileAndHistory(t *testing.T) {
	a := newRejectTestApp(t)
	filePath := writeMemoryFixture(t, "test-1", "Yetone likes chestnuts")

	if err := a.RejectMemoryAttribution("test-1", "this was something my friend said"); err != nil {
		t.Fatalf("RejectMemoryAttribution: %v", err)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("expected file removed, got err=%v", err)
	}
	dir, _ := memoryDir()
	historyDir := filepath.Join(dir, memoryHistoryDirName, "test-1")
	if _, err := os.Stat(historyDir); !os.IsNotExist(err) {
		t.Errorf("expected history dir removed, got err=%v", err)
	}
}

func TestRejectMemoryAttribution_DoesNotRetainIdentifyingLog(t *testing.T) {
	a := newRejectTestApp(t)
	writeMemoryFixture(t, "test-2", "I love watermelon")
	if err := a.RejectMemoryAttribution("test-2", "this came from a video I was watching"); err != nil {
		t.Fatal(err)
	}
	dir, _ := memoryDir()
	if _, err := os.Stat(filepath.Join(dir, memoryRejectionsLogName)); !os.IsNotExist(err) {
		t.Fatalf("new rejection retained identifying history: %v", err)
	}
}

func TestRejectMemoryAttribution_MissingIDErrors(t *testing.T) {
	a := newRejectTestApp(t)
	err := a.RejectMemoryAttribution("does-not-exist", "anything")
	if err == nil {
		t.Fatal("expected error for missing id")
	}
	if !strings.Contains(err.Error(), "load entry") {
		t.Errorf("expected wrapped load error, got %v", err)
	}
}

func TestRejectMemoryAttribution_EmptyIDErrors(t *testing.T) {
	a := newRejectTestApp(t)
	err := a.RejectMemoryAttribution("  ", "x")
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestRejectMemoryAttribution_TwoRejectionsSuppressTheirOwnIDs(t *testing.T) {
	a := newRejectTestApp(t)
	writeMemoryFixture(t, "a-1", "A1")
	writeMemoryFixture(t, "a-2", "A2")
	for _, id := range []string{"a-1", "a-2"} {
		if err := a.RejectMemoryAttribution(id, "reason"); err != nil {
			t.Fatalf("reject %s: %v", id, err)
		}
	}
	dir, _ := memoryDir()
	for _, id := range []string{"a-1", "a-2"} {
		forgotten, err := isMemoryForgotten(dir, id, "different content", time.Now())
		if err != nil || !forgotten {
			t.Errorf("rejected id %s not suppressed: forgotten=%v err=%v", id, forgotten, err)
		}
	}
}

func TestRejectMemoryAttribution_DoesNotPersistReason(t *testing.T) {
	for _, reason := range []string{"line one\nline two\twith tab and \"quotes\" and CJK 你好/世界", "  reason with spaces  \n"} {
		t.Run(reason, func(t *testing.T) {
			a := newRejectTestApp(t)
			writeMemoryFixture(t, "private-reason", "Private reason")
			if err := a.RejectMemoryAttribution("private-reason", reason); err != nil {
				t.Fatal(err)
			}
			dir, _ := memoryDir()
			data, err := os.ReadFile(filepath.Join(dir, memoryTombstonesFile))
			if err != nil || strings.Contains(string(data), strings.TrimSpace(reason)) {
				t.Fatalf("reason retained or suppression absent: err=%v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, memoryRejectionsLogName)); !os.IsNotExist(err) {
				t.Fatalf("reason retained in rejection log: %v", err)
			}
		})
	}
}

func TestRejectMemoryAttribution_DoesNotRetainSourceIdentity(t *testing.T) {
	a := newRejectTestApp(t)
	dir, _ := memoryDir()
	content := "# session-attached memory\n\n- **Source**: activity\n- **Activity Session**: sess-private\n\n## Memory Points\n\n- A private observation.\n"
	if err := os.WriteFile(filepath.Join(dir, "sessmem.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.RejectMemoryAttribution("sessmem", "wrong"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, memoryTombstonesFile))
	if err != nil || strings.Contains(string(data), "sess-private") || strings.Contains(string(data), "sessmem") {
		t.Fatalf("source identifiers retained in suppression: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, memoryRejectionsLogName)); !os.IsNotExist(err) {
		t.Fatalf("source identifiers retained in rejection log: %v", err)
	}
}
