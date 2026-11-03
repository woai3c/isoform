package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleMemoryContent = `# Prefers bun over npm

- **Updated**: 2026-04-20 11:30
- **Source**: conversation
- **Dimension**: user_insight
- **Apps**: kitty, VS Code

## Memory Points

- User prefers bun over npm for all Node.js projects.
- Installs dependencies with ` + "`bun install`" + ` by default.
`

func TestGetMemoryEntry_ReturnsFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	entryID := "memory-user-bun"
	if err := os.WriteFile(filepath.Join(dir, entryID+".md"), []byte(sampleMemoryContent), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	app := &App{}
	entry, err := app.GetMemoryEntry(entryID)
	if err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}

	if entry.ID != entryID {
		t.Errorf("ID = %q, want %q", entry.ID, entryID)
	}
	if entry.Title != "Prefers bun over npm" {
		t.Errorf("Title = %q, want %q", entry.Title, "Prefers bun over npm")
	}
	if entry.Dimension != "user_insight" {
		t.Errorf("Dimension = %q, want %q", entry.Dimension, "user_insight")
	}
	if entry.Source != "conversation" {
		t.Errorf("Source = %q, want %q", entry.Source, "conversation")
	}
	if entry.Content != sampleMemoryContent {
		t.Errorf("Content mismatch (got %d bytes, want %d)", len(entry.Content), len(sampleMemoryContent))
	}
	if entry.SizeBytes != int64(len(sampleMemoryContent)) {
		t.Errorf("SizeBytes = %d, want %d", entry.SizeBytes, len(sampleMemoryContent))
	}
	if entry.ModTime <= 0 {
		t.Errorf("ModTime = %d, want > 0", entry.ModTime)
	}
}

func TestGetMemoryEntry_ReturnsFieldsFromOKF(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	entryID := "memory-okf"
	content := `---
okf_version: "0.1"
type: Yansu Memory
title: Prefers OKF
resource: yansu://memory/memory-okf
tags:
- memory
- user_insight
timestamp: "2026-06-22T00:00:00Z"
source: conversation
dimension: user_insight
apps:
- VS Code
activity_sessions:
- session-1
---

## Memory Points

- User wants knowledge stored as OKF.
`
	if err := os.WriteFile(filepath.Join(dir, entryID+".md"), []byte(content), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	app := &App{}
	entry, err := app.GetMemoryEntry(entryID)
	if err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}
	if entry.Title != "Prefers OKF" {
		t.Errorf("Title = %q, want Prefers OKF", entry.Title)
	}
	if entry.Dimension != "user_insight" {
		t.Errorf("Dimension = %q, want user_insight", entry.Dimension)
	}
	if entry.Source != "conversation" {
		t.Errorf("Source = %q, want conversation", entry.Source)
	}
	if len(entry.Apps) != 1 || entry.Apps[0] != "VS Code" {
		t.Errorf("Apps = %+v, want [VS Code]", entry.Apps)
	}
	if len(entry.ActivitySessionIDs) != 1 || entry.ActivitySessionIDs[0] != "session-1" {
		t.Errorf("ActivitySessionIDs = %+v, want [session-1]", entry.ActivitySessionIDs)
	}
	if entry.UpdatedAt == 0 {
		t.Errorf("UpdatedAt = 0, want parsed timestamp")
	}
}

func TestGetMemoryEntry_ReturnsFieldsFromOKFWithCRLF(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	entryID := "memory-okf-crlf"
	content := strings.ReplaceAll(`---
okf_version: "0.1"
type: Yansu Memory
title: CRLF OKF
timestamp: "2026-06-22T00:00:00Z"
source: conversation
dimension: user_insight
---

## Memory Points

- CRLF files still parse.
`, "\n", "\r\n")
	if err := os.WriteFile(filepath.Join(dir, entryID+".md"), []byte(content), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	app := &App{}
	entry, err := app.GetMemoryEntry(entryID)
	if err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}
	if entry.Title != "CRLF OKF" {
		t.Errorf("Title = %q, want CRLF OKF", entry.Title)
	}
	if entry.Dimension != "user_insight" {
		t.Errorf("Dimension = %q, want user_insight", entry.Dimension)
	}
}

