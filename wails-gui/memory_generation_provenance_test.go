package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPReadGenerationDedupDoesNotWithholdIndependentMemories(t *testing.T) {
	for _, kind := range []string{"connector", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			mc := NewMemoryGenerator(a)
			switch kind {
			case "connector":
				id, err := mc.saveMemoryFile(memorySourceTelegram, "private-thread", &mcMemoryGenerationResponse{Title: "Private comparison", Dimension: "user_insight", MemoryPoints: []string{"private comparison marker"}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.SaveMemoryEntry(id, "# Private comparison\n\n## Memory Points\n\n- private comparison marker"); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				writeMCPReadMarkdown(t, "legacy", "# Legacy comparison\n\n## Memory Points\n\n- private comparison marker")
			}
			comparison, provenance := loadMemoryGenerationContext(a.ctx, a)
			if strings.Contains(comparison, "private comparison marker") || strings.Contains(comparison, "Private comparison") || strings.Contains(comparison, "Legacy comparison") {
				t.Fatalf("private comparison entered the generation prompt: %q", comparison)
			}
			// The model's independent result is supplied at the real writer boundary;
			// the comparison entry is neither its source nor its update target.
			response := &mcMemoryGenerationResponse{Title: "Independent result", MemoryPoints: []string{"independent native marker"}, Dimension: "user_insight", inputProvenance: provenance}
			if _, err := mc.saveMemoryFile(memorySourceConversation, "native-create", response); err != nil {
				t.Fatal(err)
			}
			writeMCPReadMarkdown(t, "native-update", buildOKFMemoryMarkdown("native-update", "Safe before update", time.Now(), memorySourceConversation, "user_insight", []string{"old safe point"}))
			if err := mc.updateMemoryFile(a.ctx, "native-update", memorySourceConversation, "native-thread", response, ""); err != nil {
				t.Fatal(err)
			}
			at := seedMCPReadActivity(t, a, "safe-observation", "Editor", "com.example.editor", "ordinary", "", "")
			session := ActivitySessionRow{ID: "safe-observation", StartTime: at, AppNames: `["Editor"]`}
			group := sessionGroup{Sessions: []ActivitySessionRow{session}}
			triager := &ActivityTriager{app: a}
			if _, err := triager.saveActivityMemory(session, group, response); err != nil {
				t.Fatal(err)
			}
			writeMCPReadMemory(t, "activity-update", session.ID, "old safe point")
			if _, err := triager.updateActivityMemory(a.ctx, "activity-update", session, group, response); err != nil {
				t.Fatal(err)
			}
			for _, tool := range []string{"memory_search", "memory_list"} {
				args := map[string]any{}
				if tool == "memory_search" {
					args["query"] = "independent native marker"
				}
				got := callMCPReadTest(t, a, tool, args)
				if len(got.Items) != 4 {
					t.Errorf("%s comparison withheld independent outputs: %+v", tool, got)
				}
			}
			if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "private comparison marker"}); len(got.Items) != 0 {
				t.Fatalf("comparison source itself became shareable: %+v", got)
			}
		})
	}
}

func TestMCPReadGenerationUpdatesRetainAllRenderedSourceGates(t *testing.T) {
	a := newMCPReadTestApp(t)
	seedMCPReadActivity(t, a, "source-observation", "Editor", "com.example.editor", "ordinary", "", "")
	writeMCPReadMemory(t, "source-memory", "source-observation", "observed point")
	// This is an observed update target; manual/user-edited records correctly
	// reject automatic rewrites under the memory editor's authority policy.
	content := buildOKFMemoryMarkdown("app-source", "Source label", time.Now(), memorySourceConversation, "user_insight", []string{"app dependency marker"})
	doc, _ := parseOKFDocument(content)
	doc.Frontmatter["apps"] = []string{"Project Editor"}
	writeMCPReadMarkdown(t, "app-source", renderOKFDocument(doc.Frontmatter, doc.Body))
	_, comparisonProvenance := loadMemoryGenerationContext(a.ctx, a)
	mc := NewMemoryGenerator(a)
	for _, id := range []string{"source-memory", "app-source"} {
		response := &mcMemoryGenerationResponse{Title: "Updated result", Dimension: "user_insight", MemoryPoints: []string{"selected update marker"}, inputProvenance: comparisonProvenance}
		if err := mc.updateMemoryFile(a.ctx, id, memorySourceConversation, "native-thread", response, ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "selected update marker"}); len(got.Items) != 2 {
		t.Fatalf("readable updates withheld: %+v", got)
	}
	if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.project":"Project Editor"}`); err != nil {
		t.Fatal(err)
	}
	if got := callMCPReadTest(t, a, "memory_list", map[string]any{}); len(got.Items) != 0 {
		t.Fatalf("rendered comparison source lost its dynamic privacy gate: %+v", got)
	}
	if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.project":"Project Editor","com.example.editor":"Editor"}`); err != nil {
		t.Fatal(err)
	}
	if got := callMCPReadTest(t, a, "memory_list", map[string]any{}); len(got.Items) != 0 {
		t.Fatalf("selected source dependency lost: %+v", got)
	}
}

