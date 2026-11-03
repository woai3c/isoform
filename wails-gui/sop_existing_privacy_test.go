package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/sop"
	"yansu-agent/agent"
)

func newSOPSourcePrivacyApp(t *testing.T) *App {
	t.Helper()
	app := newPrivacyReadTestApp(t)
	prior := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
	pii.SetUserSensitiveApps(nil)
	store, err := sop.NewStore(filepath.Join(os.Getenv("HOME"), "sops"))
	if err != nil {
		t.Fatal(err)
	}
	app.sopStore = store
	for _, id := range []string{"safe-source", "later-private-source", "private-window-source"} {
		insertTestSession(t, app.activity.store, id, "dismissed", 1000)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET app_names='["Diary"]' WHERE id='later-private-source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='{"appName":"Safari","windowTitle":"ordinary title","privateWindow":true}' WHERE session_id='private-window-source'`); err != nil {
		t.Fatal(err)
	}
	return app
}

func saveSOPPrivacyEntry(t *testing.T, app *App, id string, source sop.Source, sourceIDs ...string) sop.Entry {
	t.Helper()
	entry, err := app.sopStore.Save(sop.Entry{
		ID: id, Title: id + " title " + activityPrivacySecret,
		Description: id + " description " + activityPrivacySecret,
		Trigger:     id + " trigger " + activityPrivacySecret,
		Body:        id + " body " + activityPrivacySecret,
		Source:      source, SourceIDs: sourceIDs, Status: sop.StatusConfirmed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestSOPModelReadsFilterConfirmedSourceProvenance(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	saveSOPPrivacyEntry(t, app, "safe-playbook", sop.SourceTriage, "safe-source")
	saveSOPPrivacyEntry(t, app, "manual-playbook", sop.SourceManual)
	for _, fixture := range []struct {
		id     string
		source sop.Source
		ids    []string
	}{
		{"protected-playbook", sop.SourceTriage, []string{"safe-source", "later-private-source"}},
		{"manual-derived-private", sop.SourceManual, []string{"later-private-source"}},
		{"private-window-playbook", sop.SourceTriage, []string{"private-window-source"}},
		{"missing-source-playbook", sop.SourceTriage, []string{"missing-session"}},
		{"generated-without-proof", sop.SourceTriage, nil},
		{"unknown-without-proof", "", nil},
		{"imported-without-proof", sop.SourceImported, nil},
	} {
		saveSOPPrivacyEntry(t, app, fixture.id, fixture.source, fixture.ids...)
	}
	// More protected rows than the triage cap must not crowd out safe SOPs.
	for i := 0; i < dedupListCap; i++ {
		saveSOPPrivacyEntry(t, app, "protected-overflow-"+strings.Repeat("x", i+1), sop.SourceTriage, "later-private-source")
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	assertSafeText := func(name, text string) {
		t.Helper()
		for _, forbidden := range []string{"protected-playbook", "manual-derived-private", "private-window-playbook", "missing-source-playbook", "generated-without-proof", "unknown-without-proof", "imported-without-proof", "protected-overflow", activityPrivacySecret} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s leaked %q: %s", name, forbidden, text)
			}
		}
		for _, allowed := range []string{"safe-playbook", "manual-playbook", "[REDACTED:"} {
			if !strings.Contains(text, allowed) {
				t.Errorf("%s lost %q: %s", name, allowed, text)
			}
		}
	}
	assertSafeText("triage", app.buildSOPDedupSection())
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"verdict":"create"}`}}
	spec := sopAuthorSpec{Source: sop.SourceManual, Name: "public candidate"}
	if _, skip := app.applySOPDedupGate(t.Context(), runner, spec); skip != "" || len(runner.inputs) != 1 {
		t.Fatalf("safe dedup unavailable: %q calls=%d", skip, len(runner.inputs))
	}
	assertSafeText("dedup model", runner.inputs[0].Prompt)
	dir := t.TempDir()
	if err := app.writeSOPSeed(dir, spec); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(dir, "context", "existing-sops.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertSafeText("author seed", string(text))
	original, err := app.sopStore.Get("protected-playbook")
	if err != nil || !strings.Contains(original.Body, activityPrivacySecret) {
		t.Fatalf("local SOP changed: %+v %v", original, err)
	}
	app.closedEyes.enabled.Store(true)
	if app.buildSOPDedupSection() != "" {
		t.Fatal("closed eyes exposed SOP metadata")
	}
}

func TestSOPUpdateRejectsProtectedBundleBeforeWorkdir(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	saveSOPPrivacyEntry(t, app, "target", sop.SourceTriage, "safe-source", "later-private-source")
	spec := sopAuthorSpec{Source: sop.SourceManual, Action: "update", UpdateID: "target", Name: "public candidate"}
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true}}
	app.runner = runner
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	if _, skip := app.applySOPDedupGate(t.Context(), runner, spec); skip == "" {
		t.Fatal("explicit update bypassed target gate")
	}
	dir := t.TempDir()
	if err := app.writeSOPSeed(dir, spec); err == nil {
		t.Fatal("protected update seeded author workdir")
	}
	if _, err := os.Stat(filepath.Join(dir, "existing", "SOP.md")); !os.IsNotExist(err) {
		t.Fatalf("protected SOP was copied: %v", err)
	}
	if _, err := app.authorSOP(t.Context(), spec); err == nil || len(runner.inputs) != 0 {
		t.Fatalf("protected author called model: err=%v calls=%d", err, len(runner.inputs))
	}
	pii.SetUserSensitiveApps(nil)
	if err := app.writeSOPSeed(dir, spec); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(dir, "existing", "SOP.md"))
	if err != nil || !strings.Contains(string(text), "target body") || strings.Contains(string(text), activityPrivacySecret) {
		t.Fatalf("safe update lost body/redaction: %s %v", text, err)
	}
}

func TestSOPAuthorRechecksSeededSOPsAtModelBoundary(t *testing.T) {
	for _, action := range []string{"create", "update"} {
		for _, change := range []string{"source-became-private", "stored-provenance-changed", "replacement-cannot-lend-proof"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				app := newSOPSourcePrivacyApp(t)
				entry := saveSOPPrivacyEntry(t, app, "target", sop.SourceTriage, "later-private-source")
				spec := sopAuthorSpec{Source: sop.SourceManual, Action: action}
				if action == "update" {
					spec.UpdateID = "target"
				}
				dir := t.TempDir()
				var sources sopSeedSources
				if err := app.writeSOPSeedWithSources(dir, spec, &sources); err != nil {
					t.Fatal(err)
				}
				if len(sources.SOPs) == 0 {
					t.Fatal("seed did not retain its SOP source proof")
				}
				runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true}}
				if _, err := app.executeSOPAuthorTask(t.Context(), runner, spec, dir, sources); err != nil || len(runner.inputs) != 1 {
					t.Fatalf("safe source blocked: %v calls=%d", err, len(runner.inputs))
				}
				switch change {
				case "source-became-private":
					pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
				case "stored-provenance-changed":
					entry.SourceIDs = []string{"private-window-source"}
					if _, err := app.sopStore.Save(entry); err != nil {
						t.Fatal(err)
					}
				case "replacement-cannot-lend-proof":
					entry.SourceIDs = []string{"safe-source"}
					if _, err := app.sopStore.Save(entry); err != nil {
						t.Fatal(err)
					}
					pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
				}
				if _, err := app.executeSOPAuthorTask(t.Context(), runner, spec, dir, sources); err == nil || len(runner.inputs) != 1 {
					t.Fatalf("changed seed reached author: err=%v calls=%d", err, len(runner.inputs))
				}
			})
		}
	}
}

type sopPrivacyChangingRunner struct {
	modelPrivacyTaskRecorder
	after func()
}

func (r *sopPrivacyChangingRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	result, err := r.modelPrivacyTaskRecorder.ExecuteTask(ctx, input, callback)
	if r.after != nil {
		r.after()
	}
	return result, err
}

func TestSOPDedupSourceChangeStopsAuthorBeforeWorkdir(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	saveSOPPrivacyEntry(t, app, "target", sop.SourceTriage, "later-private-source")
	runner := &sopPrivacyChangingRunner{modelPrivacyTaskRecorder: modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"verdict":"update","updateId":"target"}`}}, after: func() { pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"}) }}
	app.runner = runner
	if _, err := app.authorSOP(t.Context(), sopAuthorSpec{Source: sop.SourceManual, Name: "public candidate"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0].WorkDir != "" {
		t.Fatalf("changed dedup source reached author: %+v", runner.inputs)
	}
}

func TestSOPInstallKeepsEveryCapturedSource(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	_, meta, _ := app.resolveSOPInstallTarget(sopAuthorSpec{Source: sop.SourceTriage, SourceID: "safe-source", SourceSessionIDs: []string{"safe-source", "later-private-source"}}, sop.Entry{Title: "Process"})
	if len(meta.SourceIDs) != 2 || !containsString(meta.SourceIDs, "safe-source") || !containsString(meta.SourceIDs, "later-private-source") {
		t.Fatalf("lost captured dependencies: %+v", meta)
	}
	_, manual, _ := app.resolveSOPInstallTarget(sopAuthorSpec{Source: sop.SourceManual, SourceID: "manual-request-id"}, sop.Entry{Title: "Manual process"})
	if len(manual.SourceIDs) != 0 || !app.sopEntryAllowsModelRead(manual) {
		t.Fatalf("manual request ID treated as activity source: %+v", manual)
	}
	if app.sopEntryAllowsModelRead(sop.Entry{Source: sop.SourceTriage}) || app.sopEntryAllowsModelRead(sop.Entry{}) {
		t.Fatal("missing generated proof became model permission")
	}
}

func TestSOPDetectionAndEntityLogsContainOnlyMetadata(t *testing.T) {
	t.Setenv(backgroundTranscriptDebugEnv, "")
	app := newSOPSourcePrivacyApp(t)
	marker := "private-process-title-and-reason"
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	spec := sopAuthorSpec{Source: sop.SourceManual, Name: marker}
	app.sopAuthorMu.Lock()
	app.tryAuthorSOP(spec)
	app.sopAuthorMu.Unlock()
	store := app.sopStore
	app.sopStore = nil
	app.tryAuthorSOP(spec)
	app.sopStore = store
	saveSOPPrivacyEntry(t, app, "safe-id", sop.SourceManual)
	app.dispatchTriageSOP(&activityTriageResponse{SOPName: marker}, "source-id", "", 1000, 2000)
	for _, verdict := range []string{"update", "skip"} {
		for _, id := range []string{"safe-id", marker} {
			runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"verdict":"` + verdict + `","updateId":"` + id + `","reason":"` + marker + `"}`}}
			app.applySOPDedupGate(t.Context(), runner, spec)
		}
	}
	logEntityAnnotationDuration(7, 3*time.Second, Observation{RealApp: marker, Title: marker})
	if strings.Contains(logs.String(), marker) {
		t.Fatalf("logs contained user content: %s", logs.String())
	}
	for _, metadata := range []string{"name_bytes=", "reason_bytes=", "title_bytes=", "obs=7"} {
		if !strings.Contains(logs.String(), metadata) {
			t.Errorf("log metadata %q missing: %s", metadata, logs.String())
		}
	}
	// The queued dispatch is asynchronous; inspect that log call without
	// launching a background author solely to test its formatting.
	source, err := os.ReadFile("sop_detect.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(source), "\n") {
		if strings.Contains(line, "log.Printf(") && !strings.Contains(line, "activityPrivateLogField(") && (strings.Contains(line, ", spec.Name") || strings.Contains(line, ", name,") || strings.Contains(line, ", resp.Reason)")) {
			t.Errorf("content-bearing log remains: %s", line)
		}
	}
}
