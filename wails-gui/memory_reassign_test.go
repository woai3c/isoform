package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/storage"
)

// newReassignTestApp returns an App rooted at a temp HOME with a real
// storage.Store so the principal lookups inside ReassignMemoryAttribution
// have a real DB to consult.
func newReassignTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, "yansu-test.db")
	st, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &App{store: st}
}

func writeFirstPersonMemoryFixture(t *testing.T, id, title string) string {
	t.Helper()
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	content := "# " + title + "\n\n" +
		"- **Updated**: 2026-05-21 12:00\n" +
		"- **Source**: activity\n" +
		"- **Dimension**: user_insight\n\n" +
		"## Memory Points\n\n" +
		"- I love chestnuts.\n" +
		"- My favorite season is autumn.\n" +
		"- React is a UI library.\n"
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func readReassignmentLog(t *testing.T) []MemoryReassignmentEntry {
	t.Helper()
	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	path := filepath.Join(dir, memoryReassignmentsLogName)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	var out []MemoryReassignmentEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e MemoryReassignmentEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

// ── Happy path ──

func TestReassignMemoryAttribution_RewritesContentAndLogs(t *testing.T) {
	a := newReassignTestApp(t)
	target, err := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind:        storage.PrincipalKindContact,
		DisplayName: "Zhang San",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	writeFirstPersonMemoryFixture(t, "mem-1", "I love chestnuts")

	if err := a.ReassignMemoryAttribution("mem-1", target.ID, "Zhang San said it in WeChat"); err != nil {
		t.Fatalf("ReassignMemoryAttribution: %v", err)
	}

	rewritten, err := a.GetMemoryEntry("mem-1")
	if err != nil {
		t.Fatalf("GetMemoryEntry: %v", err)
	}

	if !strings.Contains(rewritten.Content, "- **Dimension**: topic_highlight") {
		t.Errorf("dimension not capped:\n%s", rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "Reassigned to") || !strings.Contains(rewritten.Content, "Zhang San") {
		t.Errorf("reassign header missing:\n%s", rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "Zhang San said it in WeChat") {
		t.Errorf("reason not embedded:\n%s", rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "Zhang San said: I love chestnuts") {
		t.Errorf("first-person bullet not rewritten:\n%s", rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "Zhang San said: My favorite season") {
		t.Errorf("My-prefix bullet not rewritten:\n%s", rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "- React is a UI library.") {
		t.Errorf("non-first-person bullet should remain intact:\n%s", rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "# I love chestnuts — reassigned to Zhang San") {
		t.Errorf("title not annotated:\n%s", rewritten.Content)
	}

	logs := readReassignmentLog(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(logs))
	}
	got := logs[0]
	if got.MemoryID != "mem-1" {
		t.Errorf("MemoryID = %q, want mem-1", got.MemoryID)
	}
	if got.CorrectPrincipalID != target.ID {
		t.Errorf("CorrectPrincipalID = %q, want %q", got.CorrectPrincipalID, target.ID)
	}
	if got.CorrectPrincipalName != "Zhang San" {
		t.Errorf("CorrectPrincipalName = %q", got.CorrectPrincipalName)
	}
	if got.CorrectPrincipalKind != storage.PrincipalKindContact {
		t.Errorf("CorrectPrincipalKind = %q", got.CorrectPrincipalKind)
	}
	if got.OriginalDimension != "user_insight" {
		t.Errorf("OriginalDimension = %q", got.OriginalDimension)
	}
	if got.Reason != "Zhang San said it in WeChat" {
		t.Errorf("Reason = %q", got.Reason)
	}
	if got.ReassignedAt == 0 {
		t.Errorf("ReassignedAt not set")
	}
}

func TestReassignMemoryAttribution_HandlesCJKFirstPerson(t *testing.T) {
	a := newReassignTestApp(t)
	target, _ := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "张三",
	})
	dir, _ := memoryDir()
	content := "# 我喜欢栗子\n\n" +
		"- **Updated**: 2026-05-21 12:00\n" +
		"- **Source**: activity\n" +
		"- **Dimension**: user_insight\n\n" +
		"## Memory Points\n\n" +
		"- 我喜欢吃栗子。\n" +
		"- 我的最爱是秋天。\n"
	if err := os.WriteFile(filepath.Join(dir, "cjk-1.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := a.ReassignMemoryAttribution("cjk-1", target.ID, "微信里张三说的"); err != nil {
		t.Fatalf("ReassignMemoryAttribution: %v", err)
	}

	rewritten, _ := a.GetMemoryEntry("cjk-1")
	if !strings.Contains(rewritten.Content, "张三 说:我喜欢吃栗子") {
		t.Errorf("CJK first-person not rewritten:\n%s", rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "张三 说:我的最爱") {
		t.Errorf("CJK 我的-prefix not rewritten:\n%s", rewritten.Content)
	}
}

// ── Error paths ──

func TestReassignMemoryAttribution_RejectsEmptyMemoryID(t *testing.T) {
	a := newReassignTestApp(t)
	if err := a.ReassignMemoryAttribution("  ", "x", "reason"); err == nil {
		t.Errorf("expected error for empty memoryID")
	}
}

func TestReassignMemoryAttribution_RejectsEmptyPrincipalID(t *testing.T) {
	a := newReassignTestApp(t)
	writeFirstPersonMemoryFixture(t, "mem-2", "x")
	if err := a.ReassignMemoryAttribution("mem-2", "", "reason"); err == nil {
		t.Errorf("expected error for empty correctPrincipalID")
	}
}

func TestReassignMemoryAttribution_MissingMemoryErrors(t *testing.T) {
	a := newReassignTestApp(t)
	target, _ := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "X",
	})
	err := a.ReassignMemoryAttribution("ghost", target.ID, "")
	if err == nil || !strings.Contains(err.Error(), "load entry") {
		t.Errorf("expected wrapped load error, got %v", err)
	}
}

func TestReassignMemoryAttribution_MissingPrincipalErrors(t *testing.T) {
	a := newReassignTestApp(t)
	writeFirstPersonMemoryFixture(t, "mem-3", "x")
	err := a.ReassignMemoryAttribution("mem-3", "ghost-principal", "")
	if err == nil || !strings.Contains(err.Error(), "load target principal") {
		t.Errorf("expected wrapped target error, got %v", err)
	}
}

func TestReassignMemoryAttribution_NoStoreErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFirstPersonMemoryFixture(t, "mem-4", "x")
	a := &App{}
	if err := a.ReassignMemoryAttribution("mem-4", "anyone", "reason"); err == nil {
		t.Errorf("expected error when store is nil")
	}
}

// ── Re-reassignment ──

func TestReassignMemoryAttribution_SecondCallReplacesHeader(t *testing.T) {
	a := newReassignTestApp(t)
	zhang, _ := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "Zhang San",
	})
	li, _ := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "Li Si",
	})
	writeFirstPersonMemoryFixture(t, "mem-5", "I like chestnuts")

	if err := a.ReassignMemoryAttribution("mem-5", zhang.ID, "first call"); err != nil {
		t.Fatalf("first reassign: %v", err)
	}
	if err := a.ReassignMemoryAttribution("mem-5", li.ID, "second call"); err != nil {
		t.Fatalf("second reassign: %v", err)
	}

	rewritten, _ := a.GetMemoryEntry("mem-5")
	// Only one Reassigned-to header should remain (the latest).
	count := strings.Count(rewritten.Content, "- **Reassigned to**:")
	if count != 1 {
		t.Errorf("expected single Reassigned-to header, got %d:\n%s", count, rewritten.Content)
	}
	if !strings.Contains(rewritten.Content, "Li Si") || strings.Contains(rewritten.Content, "id="+zhang.ID) {
		t.Errorf("latest reassignment didn't win:\n%s", rewritten.Content)
	}

	// Both calls should be in the log.
	logs := readReassignmentLog(t)
	if len(logs) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(logs))
	}
}