func TestMCPReadGenerationUpdatesKeepWithheldTargetPrivate(t *testing.T) {
	a := newMCPReadTestApp(t)
	mc := NewMemoryGenerator(a)
	at := seedMCPReadActivity(t, a, "safe-update", "Editor", "com.example.editor", "ordinary", "", "")
	session := ActivitySessionRow{ID: "safe-update", StartTime: at, AppNames: `["Editor"]`}
	group := sessionGroup{Sessions: []ActivitySessionRow{session}}
	triager := &ActivityTriager{app: a}
	for _, source := range []string{"chat", "activity"} {
		id, err := mc.saveMemoryFile(memorySourceTelegram, source, &mcMemoryGenerationResponse{Title: "Private source", Dimension: "user_insight", MemoryPoints: []string{"private target point"}})
		if err != nil {
			t.Fatal(err)
		}
		response := &mcMemoryGenerationResponse{Title: "Update", Dimension: "user_insight", MemoryPoints: []string{"selected source marker"}}
		switch source {
		case "chat":
			err = mc.updateMemoryFile(a.ctx, id, memorySourceConversation, "native-thread", response, "")
		case "activity":
			_, err = triager.updateActivityMemory(a.ctx, id, session, group, response)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "selected source marker"}); len(got.Items) != 0 {
		t.Fatalf("selected private target lost lineage: %+v", got)
	}
}