func TestGetMemoryEntries_SkipsOKFReservedFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	for name, body := range map[string]string{
		"index.md":  "# Index\n",
		"log.md":    "# Log\n",
		"memory.md": "# Real Memory\n\n## Memory Points\n\n- real\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	app := &App{}
	entries, err := app.GetMemoryEntries()
	if err != nil {
		t.Fatalf("GetMemoryEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "memory" {
		t.Fatalf("entries = %+v, want only memory", entries)
	}
}

func TestGetMemoryEntry_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// memoryDir must exist for the stat to return a normal os.ErrNotExist
	// (not a "directory missing" error).
	if _, err := memoryDir(); err != nil {
		t.Fatalf("memoryDir: %v", err)
	}

	app := &App{}
	_, err := app.GetMemoryEntry("does-not-exist")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestGetMemoryEntry_RejectsUnsafeIDs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := memoryDir(); err != nil {
		t.Fatalf("memoryDir: %v", err)
	}

	unsafe := []string{
		"../etc/passwd",
		"foo/bar",
		"foo\\bar",
		"..",
		"../../secrets",
	}
	app := &App{}
	for _, id := range unsafe {
		_, err := app.GetMemoryEntry(id)
		if err == nil {
			t.Errorf("id %q: expected error, got nil", id)
			continue
		}
		// Path-traversal IDs should fail with the explicit validation error,
		// never reach os.ReadFile.
		if errors.Is(err, os.ErrNotExist) {
			t.Errorf("id %q: validation should reject before stat (got os.ErrNotExist)", id)
		}
	}
}

func TestParseMemoryPoints(t *testing.T) {
	points := parseMemoryPoints(sampleMemoryContent)
	want := []string{
		"User prefers bun over npm for all Node.js projects.",
		"Installs dependencies with `bun install` by default.",
	}
	if len(points) != len(want) {
		t.Fatalf("parseMemoryPoints returned %d points, want %d: %+v", len(points), len(want), points)
	}
	for i, w := range want {
		if points[i] != w {
			t.Errorf("point[%d] = %q, want %q", i, points[i], w)
		}
	}
}

