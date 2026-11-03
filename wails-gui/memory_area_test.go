package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/topicid"
)

func TestMemoryAreaClassifier(t *testing.T) {
	for _, tc := range []struct{ belief, kind, want string }{
		{"I plan project deadlines before starting work.", "user_insight", "work"},
		{"I review plans with my colleagues.", "topic_highlight", "people"},
		{"I prefer bun over npm.", "user_insight", "tools"},
		{"I review my notes every morning.", "user_insight", "routines"},
		{"I enjoy photography.", "user_insight", "interests"},
		{"I prefer concise email replies.", "deep_user_model", "communication"},
		{"I follow these workflow steps.", "agentic_memory", "routines"},
		{"I follow these workflow steps.", "custom", "other"},
		{"Something unfamiliar.", "topic_highlight", "other"},
		{"I use a terminal for hospital work and my diabetes medication routine.", "agentic_memory", "other"},
		{"My therapist recommends a morning routine for anxiety.", "user_insight", "other"},
		{"I exercise every morning to manage my blood pressure.", "user_insight", "other"},
		{"I write about my religion in emails.", "user_insight", "other"},
		{"My salary goes into my bank account weekly.", "user_insight", "other"},
		{"I discuss my sexual orientation with friends.", "user_insight", "other"},
		{"我喜欢摄影。", "user_insight", "interests"},
		{"我每天记录健康状况。", "user_insight", "other"},
		{"I like terminals.", "custom", "other"}, // Unknown words never borrow source metadata.
	} {
		t.Run(tc.belief, func(t *testing.T) {
			if got := classifyMemoryArea(tc.belief, tc.kind); got != tc.want {
				t.Fatalf("area = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMemoryAreaTopicRoundTripAndAboutYou(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	for _, area := range []string{"work", "people", "tools", "routines", "interests", "communication", "other"} {
		content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Belief", "area": area, "topic_ids": []string{"topic-1", "topic:two"}, "dimension": "user_insight"}, "I prefer a quiet space.")
		saved, err := app.SaveMemoryEntry(area, content)
		if err != nil {
			t.Fatal(err)
		}
		got, err := app.GetMemoryEntry(area)
		if err != nil || got.Area != area || saved.Area != area || !reflect.DeepEqual(got.TopicIDs, []string{"topic-1", "topic:two"}) || got.Dimension != "user_insight" {
			t.Fatalf("round-trip: %+v %v", got, err)
		}
		fm := parseMemoryFrontmatter(got.Content)
		if fm.Area != area || !reflect.DeepEqual(fm.TopicIDs, got.TopicIDs) {
			t.Fatalf("frontmatter lost metadata: %+v", fm)
		}
	}
	entries, err := app.GetMemoryEntries()
	if err != nil || len(entries) != 7 {
		t.Fatalf("entries: %+v %v", entries, err)
	}
	view, err := app.GetAboutYou()
	if err != nil || len(view.Beliefs) != 7 {
		t.Fatalf("about you: %+v %v", view, err)
	}
	for _, belief := range view.Beliefs {
		if belief.Area != belief.ID || !reflect.DeepEqual(belief.TopicIDs, []string{"topic-1", "topic:two"}) {
			t.Fatalf("about you lost fields: %+v", belief)
		}
	}
}

func TestMemoryAreaLegacyBackfillStableWithoutWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{
		"# Package manager\n\n- **Dimension**: user_insight\n- **Apps**: Hospital\n\n## Memory Points\n\n- I prefer bun over npm.\n",
		"---\ntype: Isoform Memory\ntitle: Package manager\ndimension: user_insight\napps: [Hospital]\n---\n\nI prefer bun over npm.\n",
	} {
		path := filepath.Join(dir, "legacy.md")
		if err := os.WriteFile(path, []byte(content), 0400); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fm := parseMemoryFrontmatter(content); fm.Area != "" {
			t.Fatalf("legacy frontmatter should remain empty: %+v", fm)
		}
		for i := 0; i < 3; i++ {
			app := &App{} // A fresh reader has the same deterministic projection.
			got, err := app.GetMemoryEntry("legacy")
			if err != nil || got.Area != "tools" || got.Content != content {
				t.Fatalf("legacy projection: %+v %v", got, err)
			}
			entries, err := app.GetMemoryEntries()
			if err != nil || len(entries) != 1 || entries[0].Area != got.Area {
				t.Fatalf("list projection differs: %+v %v", entries, err)
			}
			view, err := app.GetAboutYou()
			if err != nil || len(view.Beliefs) != 1 || view.Beliefs[0].Area != got.Area {
				t.Fatalf("about you projection differs: %+v %v", view, err)
			}
		}
		data, err := os.ReadFile(path)
		after, statErr := os.Stat(path)
		files, listErr := os.ReadDir(dir)
		if err != nil || statErr != nil || listErr != nil || string(data) != content || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() || len(files) != 1 {
			t.Fatalf("backfill wrote files: read=%v stat=%v list=%v files=%v", err, statErr, listErr, files)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMemoryAreaCreationAndTopicPreservation(t *testing.T) {
	// A bold belief is substantive text, never source metadata to skip.
	bold := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "My tools"}, "- **Health**: I take insulin every morning.")
	if got := memoryAreaOnRead(bold, parseMemoryFrontmatter(bold)); got != "other" {
		t.Fatalf("classifier discarded a sensitive bold fact: %s", got)
	}
	content := buildOKFMemoryMarkdown("new", "My tools", time.Now(), "manual", "user_insight", []string{"I prefer bun over npm."})
	if fm := parseMemoryFrontmatter(content); fm.Area != "tools" {
		t.Fatalf("generator did not persist area: %+v", fm)
	}
	previous := renderOKFDocument(map[string]any{"type": "Isoform Memory", "area": "work", "topic_ids": []string{"assigned"}}, "Old belief.")
	got := preserveMemoryTopics(content, previous)
	if fm := parseMemoryFrontmatter(got); fm.Area != "tools" || !reflect.DeepEqual(fm.TopicIDs, []string{"assigned"}) {
		t.Fatalf("machine rewrite lost metadata: %+v", fm)
	}
	legacy := "# Old\n- **Area**: tools\n- **Topic IDs**: [\"assigned\"]\n\nI use bun.\n"
	converted := memoryWithResolvedProvenance("old", legacy, memoryReadSource("manual"))
	if fm := parseMemoryFrontmatter(converted); fm.Area != "tools" || !reflect.DeepEqual(fm.TopicIDs, []string{"assigned"}) {
		t.Fatalf("legacy conversion lost metadata: %+v", fm)
	}
	edit := userAuthoredMemoryMarkdown("old", "# Changed\n\nI use pnpm.", converted)
	if fm := parseMemoryFrontmatter(edit); fm.Area != "tools" || !reflect.DeepEqual(fm.TopicIDs, []string{"assigned"}) {
		t.Fatalf("body edit lost metadata: %+v", fm)
	}
}

func TestMemoryMetadataValidationAndTopicIDParity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, fields := range []string{"area: health", "area: [work]", "topic_ids: [1]", "topic_ids: wrong", "topic_ids: ['']", "topic_ids: ['   ']"} {
		if _, err := (&App{}).SaveMemoryEntry("invalid", "---\ntype: Isoform Memory\n"+fields+"\n---\nI use bun."); err == nil {
			t.Fatalf("accepted invalid metadata: %s", fields)
		}
	}
	for _, tc := range []struct {
		id    string
		valid bool
	}{{"", false}, {" \t", false}, {"topic-1", true}, {"opaque/id", true}, {strings.Repeat("x", 2048), true}, {strings.Repeat("x", 2049), false}} {
		if validChatTopicID(tc.id) != tc.valid || topicid.Valid(tc.id) != tc.valid {
			t.Fatalf("topic form diverged from registry contract: %q", tc.id)
		}
	}
}

func TestMemoryAreaActivityCreationAndRetryPreserveTopics(t *testing.T) {
	app := newMCPReadTestApp(t)
	at := seedMCPReadActivity(t, app, "area-session", "Editor", "com.example.editor", "ordinary", "", "")
	session := ActivitySessionRow{ID: "area-session", StartTime: at, AppNames: `["Editor"]`}
	group := sessionGroup{Sessions: []ActivitySessionRow{session}}
	triager := &ActivityTriager{app: app}
	response := &mcMemoryGenerationResponse{Title: "Package manager", MemoryPoints: []string{"I prefer bun over npm."}, Dimension: "user_insight"}
	id, err := triager.saveActivityMemory(session, group, response)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.GetMemoryEntry(id)
	if err != nil || parseMemoryFrontmatter(entry.Content).Area != "tools" {
		t.Fatalf("activity creation did not persist area: %+v %v", entry, err)
	}
	doc, _ := parseOKFDocument(entry.Content)
	doc.Frontmatter["topic_ids"] = []string{"assigned"}
	dir, _ := memoryDir()
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(renderOKFDocument(doc.Frontmatter, doc.Body)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := triager.saveActivityMemory(session, group, response); err != nil {
		t.Fatal(err)
	}
	entry, err = app.GetMemoryEntry(id)
	if err != nil || entry.Area != "tools" || !reflect.DeepEqual(entry.TopicIDs, []string{"assigned"}) {
		t.Fatalf("activity retry lost metadata: %+v %v", entry, err)
	}
}

func TestMemoryAreaDreamCreation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &MemoryDreamService{app: &App{}}
	id, err := s.writeSynthesizedMemoryWithProvenance("My tools", "I prefer bun over npm.", "user_insight", nil, []memoryReadProvenance{memoryReadSource("manual")})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.app.GetMemoryEntry(id)
	if err != nil || parseMemoryFrontmatter(entry.Content).Area != "tools" {
		t.Fatalf("dream did not persist area: %+v %v", entry, err)
	}
}
