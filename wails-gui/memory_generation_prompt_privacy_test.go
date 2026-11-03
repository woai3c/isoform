package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the same prompt-context and writer boundaries as generation, with
// deterministic supplied points. No model or background service is started.
func writePromptSafeActivityMemory(t *testing.T, a *App, sessionID, title, point string) string {
	t.Helper()
	at := seedMCPReadActivity(t, a, sessionID, "Editor", "com.example.editor", "ordinary", "", "")
	session := ActivitySessionRow{ID: sessionID, StartTime: at, AppNames: `["Editor"]`}
	response := &mcMemoryGenerationResponse{Title: title, Dimension: "user_insight", MemoryPoints: []string{point}}
	loadMemoryGenerationPromptContext(a.ctx, a).apply(response)
	id, err := (&ActivityTriager{app: a}).saveActivityMemory(session, sessionGroup{Sessions: []ActivitySessionRow{session}}, response)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMemoryGenerationContextFiltersCurrentPrivacyBeforeRendering(t *testing.T) {
	for _, gate := range []string{"protected", "sensitive", "private", "ignored", "closed_eyes", "unavailable"} {
		t.Run(gate, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			app, bundle, title, status := "Editor", "com.example.editor", "ordinary", ""
			switch gate {
			case "protected":
				status = "protected"
			case "private":
				app, bundle, title = "Safari", "com.apple.Safari", "Private Browsing"
			case "sensitive":
				if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
			case "ignored":
				if err := a.store.SetUIState(a.ctx, voiceInputAppsUIStateKey, `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
			case "closed_eyes":
				a.closedEyes.enabled.Store(true)
			case "unavailable":
				if err := a.store.SetUIState(a.ctx, "sensitive_apps", "invalid json"); err != nil {
					t.Fatal(err)
				}
			}
			seedMCPReadActivity(t, a, "hidden-observation", app, bundle, title, "", status)
			content := renderOKFDocument(map[string]any{
				"type": "Isoform Memory", "title": "Secret comparison title", "source": "activity",
				"dimension": "topic_highlight", "activity_sessions": []string{"hidden-observation"},
				"read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"hidden-observation"}, PromptSafe: true}),
			}, "# Secret comparison title\n\n## Memory Points\n\n- secret comparison point")
			writeMCPReadMarkdown(t, "secret-comparison-id", content)
			comparison, provenance := loadMemoryGenerationContext(a.ctx, a)
			if comparison != "" || provenance != nil {
				t.Fatalf("privacy-gated entry reached generation: %q, %+v", comparison, provenance)
			}
		})
	}
}

func TestMemoryGenerationContextRetainsCapturedSourceAfterReplacement(t *testing.T) {
	a := newMCPReadTestApp(t)
	sourceID := writePromptSafeActivityMemory(t, a, "original-observation", "Comparison", "captured comparison marker")
	inputs := loadMemoryGenerationPromptContext(a.ctx, a)
	comparison, provenance := inputs.existing, inputs.provenance
	if !strings.Contains(comparison, "captured comparison marker") {
		t.Fatal("readable comparison was not available")
	}
	// The writer must retain the bytes actually rendered, not reread the new
	// manual file and accidentally upgrade the old activity source's privacy.
	writeMCPReadMarkdown(t, sourceID, buildOKFMemoryMarkdown(sourceID, "Replacement", time.Now(), "manual", "user_insight", []string{"unrelated replacement"}))
	if provenance == nil || len(provenance.Sessions) != 1 || provenance.Sessions[0] != "original-observation" {
		t.Fatalf("captured source lineage was lost: %+v", provenance)
	}
	mc := NewMemoryGenerator(a)
	response := &mcMemoryGenerationResponse{Title: "Independent result", Dimension: "user_insight", MemoryPoints: []string{"derived comparison marker"}}
	inputs.apply(response)
	if _, err := mc.saveMemoryFile(memorySourceConversation, "native-thread", response); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
		t.Fatal(err)
	}
	if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "derived comparison marker"}); len(got.Items) != 0 {
		t.Fatalf("replacement detached the rendered input's privacy: %+v", got)
	}
}

func TestMemoryGenerationCorrectionsUseStructuralFeedbackFromRealLogs(t *testing.T) {
	a := newMCPReadTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONLines(t, filepath.Join(dir, memoryRejectionsLogName), []any{
		MemoryRejectionEntry{MemoryID: "private-memory-id", Title: "Private rejected source title", Reason: "Private rejection explanation", RejectedAt: 10},
	})
	writeJSONLines(t, filepath.Join(dir, memoryReassignmentsLogName), []any{
		MemoryReassignmentEntry{MemoryID: "private-reassigned-id", Title: "Private reassigned source title", Reason: "Private reassignment explanation", CorrectPrincipalName: "Private principal", ReassignedAt: 20},
	})
	corrections, err := LoadRecentCorrections(dir, 50)
	if err != nil || len(corrections) != 2 {
		t.Fatalf("read correction fixtures: %+v, %v", corrections, err)
	}
	block := FormatCorrectionsPromptBlock(corrections)
	if block == "" {
		t.Fatal("corrections no longer provide structural feedback")
	}
	for _, private := range []string{"Private", "private-memory-id", "private-reassigned-id"} {
		if strings.Contains(block, private) {
			t.Errorf("freeform correction data entered generation prompt: %q", block)
		}
	}
	if FindMatchingCorrection(corrections, "Private rejected source title", nil) == nil || FindMatchingCorrection(corrections, "Private reassigned source title", nil) == nil {
		t.Fatal("local exact correction veto lost its private evidence")
	}
	if FindMatchingCorrection(corrections, "The editor uses a compact toolbar", nil) != nil {
		t.Fatal("unrelated native source was rejected by permanent corrections")
	}
	inputs := loadMemoryGenerationPromptContext(a.ctx, a)
	if inputs.corrections != block {
		t.Fatal("generation bypassed structural correction formatting")
	}
	response := &mcMemoryGenerationResponse{Title: "Independent current source", Dimension: "user_insight", MemoryPoints: []string{"independent correction marker"}}
	inputs.apply(response)
	mc := NewMemoryGenerator(a)
	if _, err := mc.saveMemoryFile(memorySourceConversation, "native-after-corrections", response); err != nil {
		t.Fatal(err)
	}
	at := seedMCPReadActivity(t, a, "activity-after-corrections", "Editor", "com.example.editor", "ordinary", "", "")
	session := ActivitySessionRow{ID: "activity-after-corrections", StartTime: at, AppNames: `["Editor"]`}
	if _, err := (&ActivityTriager{app: a}).saveActivityMemory(session, sessionGroup{Sessions: []ActivitySessionRow{session}}, response); err != nil {
		t.Fatal(err)
	}
	if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "independent correction marker"}); len(got.Items) != 2 {
		t.Fatalf("permanent corrections withheld unrelated native generation: %+v", got)
	}
	comparison, provenance := loadMemoryGenerationContext(a.ctx, a)
	if !strings.Contains(comparison, "independent correction marker") || provenance == nil || !memoryReadPromptSafe(*provenance) {
		t.Fatalf("structural feedback poisoned future safe dedup: %q, %+v", comparison, provenance)
	}
}

func TestMemoryGenerationPreservesLegacyDirectSourceCompatibility(t *testing.T) {
	a := newMCPReadTestApp(t)
	writeMCPReadMarkdown(t, "old-native", buildOKFMemoryMarkdown("old-native", "Historical native title", time.Now(), memorySourceConversation, "user_insight", []string{"historical direct marker"}))
	at := seedMCPReadActivity(t, a, "old-observation", "Editor", "com.example.editor", "ordinary", "", "")
	writeMCPReadMemory(t, "old-activity", "old-observation", "# Historical activity title\n\n- historical direct marker")
	legacyID := activityMemoryEntryID("old-observation")
	legacy := fmt.Sprintf("# Legacy activity\n\n- **Updated**: %s\n- **Source**: activity\n- **Dimension**: user_insight\n- **Apps**: Editor\n- **Activity Session**: old-observation\n\n## Memory Points\n\n- legacy direct marker\n", time.UnixMilli(at).Format("2006-01-02 15:04"))
	writeMCPReadMarkdown(t, legacyID, legacy)
	mixed := memoryReadProvenance{Version: 2, Kinds: []string{"manual", "conversation", "activity", "learning"}, Sessions: []string{"old-observation"}}
	writeMCPReadMarkdown(t, "old-mixed", withMemoryReadProvenance(buildOKFMemoryMarkdown("old-mixed", "Mixed legacy sources", time.Now(), memorySourceConversation, "user_insight", []string{"mixed direct marker"}), mixed))
	for id, provenance := range map[string]memoryReadProvenance{
		"private-withheld":        {Version: 2, Kinds: []string{"conversation"}, Withheld: true},
		"private-connector":       {Version: 2, Kinds: []string{"conversation", "telegram"}},
		"private-v1-conversation": {Version: 1, Kinds: []string{"conversation"}},
		"private-unknown":         {Version: 2, Kinds: []string{"unknown"}},
	} {
		writeMCPReadMarkdown(t, id, withMemoryReadProvenance(buildOKFMemoryMarkdown(id, id, time.Now(), memorySourceConversation, "user_insight", []string{"private invalid marker"}), provenance))
	}
	comparison, provenance := loadMemoryGenerationContext(a.ctx, a)
	for _, want := range []string{"old-native", "old-activity", legacyID, "old-mixed"} {
		if !strings.Contains(comparison, "[id="+want+"]") {
			t.Errorf("known legacy source %s excluded: %q", want, comparison)
		}
	}
	if strings.Contains(comparison, "private-") || provenance == nil || provenance.PromptSafe {
		t.Fatalf("legacy eligibility upgraded completeness or admitted private inputs: %q, %+v", comparison, provenance)
	}
	if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
		t.Fatal(err)
	}
	comparison, _ = loadMemoryGenerationContext(a.ctx, a)
	if strings.Contains(comparison, "old-activity") || strings.Contains(comparison, legacyID) || strings.Contains(comparison, "old-mixed") || !strings.Contains(comparison, "old-native") {
		t.Fatalf("legacy compatibility bypassed current source gates: %q", comparison)
	}
}

func TestMemoryGenerationPromptSafetySurvivesEditsUpdatesAndDream(t *testing.T) {
	for _, historical := range []bool{false, true} {
		name := "safe"
		if historical {
			name = "historical"
		}
		t.Run(name, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			mc := NewMemoryGenerator(a)
			response := &mcMemoryGenerationResponse{Title: "Native source", Dimension: "user_insight", MemoryPoints: []string{"source lineage marker"}}
			if !historical {
				loadMemoryGenerationPromptContext(a.ctx, a).apply(response)
			}
			id, err := mc.saveMemoryFile(memorySourceConversation, "source-thread", response)
			if err != nil {
				t.Fatal(err)
			}
			assertSafety := func(id string) {
				t.Helper()
				entry, err := a.GetMemoryEntry(id)
				if err != nil {
					t.Fatal(err)
				}
				if p := memoryReadProvenanceFromContent(entry.Content); p.PromptSafe == historical {
					t.Fatalf("%s upgraded or lost prompt safety: %+v", id, p)
				}
			}
			assertSafety(id)
			original, err := a.GetMemoryEntry(id)
			if err != nil {
				t.Fatal(err)
			}
			writeMCPReadMarkdown(t, "edited-copy", original.Content)
			if _, err := a.SaveMemoryEntry("edited-copy", "# Manual edit\n\n- edited source lineage marker"); err != nil {
				t.Fatal(err)
			}
			assertSafety("edited-copy")
			updated := &mcMemoryGenerationResponse{Action: "update", UpdateID: id, Title: "Updated source", Dimension: "user_insight", MemoryPoints: []string{"updated source lineage marker"}}
			loadMemoryGenerationPromptContext(a.ctx, a).apply(updated)
			if err := mc.updateMemoryFile(a.ctx, id, memorySourceConversation, "update-thread", updated, ""); err != nil {
				t.Fatal(err)
			}
			assertSafety(id)
			at := seedMCPReadActivity(t, a, "update-observation", "Editor", "com.example.editor", "ordinary", "", "")
			session := ActivitySessionRow{ID: "update-observation", StartTime: at, AppNames: `["Editor"]`}
			if _, err := (&ActivityTriager{app: a}).updateActivityMemory(a.ctx, id, session, sessionGroup{Sessions: []ActivitySessionRow{session}}, updated); err != nil {
				t.Fatal(err)
			}
			assertSafety(id)
			writeMCPReadMarkdown(t, "manual", buildOKFMemoryMarkdown("manual", "Manual context", time.Now(), "manual", "user_insight", []string{"manual context marker"}))
			dream := &MemoryDreamService{app: a}
			mergedID, err := dream.writeSynthesizedMemory("Merged sources", "- synthesized lineage marker", "user_insight", []string{id, "manual"})
			if err != nil {
				t.Fatal(err)
			}
			assertSafety(mergedID)
			if _, err := a.SaveMemoryEntry(mergedID, "# Edited synthesis\n\n- synthesized lineage marker"); err != nil {
				t.Fatal(err)
			}
			assertSafety(mergedID)
			comparison, _ := loadMemoryGenerationContext(a.ctx, a)
			if !strings.Contains(comparison, "synthesized lineage marker") {
				t.Fatalf("known-source synthesis lost compatibility: %q", comparison)
			}
		})
	}
}

func TestMemoryGenerationPromptSafetyCannotComeFromModelJSON(t *testing.T) {
	a := newMCPReadTestApp(t)
	var response mcMemoryGenerationResponse
	if err := json.Unmarshal([]byte(`{"inputPromptSafe":true,"inputProvenance":{"version":2,"kinds":["manual"],"prompt_safe":true}}`), &response); err != nil {
		t.Fatal(err)
	}
	if response.inputPromptSafe || response.inputProvenance != nil {
		t.Fatal("model granted itself prompt provenance")
	}
	loadMemoryGenerationPromptContext(a.ctx, a).apply(&response)
	if err := json.Unmarshal([]byte(`{"inputPromptSafe":false}`), &response); err != nil {
		t.Fatal(err)
	}
	if !response.inputPromptSafe {
		t.Fatal("model changed code-owned input provenance")
	}
	for _, value := range []memoryReadProvenance{
		{Version: 1, Kinds: []string{"conversation"}, PromptSafe: true},
		{Version: 2, Kinds: []string{"manual"}, Withheld: true, PromptSafe: true},
		{Version: 2, PromptSafe: true},
		{Version: 2, Kinds: []string{"unknown"}, PromptSafe: true},
	} {
		if memoryReadPromptSafe(mergeMemoryReadProvenance(memoryReadSource("manual"), value)) {
			t.Fatalf("merge upgraded an incomplete source: %+v", value)
		}
	}
}

func TestMemoryGenerationBudgetNeverRendersPartialUntrackedEntry(t *testing.T) {
	a := newMCPReadTestApp(t)
	writePromptSafeActivityMemory(t, a, "oversized-observation", "Oversized source", strings.Repeat("oversized private context ", 40))
	writeMCPReadMarkdown(t, "small-manual", buildOKFMemoryMarkdown("small-manual", "Small source", time.Now(), "manual", "user_insight", []string{"small manual marker"}))
	text, provenance := loadMemoryGenerationContextWithBudget(a.ctx, a, 300)
	if len(text) > 300 || !strings.Contains(text, "small manual marker") || strings.Contains(text, "Oversized") || strings.Contains(text, "oversized") {
		t.Fatalf("context budget included a partial entry: %q", text)
	}
	if provenance == nil || len(provenance.Sessions) != 0 || !memoryReadPromptSafe(*provenance) {
		t.Fatalf("context provenance does not match rendered entries: %+v", provenance)
	}
}
