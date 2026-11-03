package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAboutYouConfirmChangesOnlyFieldWithoutHistory(t *testing.T) {
	for _, original := range []string{
		"---\ntype: Isoform Memory\ntitle: 'Exact title' # keep comment\ntimestamp: 2026-10-01T10:00:00Z\nsource: activity\ndimension: user_insight\nread_provenance: '{\"version\":1}'\n---\n\n# Body  \n\nKeep exact spacing.\n\n",
		"\ufeff---\r\ntype: Isoform Memory\r\ntitle: Exact title\r\nsource: activity\r\n---\r\n\r\nBody  \r\n",
		"# Legacy title\n- **Source**: activity\n- **Updated**: 2026-10-01 10:00\n\nBody  \n",
	} {
		t.Run(fmt.Sprintf("format-%d", len(original)), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "belief.md")
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			app := &App{}
			before, err := app.GetMemoryEntry("belief")
			if err != nil {
				t.Fatal(err)
			}
			got, err := app.ConfirmMemoryEntry("belief")
			if err != nil {
				t.Fatal(err)
			}
			if memoryConfirmedAt(got.Content) == 0 {
				t.Fatal("confirmation missing")
			}
			start, end, _, err := confirmationHeader(got.Content)
			if err != nil {
				t.Fatal(err)
			}
			header := got.Content[start:end]
			var field string
			for _, line := range strings.SplitAfter(header, "\n") {
				if confirmedAtLine.MatchString(line) {
					field = line
				}
			}
			restored := strings.Replace(got.Content, field, "", 1)
			if strings.HasPrefix(original, "#") {
				restored = strings.TrimPrefix(restored, "---\n---\n")
			}
			if restored != original {
				t.Fatalf("confirmation rewrote other bytes:\n%q\nwant:\n%q", restored, original)
			}
			if got.Title != before.Title || got.Source != before.Source || got.Dimension != before.Dimension || got.UpdatedAt != before.UpdatedAt || got.HasHistory {
				t.Fatalf("metadata changed: before=%+v after=%+v", before, got)
			}
			if _, err := os.Stat(filepath.Join(dir, memoryHistoryDirName, "belief")); !os.IsNotExist(err) {
				t.Fatal("confirmation created history")
			}
			twice, err := app.ConfirmMemoryEntry("belief")
			if err != nil || twice.Content != got.Content {
				t.Fatalf("repeat confirmation changed: %v", err)
			}
		})
	}
}

func TestAboutYouConfirmRefusesForgottenAndInvalidID(t *testing.T) {
	app, dir := newForgetTestApp(t)
	content := "# Suppressed\n\nI prefer concise reviews.\n"
	if err := os.WriteFile(filepath.Join(dir, "blocked.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recordMemoryTombstone(dir, "blocked", []string{content}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"blocked", "../outside", "missing"} {
		if _, err := app.ConfirmMemoryEntry(id); err == nil {
			t.Fatalf("confirmed %q", id)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blocked.md"))
	if string(data) != content {
		t.Fatal("suppressed file changed")
	}
}

func TestAboutYouUserEditConfirmsAndIdenticalSaveRemainsNoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	first, err := app.SaveMemoryEntry("edited", "# A preference\n\nKeep reviews concise.")
	if err != nil {
		t.Fatal(err)
	}
	if memoryConfirmedAt(first.Content) == 0 || first.Source != "user_edit" {
		t.Fatalf("edit unconfirmed: %+v", first)
	}
	second, err := app.SaveMemoryEntry("edited", first.Content)
	if err != nil || second.Content != first.Content || second.HasHistory {
		t.Fatalf("identical save regressed: %+v %v", second, err)
	}
	got, err := app.GetAboutYou()
	if err != nil || got.Totals.Confirmed != 1 || got.Beliefs[0].Confidence != 4 {
		t.Fatalf("view=%+v err=%v", got, err)
	}
}

func TestAboutYouConfidenceAndStatusRules(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name, source, status string
		sessions, days       int
		confirmed            int64
		first                int64
		confidence           int
	}{
		{"confirmed", "activity", "confirmed", 0, 0, now.UnixMilli(), now.UnixMilli(), 4},
		{"user edit", "user_edit", "confirmed", 0, 0, 0, now.AddDate(0, 0, -10).UnixMilli(), 4},
		{"four sessions", "activity", "inferred", 4, 1, 0, now.AddDate(0, 0, -1).UnixMilli(), 3},
		{"two days", "activity", "inferred", 2, 2, 0, now.AddDate(0, 0, -1).UnixMilli(), 3},
		{"three sessions", "activity", "inferred", 3, 1, 0, now.AddDate(0, 0, -1).UnixMilli(), 2},
		{"two sessions", "activity", "inferred", 2, 1, 0, now.AddDate(0, 0, -1).UnixMilli(), 2},
		{"single", "activity", "inferred", 1, 1, 0, now.AddDate(0, 0, -1).UnixMilli(), 1},
		{"new today", "activity", "learned_today", 0, 0, 0, now.UnixMilli(), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, confidence := beliefStatusConfidence(AboutYouBelief{SessionCount: tc.sessions, DayCount: tc.days, ConfirmedAt: tc.confirmed, FirstLearnedAt: tc.first}, tc.source, now)
			if status != tc.status || confidence != tc.confidence {
				t.Fatalf("got %s/%d want %s/%d", status, confidence, tc.status, tc.confidence)
			}
		})
	}
}

