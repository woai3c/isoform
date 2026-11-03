package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"yansu-agent/agent"
)

func TestMemoryUpdatesRetainWithheldSourceRestrictions(t *testing.T) {
	for _, writer := range []string{"conversation", "activity"} {
		for _, fixture := range []struct {
			name, content       string
			app, session        string
			withheld, readBlock bool
		}{
			{name: "legacy-app", content: "# Legacy\n\n- **Apps**: Diary\n\n## Memory Points\n\n- old private fact", app: "Diary", withheld: true},
			{name: "legacy-session", content: "# Legacy\n\n- **Activity Session**: private-source\n\n## Memory Points\n\n- old private fact", session: "private-source", withheld: true},
			{name: "valid-envelope-extra-session", content: renderOKFDocument(map[string]any{"title": "Valid envelope", "activity_sessions": []string{"private-source"}, "read_provenance": `{"version":2,"kinds":["manual"]}`}, "## Memory Points\n\n- old private fact"), session: "private-source"},
			{name: "ambiguous-envelope", content: renderOKFDocument(map[string]any{"title": "Ambiguous envelope", "read_provenance": `{"version":1,"kinds":["conversation"],"apps":["Diary"],"sessions":["private-source"]}`}, "## Memory Points\n\n- old private fact"), app: "Diary", session: "private-source", withheld: true},
			{name: "malformed-envelope", content: renderOKFDocument(map[string]any{"title": "Malformed envelope", "read_provenance": `{"version":2,"apps":[`}, "## Memory Points\n\n- old private fact"), withheld: true, readBlock: true},
			{name: "unsupported-envelope", content: renderOKFDocument(map[string]any{"title": "Unsupported envelope", "read_provenance": `{"version":3,"kinds":["manual"]}`}, "## Memory Points\n\n- old private fact"), withheld: true, readBlock: true},
		} {
			t.Run(writer+"/"+fixture.name, func(t *testing.T) {
				app := newPrivacyReadTestApp(t)
				previous := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
				pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
				insertTestSession(t, app.activity.store, "private-source", "protected", 1000)
				if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
					t.Fatal(err)
				}
				runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"memoryPoints":["old private fact","new current fact"]}`}}
				app.runner = runner
				writePrivacyMemory(t, "target", fixture.content)
				if app.memorySourceAllowsRead(fixture.content) {
					t.Fatal("fixture must already deny source reads")
				}
				response := &mcMemoryGenerationResponse{Title: "Updated target", Dimension: "user_insight", MemoryPoints: []string{"new current fact"}}
				var err error
				switch writer {
				case "conversation":
					_, _, err = NewMemoryGenerator(app).updateMemoryFileDetailed(t.Context(), "target", memorySourceConversation, "current", response, "")
				case "activity":
					insertTestSession(t, app.activity.store, "current", "dismissed", 100_000)
					session, readErr := app.activity.store.GetSessionByID("current")
					if readErr != nil || session == nil {
						t.Fatalf("current activity: %v", readErr)
					}
					_, err = (&ActivityTriager{app: app}).updateActivityMemory(t.Context(), "target", *session, sessionGroup{Sessions: []ActivitySessionRow{*session}}, response)
				}
				if err != nil || len(runner.inputs) != 0 {
					t.Fatalf("protected target must update locally: err=%v calls=%d", err, len(runner.inputs))
				}
				entry, err := app.GetMemoryEntry("target")
				if err != nil || !strings.Contains(entry.Content, "old private fact") || !strings.Contains(entry.Content, "new current fact") {
					t.Fatalf("local update lost retained facts: err=%v content=%s", err, entry.Content)
				}
				p := memoryReadProvenanceFromContent(entry.Content)
				if p.Withheld != fixture.withheld || p.ReadBlocked != fixture.readBlock || (fixture.app != "" && !containsString(p.Apps, fixture.app)) || (fixture.session != "" && !containsString(p.Sessions, fixture.session)) {
					t.Fatalf("update changed source restrictions or granted sharing authority: %+v", p)
				}
				if app.memorySourceAllowsRead(entry.Content) {
					t.Fatal("update made retained private facts readable")
				}
				if _, err := app.memoryEntryForExternalRead("target"); !os.IsNotExist(err) {
					t.Fatalf("updated private facts became available to CLI: %v", err)
				}
			})
		}
	}
}

func TestMemoryDreamRetainsLegacyAndExtraEnvelopeRestrictions(t *testing.T) {
	for _, fixture := range []struct {
		name, content string
		withheld      bool
	}{
		{name: "legacy", content: "# Legacy\n\n- **Apps**: Diary\n- **Activity Session**: captured-source\n\n## Memory Points\n\n- retained legacy fact", withheld: true},
		{name: "ambiguous-envelope", content: renderOKFDocument(map[string]any{"title": "Ambiguous envelope", "read_provenance": `{"version":1,"kinds":["conversation"],"apps":["Diary"],"sessions":["captured-source"]}`}, "## Memory Points\n\n- retained legacy fact"), withheld: true},
		{name: "valid-envelope-extra-restrictions", content: renderOKFDocument(map[string]any{"title": "Extra restrictions", "apps": []string{"Diary"}, "activity_sessions": []string{"captured-source"}, "read_provenance": `{"version":2,"kinds":["manual"]}`}, "## Memory Points\n\n- retained legacy fact")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			app.ctx = context.Background()
			previous := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
			pii.SetUserSensitiveApps(nil)
			insertTestSession(t, app.activity.store, "captured-source", "dismissed", 1000)
			writePrivacyMemory(t, "source", fixture.content)
			writePrivacyMemory(t, "safe", buildOKFMemoryMarkdown("safe", "Safe note", time.Now(), memorySourceConversation, "user_insight", []string{"ordinary fact"}))
			runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"delete":[],"synthesize":[{"title":"Merged note","content":"retained legacy fact and ordinary fact","dimension":"user_insight"}]}`}}
			app.runner = runner
			var semanticCalls atomic.Int32
			app.memoryCanonicalizer = func(string, string) (string, error) {
				semanticCalls.Add(1)
				return `{"version":"isoform-memory-semantics-v1","status":"no_facts","facts":[]}`, nil
			}
			service := NewMemoryDreamService(app)
			candidates, err := service.gatherCandidates(t.Context())
			if err != nil || len(candidates) != 2 {
				t.Fatalf("safe sources before privacy change: count=%d err=%v", len(candidates), err)
			}
			run := &DreamRun{}
			if err := service.runL3Merge(run, candidates, false, &DreamPreview{}); err != nil || run.SynthesizedCreated != 1 || len(runner.inputs) != 1 {
				t.Fatalf("synthesis failed: err=%v count=%d calls=%d", err, run.SynthesizedCreated, len(runner.inputs))
			}
			if runner.inputs[0].BackendHint != agent.BackendHintLearning {
				t.Fatalf("dream merge escaped the in-house learning route: %+v", runner.inputs[0])
			}
			if semanticCalls.Load() != 1 {
				t.Fatalf("readable synthesis optional semantic probes=%d, want 1", semanticCalls.Load())
			}
			entries, err := app.GetMemoryEntries()
			if err != nil {
				t.Fatal(err)
			}
			var synthesized MemoryEntry
			for _, entry := range entries {
				if entry.Source == "dream-synthesis" {
					synthesized = entry
				}
			}
			p := memoryReadProvenanceFromContent(synthesized.Content)
			if synthesized.ID == "" || p.Withheld != fixture.withheld || p.ReadBlocked || !containsString(p.Apps, "Diary") || !containsString(p.Sessions, "captured-source") {
				t.Fatalf("synthesis erased restrictions or granted authority: id=%s provenance=%+v", synthesized.ID, p)
			}
			if !app.memorySourceAllowsRead(synthesized.Content) {
				t.Fatal("MCP withholding unexpectedly blocked ordinary local use")
			}
			// Flattened restrictions survive removal of the input memory.
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "source.md")); err != nil {
				t.Fatal(err)
			}
			pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
			if app.memorySourceAllowsRead(synthesized.Content) {
				t.Fatal("synthesis lost a subsequently protected app")
			}
			pii.SetUserSensitiveApps(nil)
			if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='captured-source'`); err != nil {
				t.Fatal(err)
			}
			if app.memorySourceAllowsRead(synthesized.Content) {
				t.Fatal("synthesis lost a subsequently protected session")
			}
		})
	}
}

func TestMemoryProvenanceFailuresRetainCapturedRestrictions(t *testing.T) {
	content := "# Legacy activity\n\n- **Updated**: 2026-10-16 10:00\n- **Source**: activity\n- **Dimension**: user_insight\n- **Apps**: Diary\n- **Activity Session**: captured-source\n\n## Memory Points\n\n- retained fact"
	dir := t.TempDir()
	for _, p := range []memoryReadProvenance{
		memoryReadCapturedProvenance(dir, "missing", content),
		memoryReadProvenanceForFile(dir, "unproven-identity", content),
	} {
		if !p.Withheld || p.ReadBlocked || !containsString(p.Apps, "Diary") || !containsString(p.Sessions, "captured-source") {
			t.Fatalf("failed source resolution discarded restrictions or changed local use: %+v", p)
		}
	}
}

func TestMemoryProvenanceUnionsOverlappingRestrictions(t *testing.T) {
	for _, fixture := range []struct {
		name, kind          string
		version             int
		withheld, readBlock bool
	}{
		{name: "valid", kind: "manual", version: 2},
		{name: "ambiguous", kind: "conversation", version: 1, withheld: true},
		{name: "unsupported", kind: "manual", version: 3, withheld: true, readBlock: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			original := memoryReadProvenance{
				Version:  fixture.version,
				Kinds:    []string{fixture.kind, fixture.kind},
				Apps:     []string{"Diary", "Editor", " Diary ", ""},
				Sessions: []string{"captured", "extra", " captured ", ""},
			}
			content := renderOKFDocument(map[string]any{
				"apps":              []string{" Diary ", "Terminal", "Diary", ""},
				"activity_sessions": []string{" captured ", "other", "captured", ""},
				"read_provenance":   memoryReadProvenanceJSON(original),
			}, "Retained fact")
			p := memoryReadProvenanceFromContent(content)
			if strings.Join(p.Apps, ",") != "Diary,Editor,Terminal" || strings.Join(p.Sessions, ",") != "captured,extra,other" {
				t.Fatalf("overlapping envelope and frontmatter restrictions were not unique in source order: %+v", p)
			}
			if p.Withheld != fixture.withheld || p.ReadBlocked != fixture.readBlock {
				t.Fatalf("deduplicating restrictions changed authority: %+v", p)
			}
			for range 3 {
				p = mergeMemoryReadProvenance(p, p, memoryReadSource("manual"))
				if strings.Join(p.Apps, ",") != "Diary,Editor,Terminal" || strings.Join(p.Sessions, ",") != "captured,extra,other" || strings.Join(p.Kinds, ",") != "manual" {
					t.Fatalf("merged envelope persisted duplicate dependencies: %+v", p)
				}
				p = memoryReadProvenanceFromContent(withMemoryReadProvenance(content, p))
				if strings.Join(p.Apps, ",") != "Diary,Editor,Terminal" || strings.Join(p.Sessions, ",") != "captured,extra,other" {
					t.Fatalf("repeated merge grew source restrictions: %+v", p)
				}
				if p.Withheld != fixture.withheld || p.ReadBlocked != fixture.readBlock {
					t.Fatalf("repeated merge changed source authority: %+v", p)
				}
				if !p.ReadBlocked && strings.Join(p.Kinds, ",") != "manual" {
					t.Fatalf("repeated merge grew source kinds: %+v", p)
				}
			}
		})
	}
}
