package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- parseMemoryFrontmatter --------------------------------------------------

func TestParseMemoryFrontmatter_FlatActivitySession(t *testing.T) {
	content := `# Title

- **Updated**: 2026-04-20 11:30
- **Source**: activity
- **Activity Session**:
  - session-a — 10:28, cmux
  - session-b — 10:42, VSCode

## Memory Points

- something
`
	fm := parseMemoryFrontmatter(content)
	if got, want := fm.ActivitySessionIDs, []string{"session-a", "session-b"}; !equalStrings(got, want) {
		t.Fatalf("ActivitySessionIDs = %v, want %v", got, want)
	}
}

func TestParseMemoryFrontmatter_GroupedActivitySession(t *testing.T) {
	content := `# Title

- **Updated**: 2026-04-20 14:15
- **Source**: activity
- **Activity Session**:
  - **Update 2026-04-20 11:30** (initial)
    - session-a — 10:28, cmux
  - **Update 2026-04-20 14:15** (update)
    - session-b — 14:02, VSCode
    - session-c — 14:05, Chrome

## Memory Points

- something
`
	fm := parseMemoryFrontmatter(content)
	want := []string{"session-a", "session-b", "session-c"}
	if !equalStrings(fm.ActivitySessionIDs, want) {
		t.Fatalf("ActivitySessionIDs = %v, want %v", fm.ActivitySessionIDs, want)
	}
}

func TestParseMemoryFrontmatter_LegacySingleLine(t *testing.T) {
	content := `- **Activity Session**: session-legacy
`
	fm := parseMemoryFrontmatter(content)
	if got, want := fm.ActivitySessionIDs, []string{"session-legacy"}; !equalStrings(got, want) {
		t.Fatalf("ActivitySessionIDs = %v, want %v", got, want)
	}
}

// --- extract / detect / upgrade helpers --------------------------------------

func TestExtractActivitySessionBlock(t *testing.T) {
	content := `# T

- **Updated**: 2026-04-20 11:30
- **Activity Session**:
  - session-a — 10:28, cmux
  - session-b — 10:42, VSCode

## Memory Points
`
	got := extractActivitySessionBlock(content)
	want := "- **Activity Session**:\n  - session-a — 10:28, cmux\n  - session-b — 10:42, VSCode"
	if got != want {
		t.Fatalf("block = %q, want %q", got, want)
	}
}

func TestIsGroupedActivitySessionBlock(t *testing.T) {
	flat := "- **Activity Session**:\n  - session-a — 10:28"
	grouped := "- **Activity Session**:\n  - **Update 2026-04-20 11:30** (initial)\n    - session-a — 10:28"

	if isGroupedActivitySessionBlock(flat) {
		t.Fatal("flat block should not be detected as grouped")
	}
	if !isGroupedActivitySessionBlock(grouped) {
		t.Fatal("grouped block should be detected as grouped")
	}
}

func TestUpgradeFlatActivitySessionBlock_WrapsUnderPreMigration(t *testing.T) {
	flat := "- **Activity Session**:\n  - session-a — 10:28, cmux\n  - session-b — 10:42, VSCode"
	ms := time.Date(2026, 4, 19, 10, 30, 0, 0, time.Local).UnixMilli()

	got := upgradeFlatActivitySessionBlock(flat, ms)

	if !isGroupedActivitySessionBlock(got) {
		t.Fatalf("upgraded block is not grouped: %q", got)
	}
	if !strings.Contains(got, "**Update 2026-04-19 10:30** (pre-migration)") {
		t.Fatalf("missing pre-migration header: %q", got)
	}
	// Session rows must be re-indented to 4 spaces under the group header.
	if !strings.Contains(got, "\n    - session-a — 10:28, cmux") {
		t.Fatalf("session-a not re-indented: %q", got)
	}
	if !strings.Contains(got, "\n    - session-b — 10:42, VSCode") {
		t.Fatalf("session-b not re-indented: %q", got)
	}

	// Re-applying must be idempotent.
	if second := upgradeFlatActivitySessionBlock(got, ms); second != got {
		t.Fatalf("upgrade not idempotent:\nfirst:  %q\nsecond: %q", got, second)
	}
}

func TestUpgradeFlatActivitySessionBlock_Empty(t *testing.T) {
	if got := upgradeFlatActivitySessionBlock("", 0); got != "" {
		t.Fatalf("empty input should yield empty output, got %q", got)
	}
}

// --- grouped writer (append new group) ---------------------------------------

