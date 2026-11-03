package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryKnowHowSearchRedactionReadModels(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	title := "Reclaim[REDACTED:account_number]w outdated Command Line Tools error"
	want := "Reclaim[...]w outdated Command Line Tools error"
	content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": title, "source": "manual"}, "## Memory Points\n\n- Use [REDACTED:api_key] safely.")
	dir, _ := memoryDir()
	if err := os.WriteFile(filepath.Join(dir, "belief.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	knowledge := renderOKFDocument(map[string]any{"type": "Isoform Knowledge", "title": title, "description": "Use [REDACTED:api_key] safely."}, "## Key Insights\n\n- Keep [REDACTED:private_email] hidden.")
	writeKnowHowFixture(t, "fix", knowledge)
	memory, err := app.GetMemoryEntry("belief")
	if err != nil || memory.Title != want || memory.Content != content {
		t.Fatalf("memory=%+v %v", memory, err)
	}
	memories, err := app.GetMemoryEntries()
	if err != nil || len(memories) != 1 || memories[0].Title != want {
		t.Fatalf("memories=%+v %v", memories, err)
	}
	view, err := app.GetAboutYou()
	if err != nil || len(view.Beliefs) != 1 || view.Beliefs[0].Title != want {
		t.Fatalf("beliefs=%+v %v", view.Beliefs, err)
	}
	entry, err := app.GetKnowledgeEntry("fix")
	if err != nil || entry.Title != want || entry.Content != knowledge {
		t.Fatalf("knowledge=%+v %v", entry, err)
	}
	cards, err := app.ListKnowHow()
	if err != nil || len(cards) != 1 || cards[0].Title != want || cards[0].Takeaway != "Use [...] safely." || len(cards[0].Points) != 1 || cards[0].Points[0] != "Keep [...] hidden." {
		t.Fatalf("cards=%+v %v", cards, err)
	}
	for _, place := range []string{"about_you", "know_how"} {
		results := searchTestResults(t, app, "Reclaim", place, 20)
		if len(results) != 1 || results[0].Title != want || strings.Contains(results[0].Snippet, "[REDACTED:") {
			t.Fatalf("%s: %+v", place, results)
		}
	}
	// Both pages also render Markdown summaries from serialized Content.
	for _, value := range []any{memory, entry} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), "[REDACTED:") || !strings.Contains(string(raw), "[...]") {
			t.Fatalf("wire display: %s %v", raw, err)
		}
	}
	// Display formatting does not rewrite stored documents or their masks.
	raw, err := os.ReadFile(filepath.Join(dir, "belief.md"))
	if err != nil || string(raw) != content {
		t.Fatalf("read mutated document: %v", err)
	}
}

func TestAboutYouPortraitStoredRedactionMarker(t *testing.T) {
	app, service, _, path := newPortraitTestRig(t, 20)
	service.check(context.Background(), time.Now())
	doc, err := readPortrait(path)
	if err != nil || doc == nil || len(doc.Sentences) == 0 {
		t.Fatalf("portrait setup %v", err)
	}
	doc.Sentences[0].Parts[0].Text = "You prefer [REDACTED:private_email] in clear plans. "
	if err := writePortrait(path, doc); err != nil {
		t.Fatal(err)
	}
	got := mustPortraitRead(t, app, "ready")
	if got.Sentences[0].Parts[0].Text != "You prefer [...] in clear plans. " {
		t.Fatalf("part=%+v", got.Sentences[0].Parts[0])
	}
	stored, err := readPortrait(path)
	if err != nil || !strings.Contains(stored.Sentences[0].Parts[0].Text, "[REDACTED:") {
		t.Fatalf("stored mask rewritten: %+v %v", stored, err)
	}
}

func TestTodayChatMemoryEvidenceProjectionRetainsFullReadGate(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	for _, id := range []string{"support", "comparison"} {
		if err := app.activity.store.SaveSession(ActivitySessionRow{ID: id, AppNames: `["Code"]`, StartTime: 100, EndTime: 200}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	content := renderOKFDocument(map[string]any{"title": "Atlas preference", "source": "activity", "activity_sessions": []string{"support", "comparison"}, "read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"support", "comparison"}, PromptSafe: true})}, "## Memory Points\n\n- Atlas safe legacy preference.")
	writeTodayContextMemory(t, "belief", content)
	entry, err := app.GetMemoryEntry("belief")
	if err != nil || len(entry.ActivitySessionIDs) != 0 {
		t.Fatalf("ambiguous support=%v %v", entry.ActivitySessionIDs, err)
	}
	sources := app.collectTodayChatSources(context.Background(), time.Now())
	if len(sources.Memories) != 1 {
		t.Fatalf("legitimate legacy memory dropped: %+v", sources.Memories)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='comparison'`); err != nil {
		t.Fatal(err)
	}
	sources = app.collectTodayChatSources(context.Background(), time.Now())
	if len(sources.Memories) != 0 {
		t.Fatalf("comparison privacy dependency lost: %+v", sources.Memories)
	}
}