func TestParseMemoryPoints_SkipsHeaderAndBlanks(t *testing.T) {
	md := "# Title\n\n" +
		"- **Updated**: 2026-04-21 09:32\n" +
		"- **Source**: activity\n\n" +
		"## Memory Points\n\n" +
		"- First bullet\n" +
		"- \n" + // blank bullet body — must be dropped
		"-\n" + // malformed, not a bullet
		"- Second bullet\n"
	got := parseMemoryPoints(md)
	want := []string{"First bullet", "Second bullet"}
	if len(got) != len(want) {
		t.Fatalf("got %d points, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("point[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestParseMemoryPoints_KeepsBoldBullets(t *testing.T) {
	// Regression for the original `- **` filter that would drop legitimate
	// bullets starting with bold text. The LLM can emit bullets like
	// `- **Note**: prefers verbose errors` and those must survive.
	md := "# Title\n\n" +
		"- **Updated**: 2026-04-21 09:32\n" +
		"- **Source**: conversation\n\n" +
		"## Memory Points\n\n" +
		"- **Note**: prefers verbose errors\n" +
		"- **Preference**: uses bun over npm\n" +
		"- Plain bullet also survives\n"
	got := parseMemoryPoints(md)
	want := []string{
		"**Note**: prefers verbose errors",
		"**Preference**: uses bun over npm",
		"Plain bullet also survives",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d points, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("point[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestParseMemoryPoints_NoMarkerReturnsNil(t *testing.T) {
	// Files without the "## Memory Points" marker are either not memory
	// entries or hand-edited past recognition — returning nil makes the
	// merge fall through to the LLM's list alone rather than treating
	// the whole file body as bullets.
	md := "# Random file\n\n" +
		"- some bullet\n" +
		"- another bullet\n"
	got := parseMemoryPoints(md)
	if got != nil {
		t.Errorf("expected nil when marker is absent, got %+v", got)
	}
}

func TestMergeMemoryPoints_UnionsAndDedupes(t *testing.T) {
	existing := []string{
		"Uses Xero for accounting",
		"Altea Group contact via Ioann Fainsilber",
		"Researches contacts on LinkedIn before meetings",
	}
	incoming := []string{
		"Researches contacts on LinkedIn before meetings",    // duplicate of existing[2]
		"GTCR is a prospect",                                 // new
		"Follows up with prospects via email after meetings", // new
	}
	merged := mergeMemoryPoints(existing, incoming)

	// All three existing bullets must survive — this is the bug we're fixing.
	for _, p := range existing {
		found := false
		for _, m := range merged {
			if m == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("existing bullet dropped from merge: %q", p)
		}
	}
	// New, non-duplicate bullets must be appended.
	wantNew := []string{"GTCR is a prospect", "Follows up with prospects via email after meetings"}
	for _, p := range wantNew {
		found := false
		for _, m := range merged {
			if m == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("new bullet missing from merge: %q", p)
		}
	}
	// Duplicate must appear exactly once.
	count := 0
	for _, m := range merged {
		if m == "Researches contacts on LinkedIn before meetings" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("duplicate bullet appeared %d times, want 1", count)
	}
	// Ordering: existing preserved, new appended.
	if merged[0] != existing[0] {
		t.Errorf("merged[0] = %q, want %q (existing ordering preserved)", merged[0], existing[0])
	}
}

func TestMergeMemoryPoints_WhitespaceNormalized(t *testing.T) {
	existing := []string{"  uses bun  "}
	incoming := []string{"uses bun"}
	merged := mergeMemoryPoints(existing, incoming)
	if len(merged) != 1 {
		t.Fatalf("trimmed duplicate should collapse to 1, got %d: %+v", len(merged), merged)
	}
	if merged[0] != "uses bun" {
		t.Errorf("merged[0] = %q, want trimmed %q", merged[0], "uses bun")
	}
}

func TestMergeMemoryPoints_EmptyInputs(t *testing.T) {
	// No existing — returns incoming unchanged (minus blanks).
	got := mergeMemoryPoints(nil, []string{"a", "", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("empty-existing case: got %+v, want [a b]", got)
	}
	// No incoming — returns existing unchanged.
	got = mergeMemoryPoints([]string{"a", "b"}, nil)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("empty-incoming case: got %+v, want [a b]", got)
	}
}

func BenchmarkGetMemoryEntries59(b *testing.B) {
	b.Setenv("HOME", b.TempDir())
	dir, err := memoryDir()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 59; i++ {
		sessions := []string{fmt.Sprintf("session-%02d-a", i), fmt.Sprintf("session-%02d-b", i)}
		frontmatter := map[string]any{
			"type":              "Isoform Memory",
			"title":             fmt.Sprintf("Review preference %02d", i),
			"timestamp":         "2026-10-25T12:00:00Z",
			"source":            "activity",
			"dimension":         "user_insight",
			"apps":              []string{"Code", "Browser"},
			"activity_sessions": sessions,
			"topic_ids":         []string{"project-reviews"},
			"read_provenance":   memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: sessions, Apps: []string{"Code", "Browser"}, PromptSafe: true}),
		}
		if i%3 != 0 {
			frontmatter["evidence_sessions"] = sessions
		}
		if i%4 == 0 {
			frontmatter["area"] = "work"
		}
		id := fmt.Sprintf("belief-%02d", i)
		content := renderOKFDocument(frontmatter, "## Memory Points\n\n- Keep project reviews clear and concise.")
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
			b.Fatal(err)
		}
		if i%3 == 0 {
			history, err := memoryHistoryDir(dir, id)
			if err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(history, "1.md"), []byte(content), 0600); err != nil {
				b.Fatal(err)
			}
		}
	}

	app := &App{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries, err := app.GetMemoryEntries()
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) != 59 {
			b.Fatalf("entries = %d, want 59", len(entries))
		}
	}
}