func TestAboutYouListParityNewWeekAndEarliestHistory(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	now := time.Now()
	today := localBeliefDay(now)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 9 {
		at := today.AddDate(0, 0, -i).Add(time.Hour)
		content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": fmt.Sprintf("Belief %d", i), "timestamp": at.Format(time.RFC3339), "source": "activity"}, "- Keep the plan clear.")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("b%d.md", i)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.ConfirmMemoryEntry("b1"); err != nil {
		t.Fatal(err)
	}
	history, err := memoryHistoryDir(dir, "b0")
	if err != nil {
		t.Fatal(err)
	}
	old := today.AddDate(0, 0, -30)
	if err := os.WriteFile(filepath.Join(history, fmt.Sprintf("%d.md", today.AddDate(0, 0, -2).UnixMilli())), []byte(renderOKFDocument(map[string]any{"type": "Isoform Memory", "timestamp": old.Format(time.RFC3339)}, "Prior version")), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := app.GetMemoryEntries()
	if err != nil {
		t.Fatal(err)
	}
	view, err := app.GetAboutYou()
	if err != nil {
		t.Fatal(err)
	}
	var ids, actual []string
	var bytes int64
	for _, entry := range entries {
		ids = append(ids, entry.ID)
		bytes += entry.SizeBytes
	}
	for _, belief := range view.Beliefs {
		actual = append(actual, belief.ID)
		if belief.ID == "b0" && belief.FirstLearnedAt != old.UnixMilli() {
			t.Fatalf("first learned=%d want %d", belief.FirstLearnedAt, old.UnixMilli())
		}
	}
	if !reflect.DeepEqual(ids, actual) || !reflect.DeepEqual(view.NewThisWeek, []string{"b2", "b3", "b4", "b5", "b6"}) || view.Totals.Beliefs != 9 || view.Totals.Confirmed != 1 || view.Storage.Bytes != bytes || view.Storage.Files != 9 {
		t.Fatalf("view=%+v ids=%v", view, ids)
	}
	app.closedEyes.enabled.Store(true)
	closed, err := app.GetAboutYou()
	if err != nil || len(closed.Beliefs) != len(entries) {
		t.Fatalf("owner list changed: %+v %v", closed, err)
	}
}

func TestAboutYouDistinctReadableSessionsAndLocalDays(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	now := time.Now()
	insertTestSession(t, app.activity.store, "s1", "done", localBeliefDay(now).Add(-time.Minute).UnixMilli())
	insertTestSession(t, app.activity.store, "s2", "done", localBeliefDay(now).Add(time.Minute).UnixMilli())
	dir, _ := memoryDir()
	content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Repeated preference", "timestamp": now.Format(time.RFC3339), "activity_sessions": []string{"s1", "s2", "s1"}}, "- Keep the plan clear.")
	if err := os.WriteFile(filepath.Join(dir, "repeated.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := app.GetAboutYou()
	if err != nil {
		t.Fatal(err)
	}
	belief := view.Beliefs[0]
	if belief.SessionCount != 2 || belief.DayCount != 2 || belief.Confidence != 3 {
		t.Fatalf("belief=%+v", belief)
	}
	app.closedEyes.enabled.Store(true)
	view, err = app.GetAboutYou()
	if err != nil || view.Beliefs[0].SessionCount != 0 || view.Beliefs[0].DayCount != 0 {
		t.Fatalf("derived private counts=%+v %v", view, err)
	}
}

func TestAboutYouLegacyConfirmationPreservesProvenanceAndTranslation(t *testing.T) {
	original := "# Writing style\n- **Source**: activity\n- **Updated**: 2026-10-01 10:00\n- **Apps**: Editor\n- **Activity Session**: session-one\n- **Dimension**: user_insight\n\nKeep reviews clear.\n"
	confirmed, err := setMemoryConfirmedAt(original, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parseMemoryFrontmatter(original), parseMemoryFrontmatter(confirmed)) {
		t.Fatal("confirmation lost legacy provenance")
	}
	if stripMemoryFrontmatter(original) != stripMemoryFrontmatter(confirmed) {
		t.Fatal("confirmation entered substantive memory body")
	}
	parts := splitMarkdownTranslationParts(confirmed)
	translated := parts.Recombine("# Review preference\n\nKeep the plan readable.")
	if memoryConfirmedAt(translated) != memoryConfirmedAt(confirmed) || !reflect.DeepEqual(parseMemoryFrontmatter(original), parseMemoryFrontmatter(translated)) {
		t.Fatal("translation lost confirmation or provenance")
	}
}
