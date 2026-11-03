package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeUserControlMemory(t *testing.T, dir, id, title, source, body string) string {
	t.Helper()
	content := "# " + title + "\n\n- **Source**: " + source + "\n- **Dimension**: user_insight\n\n## Memory Points\n\n- " + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return content
}

func TestMemoryInjectionUserEditPrecedesObservationAndRefreshesStaleChunk(t *testing.T) {
	dir := t.TempDir()
	edited := writeUserControlMemory(t, dir, "edited", "Writing preference", "user_edit", "Use concise prose.")
	writeUserControlMemory(t, dir, "observed", "Writing preference", "activity", "Use elaborate prose.")
	writeUserControlMemory(t, dir, "other", "Meeting preference", "activity", "Meet after lunch.")
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeUserControlMemory(t, dir, "nested/project", "Project notes", "activity", "Use the documented release process.")
	results := []MemorySearchResult{
		{Path: "observed.md", Source: "memory", Score: 0.99, Snippet: "Use elaborate prose."},
		{Path: "other.md", Source: "memory", Score: 0.98, Snippet: "Meet after lunch."},
		{Path: "edited.md", Source: "memory", Score: 0.1, Snippet: "Stale text before the correction."},
		{Path: "edited.md", Source: "memory", Score: 0.09, Snippet: "Another stale chunk."},
		{Path: "edited.md", Source: "knowledge", Score: 0.9, Snippet: "Independent knowledge."},
		{Path: "nested/project.md", Source: "memory", Score: 0.9, Snippet: "Use the documented release process."},
	}
	filtered, err := applyMemoryUserControls(dir, results)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 4 || filtered[0].Path != "edited.md" || filtered[0].Snippet != edited {
		t.Fatalf("user correction did not win: %+v", filtered)
	}
	if filtered[1].Path != "other.md" || filtered[2].Source != "knowledge" || filtered[3].Path != "nested/project.md" {
		t.Fatalf("unrelated context changed: %+v", filtered)
	}
	selected := buildMemoryContextSelection(filtered, memoryInjectMinScore, len(strings.TrimSpace(edited)))
	if selected.SelectedCount != 1 || selected.Results[0].Path != "edited.md" {
		t.Fatalf("budget did not prioritize user edit: %+v", selected)
	}
}

func TestForgottenMemoryExcludedFromInjectionDreamAndHistoricalSources(t *testing.T) {
	a := newRejectTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	content := writeUserControlMemory(t, dir, "forgotten", "Travel preference", "activity", "I take the train to the coast.")
	if err := recordMemoryTombstone(dir, "forgotten", []string{content}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Simulate an external re-observation under a new ID bypassing app writes.
	writeUserControlMemory(t, dir, "reobserved", "Travel preference", "activity", "I take the train to the coast.")
	writeUserControlMemory(t, dir, "edited", "Writing preference", "user_edit", "Use concise prose.")
	writeUserControlMemory(t, dir, "allowed", "Meeting preference", "activity", "Meet after lunch.")
	results, err := applyMemoryUserControls(dir, []MemorySearchResult{
		{Path: "forgotten.md", Source: "memory", Score: 0.9, Snippet: "forgotten"},
		{Path: "reobserved.md", Source: "memory", Score: 0.9, Snippet: "reobserved"},
		{Path: "missing.md", Source: "memory", Score: 0.9, Snippet: "stale removed record"},
		{Path: "allowed.md", Source: "memory", Score: 0.9, Snippet: "Meet after lunch."},
	})
	if err != nil || len(results) != 1 || results[0].Path != "allowed.md" {
		t.Fatalf("injection results=%+v err=%v", results, err)
	}
	candidates, err := NewMemoryDreamService(a).gatherCandidates(context.Background())
	if err != nil || len(candidates) != 1 || candidates[0].ID != "allowed" {
		t.Fatalf("Dream candidates=%+v err=%v", candidates, err)
	}
	sources, err := GatherTaskContextSources(TaskContextSourceRequest{CutoffAt: time.Now().Add(time.Minute).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range sources.Candidates {
		if strings.Contains(candidate.Content, "train to the coast") {
			t.Fatalf("forgotten observation entered Self Dream/Purpose inputs: %+v", candidate)
		}
	}
	if !taskContextSourceHasExclusion(sources.Exclusions, taskContextSourceMemoryKind, TaskContextSourceReasonRetracted) {
		t.Fatalf("missing forget exclusion receipt: %+v", sources.Exclusions)
	}
}

func TestDreamCannotArchiveEditedMemoryOrSynthesizeForgottenSource(t *testing.T) {
	a := newRejectTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	writeUserControlMemory(t, dir, "edited", "Writing preference", "activity", "Use elaborate prose.")
	before, err := a.GetMemoryEntry("edited")
	if err != nil {
		t.Fatal(err)
	}
	writeUserControlMemory(t, dir, "edited", "Writing preference", "user_edit", "Use concise prose.")
	if _, err := a.archiveMemoryFile(before, "in-flight-dream", ArchiveReasonExpired, ""); !errors.Is(err, errMemoryUserEdited) {
		t.Fatalf("stale Dream archived user correction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "edited.md")); err != nil {
		t.Fatal(err)
	}
	if err := recordMemoryTombstone(dir, "forgotten", []string{"forgotten source"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err = NewMemoryDreamService(a).writeSynthesizedMemory("Derived", "A paraphrase", "user_insight", []string{"forgotten"})
	if !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("late Dream synthesis accepted forgotten source: %v", err)
	}
}

func TestMemoryInjectionFailsClosedWhenTombstoneCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	writeUserControlMemory(t, dir, "memory", "Preference", "activity", "A fact.")
	if err := os.WriteFile(filepath.Join(dir, memoryTombstonesFile), []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := applyMemoryUserControls(dir, []MemorySearchResult{{Path: "memory.md", Source: "memory", Score: 1, Snippet: "A fact."}})
	if err == nil {
		t.Fatal("injection continued without usable forget controls")
	}
}
