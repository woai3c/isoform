package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPReadRestoredForgottenMemoryStaysSuppressed(t *testing.T) {
	for _, customStore := range []bool{false, true} {
		name := "default_directory"
		if customStore {
			name = "active_store_directory"
		}
		t.Run(name, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			forgotten, err := a.saveMemoryEntry("forgotten", buildOKFMemoryMarkdown("forgotten", "Retention preference", time.Now(), memorySourceConversation, "user_insight", []string{"Retention of orchard deployment secrets is restricted."}), false)
			if err != nil {
				t.Fatal(err)
			}
			control, err := a.SaveMemoryEntry("control", "# Retention control\n\n## Memory Points\n\n- Retention of public release notes is allowed.\n")
			if err != nil {
				t.Fatal(err)
			}
			userEdited, err := a.SaveMemoryEntry("forgotten-user-edit", "# Retention correction\n\n- Retention of my explicitly authored private note is restricted.\n")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range []MemoryEntry{forgotten, userEdited} {
				preview, err := a.GetMemoryForgetPreview(entry.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.ForgetMemoryEntry(entry.ID, preview.ConfirmationToken); err != nil {
					t.Fatal(err)
				}
			}
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a backup/sync restoring both the original filename and
			// its observed content under a new ID, bypassing every writer.
			// Explicit edits have an independent content-match exemption, but
			// restoring the same forgotten GUI record is still forbidden.
			for _, id := range []string{forgotten.ID, "restored-alias"} {
				if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(forgotten.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, userEdited.ID+".md"), []byte(userEdited.Content), 0600); err != nil {
				t.Fatal(err)
			}
			derived := buildOKFMemoryMarkdown("derived", "Retention derivative", time.Now(), "manual", "custom", []string{"Retention of a rewritten derived detail."})
			doc, ok := parseOKFDocument(derived)
			if !ok {
				t.Fatal("expected canonical memory")
			}
			doc.Frontmatter["merged_from"] = []string{forgotten.ID}
			writeMCPReadMarkdown(t, "derived", renderOKFDocument(doc.Frontmatter, doc.Body))
			if customStore {
				store, err := NewMemoryStore(t.TempDir(), MemoryStoreConfig{Provider: "none"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { store.Close() })
				for _, file := range []string{"forgotten.md", "forgotten-user-edit.md", "restored-alias.md", "derived.md", "control.md", memoryTombstonesFile} {
					data, err := os.ReadFile(filepath.Join(dir, file))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(store.baseDir, file), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				// Only the active store now carries suppression. A check against
				// the default directory would incorrectly allow these snapshots.
				if err := os.Remove(filepath.Join(dir, memoryTombstonesFile)); err != nil {
					t.Fatal(err)
				}
				a.memoryStore = store
			}
			storeMCPBriefReferences(t, MobileBriefRef{SourceKind: "memory", SourceID: forgotten.ID}, MobileBriefRef{SourceKind: "memory", SourceID: userEdited.ID}, MobileBriefRef{SourceKind: "memory", SourceID: "restored-alias"}, MobileBriefRef{SourceKind: "memory", SourceID: "derived"}, MobileBriefRef{SourceKind: "memory", SourceID: control.ID})
			assertMCPMemoryReadSurfaces(t, a, control.ID)
		})
	}
}

func TestMCPReadMemorySuppressionFailureClosesRawReads(t *testing.T) {
	a := newMCPReadTestApp(t)
	entry, err := a.SaveMemoryEntry("control", "# Retention control\n\n- Retention of public release notes is allowed.\n")
	if err != nil {
		t.Fatal(err)
	}
	storeMCPBriefReferences(t, MobileBriefRef{SourceKind: "memory", SourceID: entry.ID})
	assertMCPMemoryReadSurfaces(t, a, entry.ID)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, memoryTombstonesFile), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	assertMCPMemoryReadSurfaces(t, a, "")
}

func assertMCPMemoryReadSurfaces(t *testing.T, a *App, allowedID string) {
	t.Helper()
	for _, tool := range []string{"memory_list", "memory_search", "brief_today"} {
		t.Run(tool, func(t *testing.T) {
			args := map[string]any{}
			if tool == "memory_search" {
				args["query"] = "retention"
			}
			got := callMCPReadTest(t, a, tool, args)
			if allowedID == "" {
				if len(got.Items) != 0 {
					t.Fatalf("unverifiable suppression exposed memory: %+v", got)
				}
			} else if len(got.Items) != 1 || got.Items[0].ID.Text != allowedID {
				t.Fatalf("forgotten memory escaped or unrelated memory disappeared: %+v", got)
			}
		})
	}
	t.Run("generation_dedup", func(t *testing.T) {
		text, _ := loadMemoryGenerationContextForQuery(a.ctx, a, "retention", 50_000)
		if allowedID == "" {
			if text != "" {
				t.Fatalf("unverifiable suppression entered dedup: %q", text)
			}
		} else if !strings.Contains(text, "[id="+allowedID+"]") || strings.Contains(text, "[id=forgotten]") || strings.Contains(text, "[id=forgotten-user-edit]") || strings.Contains(text, "[id=restored-alias]") || strings.Contains(text, "[id=derived]") {
			t.Fatalf("forgotten memory entered dedup or unrelated memory disappeared: %q", text)
		}
	})
}