func TestWriteGroupedActivitySessionBlock_InitialAndAppend(t *testing.T) {
	group1 := sessionGroup{
		Sessions: []ActivitySessionRow{
			{ID: "session-a", StartTime: time.Date(2026, 4, 20, 10, 28, 0, 0, time.Local).UnixMilli(), AppNames: `["cmux"]`},
		},
	}
	group2 := sessionGroup{
		Sessions: []ActivitySessionRow{
			{ID: "session-b", StartTime: time.Date(2026, 4, 20, 14, 2, 0, 0, time.Local).UnixMilli(), AppNames: `["VSCode"]`},
		},
	}

	var first strings.Builder
	writeGroupedActivitySessionBlock(&first, "",
		group1, "initial", time.Date(2026, 4, 20, 11, 30, 0, 0, time.Local))

	if !strings.Contains(first.String(), "**Update 2026-04-20 11:30** (initial)") {
		t.Fatalf("missing initial header: %q", first.String())
	}

	// Feeding the first block back in with a new group must append, not replace.
	existing := strings.TrimRight(first.String(), "\n")
	var second strings.Builder
	writeGroupedActivitySessionBlock(&second, existing,
		group2, "update", time.Date(2026, 4, 20, 14, 15, 0, 0, time.Local))

	out := second.String()
	if !strings.Contains(out, "**Update 2026-04-20 11:30** (initial)") {
		t.Fatalf("lost initial group after append: %q", out)
	}
	if !strings.Contains(out, "**Update 2026-04-20 14:15** (update)") {
		t.Fatalf("missing update header: %q", out)
	}
	if !strings.Contains(out, "\n    - session-a — 10:28, cmux") ||
		!strings.Contains(out, "\n    - session-b — 14:02, VSCode") {
		t.Fatalf("session rows missing or mis-indented: %q", out)
	}
}

// --- knowledge flat dedup merge ---------------------------------------------

func TestMergeFlatActivitySessionBlock_DedupsAndAppends(t *testing.T) {
	existing := `- **Activity Session**:
  - session-a — 10:28, cmux
  - session-b — 10:42, VSCode
`
	group := sessionGroup{
		Sessions: []ActivitySessionRow{
			{ID: "session-b", StartTime: time.Date(2026, 4, 20, 10, 42, 0, 0, time.Local).UnixMilli(), AppNames: `["VSCode"]`}, // duplicate
			{ID: "session-c", StartTime: time.Date(2026, 4, 20, 14, 2, 0, 0, time.Local).UnixMilli(), AppNames: `["Chrome"]`},
		},
	}

	out := mergeFlatActivitySessionBlock(existing, group)

	// Duplicate must not reappear.
	if strings.Count(out, "- session-b") != 1 {
		t.Fatalf("session-b appeared %d times, want 1: %q", strings.Count(out, "- session-b"), out)
	}
	// New session must be appended.
	if !strings.Contains(out, "- session-c — 14:02, Chrome") {
		t.Fatalf("session-c not appended: %q", out)
	}
	// Existing header preserved.
	if !strings.HasPrefix(strings.TrimLeft(out, "\n"), "- **Activity Session**:") {
		t.Fatalf("header missing: %q", out)
	}
}

func TestMergeFlatActivitySessionBlock_LegacySingleLineUpgrade(t *testing.T) {
	existing := "- **Activity Session**: session-legacy\n"
	group := sessionGroup{
		Sessions: []ActivitySessionRow{
			{ID: "session-new", StartTime: time.Date(2026, 4, 20, 14, 2, 0, 0, time.Local).UnixMilli(), AppNames: `["Chrome"]`},
		},
	}

	out := mergeFlatActivitySessionBlock(existing, group)

	if !strings.Contains(out, "- session-legacy") {
		t.Fatalf("legacy ID not preserved: %q", out)
	}
	if !strings.Contains(out, "- session-new — 14:02, Chrome") {
		t.Fatalf("new session not appended: %q", out)
	}
}

// --- snapshot + version history ---------------------------------------------

func TestSnapshotMemoryFile_CreatesDatedCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	entryID := "activity-test123"
	srcPath := filepath.Join(dir, entryID+".md")
	body := []byte("# Original\n")
	if err := os.WriteFile(srcPath, body, 0600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := snapshotMemoryFile(dir, entryID); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	historyDir := filepath.Join(dir, memoryHistoryDirName, entryID)
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(entries))
	}
	got, err := os.ReadFile(filepath.Join(historyDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("snapshot content mismatch: %q vs %q", got, body)
	}
}

func TestSnapshotMemoryFile_NoopWhenMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	if err := snapshotMemoryFile(dir, "missing-entry"); err != nil {
		t.Fatalf("unexpected error on missing source: %v", err)
	}
	// No history dir should have been created.
	if _, err := os.Stat(filepath.Join(dir, memoryHistoryDirName, "missing-entry")); !os.IsNotExist(err) {
		t.Fatalf("history dir should not exist, stat err = %v", err)
	}
}

func TestListAndGetMemoryVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	entryID := "activity-v1"

	// Create two snapshots manually (simulating two updates).
	historyDir := filepath.Join(dir, memoryHistoryDirName, entryID)
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tsEarly := time.Now().UnixMilli() - 10_000
	tsLate := time.Now().UnixMilli()
	if err := os.WriteFile(filepath.Join(historyDir, fmt.Sprintf("%d.md", tsEarly)), []byte("early"), 0600); err != nil {
		t.Fatalf("write early: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, fmt.Sprintf("%d.md", tsLate)), []byte("late"), 0600); err != nil {
		t.Fatalf("write late: %v", err)
	}

	app := &App{}
	versions, err := app.ListMemoryVersions(entryID)
	if err != nil {
		t.Fatalf("ListMemoryVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	// Newest first.
	if versions[0].TimestampMs != tsLate || versions[1].TimestampMs != tsEarly {
		t.Fatalf("wrong order: %+v", versions)
	}

	body, err := app.GetMemoryVersion(entryID, tsEarly)
	if err != nil {
		t.Fatalf("GetMemoryVersion: %v", err)
	}
	if body != "early" {
		t.Fatalf("body = %q, want %q", body, "early")
	}

	if _, err := app.GetMemoryVersion(entryID, 1); err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestListMemoryVersions_EmptyWhenNoHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	versions, err := app.ListMemoryVersions("activity-nohistory")
	if err != nil {
		t.Fatalf("expected nil error for missing history, got %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected empty slice, got %+v", versions)
	}
}

func TestListMemoryVersions_RejectsUnsafeIDs(t *testing.T) {
	app := &App{}
	for _, id := range []string{"", "..", "a/b", "a\\b", "../escape"} {
		if _, err := app.ListMemoryVersions(id); err == nil {
			t.Fatalf("expected error for id %q", id)
		}
	}
}

// --- SaveMemoryEntry history coupling ---------------------------------------

func TestSaveMemoryEntry_GeneratesHistoryOnUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	saved, err := app.SaveMemoryEntry("", "# v1\n")
	if err != nil {
		t.Fatalf("initial save: %v", err)
	}
	id := saved.ID

	// No history yet — first write has no prior content to snapshot.
	versions, err := app.ListMemoryVersions(id)
	if err != nil {
		t.Fatalf("list after create: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected no history after create, got %d", len(versions))
	}

	if _, err := app.SaveMemoryEntry(id, "# v2\n"); err != nil {
		t.Fatalf("second save: %v", err)
	}

	versions, err = app.ListMemoryVersions(id)
	if err != nil {
		t.Fatalf("list after update: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 history entry after update, got %d", len(versions))
	}
	body, err := app.GetMemoryVersion(id, versions[0].TimestampMs)
	if err != nil {
		t.Fatalf("GetMemoryVersion: %v", err)
	}
	if body != saved.Content {
		t.Fatalf("history body = %q, want %q", body, saved.Content)
	}
}

func TestSaveMemoryEntry_IdenticalUpdateDoesNotCreateHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	saved, err := app.SaveMemoryEntry("", "# unchanged\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveMemoryEntry(saved.ID, "# unchanged\n"); err != nil {
		t.Fatal(err)
	}
	versions, err := app.ListMemoryVersions(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("identical update created history: %+v", versions)
	}
}

func TestSaveMemoryEntry_ConcurrentUpdatesKeepBothSnapshots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	saved, err := app.SaveMemoryEntry("", "# v1\n")
	if err != nil {
		t.Fatalf("initial save: %v", err)
	}
	id := saved.ID

	// Two updates back-to-back should produce two distinct history snapshots
	// even when they might land in the same millisecond (O_EXCL + retry).
	if _, err := app.SaveMemoryEntry(id, "# v2\n"); err != nil {
		t.Fatalf("update v2: %v", err)
	}
	if _, err := app.SaveMemoryEntry(id, "# v3\n"); err != nil {
		t.Fatalf("update v3: %v", err)
	}

	versions, err := app.ListMemoryVersions(id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 history entries, got %d: %+v", len(versions), versions)
	}
}

func TestDeleteMemoryEntry_RemovesHistoryDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	saved, err := app.SaveMemoryEntry("", "# v1\n")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.ID
	if _, err := app.SaveMemoryEntry(id, "# v2\n"); err != nil {
		t.Fatalf("update: %v", err)
	}

	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	historyPath := filepath.Join(dir, memoryHistoryDirName, id)
	if _, err := os.Stat(historyPath); err != nil {
		t.Fatalf("history dir should exist before delete: %v", err)
	}

	if err := app.DeleteMemoryEntry(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(historyPath); !os.IsNotExist(err) {
		t.Fatalf("history dir should be gone after delete, stat err = %v", err)
	}
}

// --- discoverFiles skips .history -------------------------------------------

func TestWalkDir_SkipsHistoryDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}

	// Live file.
	if err := os.WriteFile(filepath.Join(dir, "live.md"), []byte("# live\n"), 0600); err != nil {
		t.Fatalf("write live: %v", err)
	}
	// Snapshot under .history should NOT be indexed.
	historyDir := filepath.Join(dir, memoryHistoryDirName, "live")
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "1.md"), []byte("# snapshot\n"), 0600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	store := &MemoryStore{baseDir: dir}
	var files []memoryFileRecord
	if err := store.walkDir(dir, MemorySourceMemory, &files); err != nil {
		t.Fatalf("walkDir: %v", err)
	}

	for _, f := range files {
		if strings.HasPrefix(f.Path, memoryHistoryDirName) || strings.Contains(f.Path, "/"+memoryHistoryDirName+"/") {
			t.Fatalf("history file was indexed: %+v", f)
		}
	}
	if len(files) != 1 || files[0].Path != "live.md" {
		t.Fatalf("expected only live.md, got %+v", files)
	}
}

// --- helpers -----------------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