func TestMCPReadGenerationFallbackKeepsUnverifiedTargetPrivate(t *testing.T) {
	for _, writer := range []string{"chat", "activity"} {
		for _, target := range []string{"removed", "archived", "invalid", "unreadable"} {
			t.Run(writer+"/"+target, func(t *testing.T) {
				a := newMCPReadTestApp(t)
				mc := NewMemoryGenerator(a)
				id, err := mc.saveMemoryFile(memorySourceTelegram, "private-thread", &mcMemoryGenerationResponse{Title: "Private target", Dimension: "user_insight", MemoryPoints: []string{"private fallback marker"}})
				if err != nil {
					t.Fatal(err)
				}
				// Keep the target machine-authored so the archive path exercises
				// unavailable-source fallback rather than user-edit protection.
				entry, err := a.GetMemoryEntry(id)
				if err != nil {
					t.Fatal(err)
				}
				comparison, provenance := loadMemoryGenerationContext(a.ctx, a)
				if strings.Contains(comparison, "private fallback marker") {
					t.Fatal("private target entered generation context")
				}
				// A model can still return an invented or inadmissible target ID.
				// The writer must retain privacy when that target is unavailable.
				response := &mcMemoryGenerationResponse{Action: "update", UpdateID: id, Title: "Merged target", Dimension: "user_insight", MemoryPoints: []string{"private fallback marker"}, inputProvenance: provenance}
				switch target {
				case "removed":
					if err := a.DeleteMemoryEntry(id); err != nil {
						t.Fatal(err)
					}
				case "archived":
					archiveID, err := a.archiveMemoryFile(entry, "test-generation", ArchiveReasonManual, "")
					if err != nil || archiveID == "" {
						t.Fatalf("archive selected target: %q, %v", archiveID, err)
					}
				case "invalid":
					response.UpdateID = "../" + id
				case "unreadable":
					if err := a.DeleteMemoryEntry(id); err != nil {
						t.Fatal(err)
					}
					// A real directory at the file path reliably makes ReadFile
					// fail without depending on the test process's privileges.
					if err := os.Mkdir(filepath.Join(configDirOrFallback(), "memory", id+".md"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				at := seedMCPReadActivity(t, a, "fallback-source", "Editor", "com.example.editor", "ordinary", "", "")
				session := ActivitySessionRow{ID: "fallback-source", StartTime: at, AppNames: `["Editor"]`}
				createdID := activityMemoryEntryID(session.ID)
				var action string
				switch writer {
				case "chat":
					createdID, action, err = mc.updateMemoryFileDetailed(a.ctx, response.UpdateID, memorySourceConversation, "native-source", response, "")
				case "activity":
					triager := &ActivityTriager{app: a}
					action, err = triager.updateActivityMemory(a.ctx, response.UpdateID, session, sessionGroup{Sessions: []ActivitySessionRow{session}}, response)
				}
				if response.inputProvenance != provenance {
					t.Fatal("fallback mutated the original generation response")
				}
				if writer == "activity" && target == "unreadable" {
					// Activity already returns an error for an unreadable live
					// target. Keep that behavior instead of inventing a fallback.
					if err == nil || action != "" {
						t.Fatalf("unreadable activity target changed behavior: %q, %v", action, err)
					}
					if _, statErr := os.Stat(filepath.Join(configDirOrFallback(), "memory", createdID+".md")); !os.IsNotExist(statErr) {
						t.Fatalf("failed activity update created a memory: %v", statErr)
					}
				} else {
					if err != nil || action != BackgroundMaterializationCreated {
						t.Fatalf("existing create fallback was lost: %q, %v", action, err)
					}
					created, err := a.GetMemoryEntry(createdID)
					if err != nil || !strings.Contains(created.Content, "private fallback marker") || !memoryReadProvenanceFromContent(created.Content).Withheld {
						t.Fatalf("fallback was not persisted with withheld provenance: %+v, %v", created, err)
					}
				}
				for _, tool := range []string{"memory_search", "memory_list"} {
					args := map[string]any{}
					if tool == "memory_search" {
						args["query"] = "private fallback marker"
					}
					if got := callMCPReadTest(t, a, tool, args); len(got.Items) != 0 {
						t.Fatalf("%s exported unavailable selected target: %+v", tool, got)
					}
				}
			})
		}
	}
}

// A code-owned blocked dependency remains blocked after the MCP generation
// metadata is composed with the shared read-time privacy envelope.
func TestMemoryGenerationReadBlockedLineageSurvivesMerge(t *testing.T) {
	a := newMCPReadTestApp(t)
	blocked := memoryReadProvenance{Version: 2, Kinds: []string{"manual"}, ReadBlocked: true, PromptSafe: true}
	if memoryReadInputComplete(blocked) || memoryReadPromptSafe(blocked) {
		t.Fatal("blocked dependency was certified as a safe generation input")
	}
	merged := mergeMemoryReadProvenance(memoryReadSource("manual"), blocked)
	if !merged.ReadBlocked || !merged.Withheld || merged.PromptSafe {
		t.Fatalf("merge discarded read-time privacy restriction: %+v", merged)
	}
	writeMCPReadMarkdown(t, "blocked-comparison", withMemoryReadProvenance(buildOKFMemoryMarkdown("blocked-comparison", "Blocked comparison", time.Now(), "manual", "user_insight", []string{"blocked comparison marker"}), merged))
	writeMCPReadMarkdown(t, "readable-comparison", buildOKFMemoryMarkdown("readable-comparison", "Readable comparison", time.Now(), "manual", "user_insight", []string{"readable comparison marker"}))
	comparison, provenance := loadMemoryGenerationContext(a.ctx, a)
	if strings.Contains(comparison, "blocked-comparison") || !strings.Contains(comparison, "readable-comparison") || provenance == nil || provenance.ReadBlocked {
		t.Fatalf("shared blocked source entered generation context: %q, %+v", comparison, provenance)
	}
}
