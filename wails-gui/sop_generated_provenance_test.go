package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/sop"
	"yansu-agent/agent"
)

type sopProvenanceAuthorRunner struct {
	memoryTranslationFakeRunner
	inputs []agent.TaskInput
}

func (r *sopProvenanceAuthorRunner) ExecuteTask(_ context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.inputs = append(r.inputs, *input)
	if input.WorkDir == "" {
		return "", callback(&agent.StreamEvent{Type: "assistant", Content: `{"verdict":"create","activityIds":["observed"],"name":"Matched process","trigger":"Matched trigger"}`})
	}
	// The author must not be able to remove code-owned source restrictions.
	draft := "---\ntitle: Generated process\nsource: manual\nsourceIds: []\nreadProvenance: {\"version\":1}\n---\nGenerated steps.\n"
	return "", os.WriteFile(filepath.Join(input.WorkDir, "SOP.md"), []byte(draft), 0600)
}

func seedSOPGeneratedEvidence(t *testing.T, app *App) int64 {
	t.Helper()
	start := time.Now().Add(-time.Hour).UnixMilli()
	seedSOPPrivacyActivity(t, app, "observed", "Diary", "", "Observed workflow", start)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET app_names='["Diary"]' WHERE id='observed-session'`); err != nil {
		t.Fatal(err)
	}
	return start
}

func TestSOPGeneratedEvidenceRetainsRealSourceIDs(t *testing.T) {
	for _, mode := range []string{"manual-range", "manual-description", "triage-lookback"} {
		t.Run(mode, func(t *testing.T) {
			app := newSOPSourcePrivacyApp(t)
			start := seedSOPGeneratedEvidence(t, app)
			runner := &sopProvenanceAuthorRunner{}
			app.runner = runner
			spec := sopAuthorSpec{Source: sop.SourceManual, SourceID: "manual-request", WindowStartMs: start - 1, WindowEndMs: start + 60_000}
			switch mode {
			case "manual-description":
				spec = app.buildDescribedSOPSpec(t.Context(), "Document my recurring workflow")
			case "triage-lookback":
				spec.Source = sop.SourceTriage
				spec.SourceID = "safe-source"
				spec.SourceSessionIDs = []string{"safe-source"}
			}
			saved, err := app.authorSOP(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if !containsString(saved.SourceIDs, "observed-session") || containsString(saved.SourceIDs, "manual-request") || saved.ReadProvenance == "" {
				t.Fatalf("generated evidence lost real dependencies: %+v", saved)
			}
			if mode == "triage-lookback" && !containsString(saved.SourceIDs, "safe-source") {
				t.Fatal("lookback replaced captured trigger source")
			}
			if !app.sopEntryAllowsModelRead(saved) {
				t.Fatalf("safe generated SOP unreadable: %+v", saved)
			}
			// A user confirmation/edit must preserve the source restrictions.
			saved, err = app.SaveSOP(saved.ID, "Edited process", saved.Description, saved.Trigger, saved.Body)
			if err != nil {
				t.Fatal(err)
			}
			pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
			if app.sopEntryAllowsModelRead(saved) {
				t.Fatal("manual request/confirmation laundered observed source")
			}
			manual := saveSOPPrivacyEntry(t, app, "handwritten", sop.SourceManual)
			if !app.sopEntryAllowsModelRead(manual) {
				t.Fatal("source-free handwritten manual SOP was lost")
			}
		})
	}
}

func TestSOPDescribeMetadataRetainsCapturedSources(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	seedSOPGeneratedEvidence(t, app)
	runner := &sopProvenanceAuthorRunner{}
	app.runner = runner
	spec := app.buildDescribedSOPSpec(t.Context(), "Document recurring workflow")
	if spec.Name != "Matched process" || !containsString(spec.SourceSessionIDs, "observed-session") {
		t.Fatalf("matcher omitted metadata provenance: %+v", spec)
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	if _, err := app.authorSOP(t.Context(), spec); err == nil || len(runner.inputs) != 1 {
		t.Fatalf("captured name/trigger reached author after protection: calls=%d err=%v", len(runner.inputs), err)
	}
}

func TestSOPSourceFreeGenerationDoesNotGainManualExemption(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	app.runner = &sopProvenanceAuthorRunner{}
	saved, err := app.authorSOP(t.Context(), sopAuthorSpec{Source: sop.SourceManual, SourceID: "manual-request", Name: "Generate a process"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ReadProvenance == "" || len(saved.SourceIDs) != 0 || app.sopEntryAllowsModelRead(saved) {
		t.Fatalf("unproven generated content acquired handwritten permission: %+v", saved)
	}
	if !app.sopEntryAllowsModelRead(sop.Entry{Source: sop.SourceManual}) {
		t.Fatal("explicit handwritten manual policy changed")
	}
}

func TestSOPRecallSnapshotsRetainDependenciesAndRecheckCurrentFiles(t *testing.T) {
	for _, change := range []string{"captured-source", "current-file", "unresolved-apps"} {
		t.Run(change, func(t *testing.T) {
			app := newSOPSourcePrivacyApp(t)
			store, err := NewMemoryStore(filepath.Join(os.Getenv("HOME"), configDirName), MemoryStoreConfig{Provider: "none"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			app.memoryStore = store
			for _, source := range []MemorySource{MemorySourceMemory, MemorySourceKnowledge} {
				id := string(source) + "-recall"
				dir, err := memoryDir()
				if source == MemorySourceKnowledge {
					dir, err = knowledgeDir()
				}
				if err != nil {
					t.Fatal(err)
				}
				proof := memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"later-private-source"}, Apps: []string{"Diary"}}
				if change == "unresolved-apps" {
					proof.Sessions = nil
				}
				body := withMemoryReadProvenance(buildOKFMemoryMarkdown(id, "soplineageneedle", time.Now(), "activity", "custom", []string{"source fact"}), proof)
				if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Sync("sop-lineage"); err != nil {
				t.Fatal(err)
			}
			var sources sopSeedSources
			spec := sopAuthorSpec{Source: sop.SourceManual, Name: "soplineageneedle", SourceSessionIDs: []string{"safe-source"}}
			dir := t.TempDir()
			if err := app.writeSOPSeedWithSources(dir, spec, &sources); err != nil {
				t.Fatal(err)
			}
			if len(sources.Memories) != 2 || !containsString(sources.Apps, "Diary") {
				t.Fatalf("recall lost captured dependencies: %+v", sources)
			}
			spec.SourceSessionIDs = append(spec.SourceSessionIDs, sources.Sessions...)
			spec.readProvenance = sources.readProvenance()
			_, meta, _ := app.resolveSOPInstallTarget(spec, sop.Entry{Title: "Derived"})
			if change == "unresolved-apps" {
				if app.sopEntryAllowsModelRead(meta) {
					t.Fatal("unresolved app-derived recall acquired source-free manual permission")
				}
				return
			}
			if !containsString(meta.SourceIDs, "later-private-source") || !app.sopEntryAllowsModelRead(meta) {
				t.Fatalf("safe recalled provenance missing: %+v", meta)
			}
			memoryPath, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "captured-source":
				if err := os.WriteFile(filepath.Join(memoryPath, string(MemorySourceMemory)+"-recall.md"), []byte("# Safe replacement\n"), 0600); err != nil {
					t.Fatal(err)
				}
				pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
			case "current-file":
				if err := os.WriteFile(filepath.Join(memoryPath, string(MemorySourceMemory)+"-recall.md"), []byte("# Now private\n\n- **Activity Session**: private-window-source\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &sopProvenanceAuthorRunner{}
			if _, err := app.executeSOPAuthorTask(t.Context(), runner, spec, dir, sources); err == nil || len(runner.inputs) != 0 {
				t.Fatalf("changed recalled source reached model: calls=%d err=%v", len(runner.inputs), err)
			}
		})
	}
}

func TestSOPGeneratedProofSurvivesExportAndCannotBeForgedByImport(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	entry := saveSOPPrivacyEntry(t, app, "generated", sop.SourceManual, "safe-source")
	entry.ReadProvenance = sopReadProvenanceJSON(sopReadProvenance{Version: 1, Apps: []string{"Diary"}})
	if _, err := app.sopStore.Save(entry); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "bundle.zip")
	if err := zipDirectory(app.sopStore.PathFor(entry.ID), archive); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := extractZipArchive(archive, dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "SOP.md"))
	if err != nil || !strings.Contains(string(raw), "readProvenance:") {
		t.Fatalf("export erased source proof: %s %v", raw, err)
	}
	imported, err := app.sopStore.InstallBundle("imported", dir, importedSOPMetadata())
	if err != nil || app.sopEntryAllowsModelRead(imported) {
		t.Fatalf("import laundered local source authority: %+v err=%v", imported, err)
	}
	for _, proof := range []string{`{"version":99}`, `{"version":1,"readBlocked":true}`, `invalid`} {
		entry.ReadProvenance = proof
		if app.sopEntryAllowsModelRead(entry) {
			t.Fatalf("invalid generated proof allowed: %s", proof)
		}
	}
}

func TestMCPPlaybookStatusHonorsSOPGeneratedProof(t *testing.T) {
	app := newMCPReadTestApp(t)
	prior := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	for _, fixture := range []struct{ id, proof string }{
		{"manual", ""},
		{"blocked", `{"version":1,"readBlocked":true}`},
		{"sensitive", `{"version":1,"apps":["Diary"]}`},
		{"invalid", `{"version":99}`},
	} {
		if _, err := app.sopStore.Save(sop.Entry{ID: fixture.id, Title: fixture.id, Source: sop.SourceManual, ReadProvenance: fixture.proof}); err != nil {
			t.Fatal(err)
		}
		if _, err := app.playbookPlans.Update(fixture.id, func(plan *sop.Plan) { plan.BodyHash = "approved" }); err != nil {
			t.Fatal(err)
		}
	}
	result := callMCPReadTest(t, app, "playbook_status", map[string]any{})
	if len(result.Items) != 1 || result.Items[0].ID.Text != "manual" {
		raw, _ := json.Marshal(result)
		t.Fatalf("MCP exposed generated protected metadata: %s", raw)
	}
}