// ── Reason round-trips ──

func TestReassignMemoryAttribution_ReasonWithSpecialCharsRoundtrips(t *testing.T) {
	a := newReassignTestApp(t)
	target, _ := a.store.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "Z",
	})
	writeFirstPersonMemoryFixture(t, "mem-6", "x")

	tricky := "multi\nline\twith \"quotes\" and CJK 你好/世界"
	if err := a.ReassignMemoryAttribution("mem-6", target.ID, tricky); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	logs := readReassignmentLog(t)
	if len(logs) != 1 || logs[0].Reason != tricky {
		t.Errorf("reason did not roundtrip: %+v", logs)
	}
}

// ── rewriteMemoryBulletForReassignment ──

func TestRewriteMemoryBulletForReassignment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"first_person_I", "- I love bun.", "- Z said: I love bun."},
		{"first_person_My", "- My favorite editor is Vim.", "- Z said: My favorite editor is Vim."},
		{"first_person_cjk", "- 我用 Vim。", "- Z 说:我用 Vim。"},
		{"first_person_cjk_no_space", "- 我喜欢栗子。", "- Z 说:我喜欢栗子。"},
		{"first_person_cjk_de", "- 我的最爱是秋天。", "- Z 说:我的最爱是秋天。"},
		{"non_first_person_kept", "- React is a UI library.", "- React is a UI library."},
		{"non_bullet_kept", "## Section", "## Section"},
		{"indented_bullet", "  - I tested this.", "  - Z said: I tested this."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteMemoryBulletForReassignment(c.in, "Z")
			if got != c.want {
				t.Errorf("rewrite(%q)\n got=%q\nwant=%q", c.in, got, c.want)
			}
		})
	}
}

// ── rewriteMemoryForReassignment edge cases ──

func TestRewriteMemoryForReassignment_NoDimensionLineInjectsHeader(t *testing.T) {
	original := "# Old title\n\n- **Source**: activity\n\n## Memory Points\n\n- I like X.\n"
	rewritten := rewriteMemoryForReassignment(original, "Z", "z-id", "because")
	if !strings.Contains(rewritten, "- **Dimension**: topic_highlight") {
		t.Errorf("dimension header not injected:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "- **Reassigned to**: Z (id=z-id) — because") {
		t.Errorf("reassignment header not injected with reason:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "Z said: I like X") {
		t.Errorf("bullet not rewritten:\n%s", rewritten)
	}
}

func TestRewriteMemoryForReassignment_NoReasonOmitsDashSuffix(t *testing.T) {
	original := "# t\n\n- **Dimension**: user_insight\n\n## Memory Points\n\n- foo\n"
	rewritten := rewriteMemoryForReassignment(original, "Z", "z-id", "")
	if !strings.Contains(rewritten, "- **Reassigned to**: Z (id=z-id)\n") {
		t.Errorf("reassignment header should not have trailing — when reason empty:\n%s", rewritten)
	}
	if strings.Contains(rewritten, "(id=z-id) — ") {
		t.Errorf("dash suffix leaked despite empty reason:\n%s", rewritten)
	}
}
