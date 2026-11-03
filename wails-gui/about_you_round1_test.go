package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestAboutYouRound1ConfirmationSurvivesRewrites(t *testing.T) {
	for _, format := range []string{"legacy", "okf"} {
		for _, path := range []string{"wrong-subject", "dream-undo"} {
			t.Run(format+"/"+path, func(t *testing.T) {
				app := newReassignTestApp(t)
				original := "# My preference\n\n- **Source**: activity\n- **Dimension**: user_insight\n\n## Memory Points\n\n- I like concise reviews.\n"
				if format == "okf" {
					original = buildOKFMemoryMarkdown("belief", "My preference", time.Now(), "activity", "user_insight", []string{"I like concise reviews."})
				}
				confirmed, err := setMemoryConfirmedAt(original, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
				if err != nil {
					t.Fatal(err)
				}
				dir, _ := memoryDir()
				if err := os.WriteFile(filepath.Join(dir, "belief.md"), []byte(confirmed), 0600); err != nil {
					t.Fatal(err)
				}
				switch path {
				case "wrong-subject":
					target, err := app.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Alex"})
					if err != nil {
						t.Fatal(err)
					}
					if err := app.ReassignMemoryAttribution("belief", target.ID, "Wrong subject"); err != nil {
						t.Fatal(err)
					}
				case "dream-undo":
					entry, err := app.GetMemoryEntry("belief")
					if err != nil {
						t.Fatal(err)
					}
					archiveID, err := app.archiveMemoryFile(entry, "test-run", ArchiveReasonManual, "")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := app.restoreArchivedMemory(archiveID); err != nil {
						t.Fatal(err)
					}
				}
				got, err := app.GetMemoryEntry("belief")
				if err != nil {
					t.Fatal(err)
				}
				if memoryConfirmedAt(got.Content) == 0 {
					t.Error("confirmation lost")
				}
				if path == "dream-undo" && memoryConfirmedAt(got.Content) != memoryConfirmedAt(confirmed) {
					t.Error("Undo changed confirmation time")
				}
				doc, ok := parseOKFDocument(got.Content)
				if !ok || okfString(doc.Frontmatter, "type") != "Isoform Memory" || strings.Contains(doc.Body, "confirmed_at:") || strings.Contains(doc.Body, "\n---\n") {
					t.Errorf("frontmatter leaked into body: %q", doc.Body)
				}
				if !strings.Contains(doc.Body, "concise reviews") || parseMemoryFrontmatter(got.Content).ReadProvenance == "" {
					t.Errorf("rewrite lost text or provenance: %q", got.Content)
				}
			})
		}
	}
}

func TestContextUseRound1DeleteMemoryAndRecreate(t *testing.T) {
	for _, kind := range []string{"memory", "knowledge"} {
		t.Run(kind, func(t *testing.T) {
			app := newReassignTestApp(t)
			dir, _ := memoryDir()
			if kind == "knowledge" {
				dir, _ = knowledgeDir()
			}
			path := filepath.Join(dir, "reused.md")
			if err := os.WriteFile(path, []byte("# Original\n\nOriginal belief."), 0600); err != nil {
				t.Fatal(err)
			}
			epoch := app.contextUseEpoch()
			app.recordContextUses(kind, []string{"reused"}, "chat", "same-session")
			awaitEntryUses(t, app, kind, "reused", 1)
			var err error
			switch kind {
			case "memory":
				err = app.DeleteMemoryEntry("reused")
			case "knowledge":
				err = app.DeleteKnowledgeEntry("reused")
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := app.store.EntryUses(t.Context(), kind, "reused")
			if err != nil || got.Total != 0 {
				t.Fatalf("deleted entry retained uses: %+v %v", got, err)
			}
			if err := os.WriteFile(path, []byte("# New\n\nNew entry with the same deterministic ID."), 0600); err != nil {
				t.Fatal(err)
			}
			app.recordContextUses(kind, []string{"reused"}, "chat", "same-session")
			awaitEntryUses(t, app, kind, "reused", 1)
			app.recordContextEntries([]contextUseEntry{{kind, "reused", epoch}, {kind, "control", epoch}}, "assistant_tool", "old-read")
			awaitEntryUses(t, app, kind, "control", 1)
			got, err = app.store.EntryUses(t.Context(), kind, "reused")
			if err != nil || got.Total != 1 || len(got.Surfaces) != 1 || got.Surfaces[0].Surface != "chat" {
				t.Fatalf("old read affected new entry: %+v %v", got, err)
			}
		})
	}
}

func TestAboutYouRound1ProvenanceConversionKeepsConfirmation(t *testing.T) {
	for _, original := range []string{
		"# Legacy\n\n- **Source**: activity\n\nOld belief.",
		buildOKFMemoryMarkdown("belief", "Original", time.Now(), "activity", "user_insight", []string{"Old belief."}),
	} {
		confirmed, err := setMemoryConfirmedAt(original, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		resolved := memoryWithResolvedProvenance("belief", confirmed, memoryReadSource("activity"))
		if memoryConfirmedAt(resolved) != memoryConfirmedAt(confirmed) {
			t.Fatal("provenance conversion dropped confirmation")
		}
		doc, ok := parseOKFDocument(resolved)
		if !ok || strings.Contains(doc.Body, "confirmed_at:") {
			t.Fatal("provenance conversion leaked frontmatter")
		}
	}
}

func TestAboutYouRound1ActivityWritersKeepConfirmation(t *testing.T) {
	app := newMCPReadTestApp(t)
	at := seedMCPReadActivity(t, app, "confirmation-session", "Editor", "com.example.editor", "ordinary", "", "")
	session := ActivitySessionRow{ID: "confirmation-session", StartTime: at, AppNames: `["Editor"]`}
	group := sessionGroup{Sessions: []ActivitySessionRow{session}}
	triager := &ActivityTriager{app: app}
	response := &mcMemoryGenerationResponse{Title: "Review preference", MemoryPoints: []string{"Concise reviews."}, Dimension: "user_insight"}
	id, err := triager.saveActivityMemory(session, group, response)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := app.ConfirmMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"retry", "update", "save"} {
		switch path {
		case "retry":
			_, err = triager.saveActivityMemory(session, group, response)
		case "update":
			_, err = triager.updateActivityMemory(t.Context(), id, session, group, response)
		case "save":
			entry, readErr := app.GetMemoryEntry(id)
			if readErr != nil {
				t.Fatal(readErr)
			}
			_, err = app.saveMemoryEntry(id, entry.Content, false)
		}
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		got, err := app.GetMemoryEntry(id)
		if err != nil || memoryConfirmedAt(got.Content) != memoryConfirmedAt(confirmed.Content) {
			t.Fatalf("%s lost confirmation: %v", path, err)
		}
		doc, ok := parseOKFDocument(got.Content)
		if !ok || strings.Contains(doc.Body, "confirmed_at:") {
			t.Fatalf("%s leaked frontmatter", path)
		}
	}
}
