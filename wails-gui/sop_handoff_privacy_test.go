package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
	"wails-gui/internal/sop"
)

func seedSOPPrivacyActivity(t *testing.T, app *App, id, appName, event, title string, start int64) {
	t.Helper()
	store := app.activity.store
	insertTestSession(t, store, id+"-session", "dismissed", start)
	if event != "" {
		if _, err := store.db.Exec(`UPDATE activity_events SET data = ? WHERE session_id = ?`, event, id+"-session"); err != nil {
			t.Fatal(err)
		}
	}
	run := ActivityTimelineRunRow{
		ID: id + "-run", AppName: appName, AppKey: "app:" + strings.ReplaceAll(appName, " ", ""),
		StartMs: start, EndMs: start + 30_000, ActiveMs: 20_000,
		FirstEventRowID: start, LastEventRowID: start, SessionIDs: []string{id + "-session"},
		EventCount: 1, Status: timelineRunStatusClosed,
	}
	if err := insertTimelineRun(store.db, run, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertActivity(ActivityRow{
		ID: id, Title: title, Description: "description " + activityPrivacySecret,
		LatestChange: "change " + activityPrivacySecret, Status: activityStatusActive,
		StartMs: start, EndMs: start + 30_000, RunIDs: composeJSON([]string{run.ID}),
		ActiveMsByApp: composeJSON(map[string]int64{appName: 20_000}),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSOPAuthorEvidenceFiltersSourcesBeforeCaps(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	seedSOPPrivacyActivity(t, app, "safe", "Public Editor", "", "safe-evidence "+activityPrivacySecret, 1000)
	seedSOPPrivacyActivity(t, app, "private-browser", "Browser private marker", `{"appName":"Chrome","windowTitle":"Bank - Incognito"}`, "private-bucket-marker", 2000)
	// Newer protected rows outnumber both seed caps. They must never crowd
	// the older public work out of the evidence budget.
	for i := range sopSeedMaxTimelineRuns + 1 {
		seedSOPPrivacyActivity(t, app, fmt.Sprintf("hidden-%d", i), "Diary", "", "private-bucket-marker", int64(3000+i))
	}
	dir := t.TempDir()
	app.writeSOPActivityEvidence(dir, sopAuthorSpec{WindowStartMs: 1, WindowEndMs: 100_000})
	for _, name := range []string{"activities.md", "timeline.md"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"Diary", "private-bucket-marker", "Browser private marker", activityPrivacySecret} {
			if strings.Contains(string(body), private) {
				t.Errorf("%s exposed %q", name, private)
			}
		}
		if !strings.Contains(string(body), "Public Editor") {
			t.Errorf("%s lost safe evidence behind protected rows: %s", name, body)
		}
	}
}

func TestSOPDescriptionMatcherFiltersSourcesBeforeModelPass(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	seedSOPPrivacyActivity(t, app, "safe", "Public Editor", "", "safe-description "+activityPrivacySecret, 1000)
	seedSOPPrivacyActivity(t, app, "private", "Diary", "", "private-description-marker", 2000)
	seedSOPPrivacyActivity(t, app, "private-browser", "Chrome", `{"windowTitle":"Inbox - Incognito"}`, "private-browser-marker", 3000)
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"activityIds":["safe","private","private-browser"]}`}}
	app.runner = runner
	result, ok := app.matchActivitiesForDescription(t.Context(), "describe "+activityPrivacySecret, 1, 100_000)
	if !ok || len(runner.inputs) != 1 || len(result.ActivityIDs) != 1 || result.ActivityIDs[0] != "safe" {
		t.Fatalf("matcher failed to retain only safe candidates: %+v calls=%d ok=%v", result, len(runner.inputs), ok)
	}
	for _, private := range []string{"private-description-marker", "private-browser-marker", activityPrivacySecret} {
		if strings.Contains(runner.inputs[0].Prompt, private) {
			t.Errorf("matcher sent %q", private)
		}
	}
	app.closedEyes.enabled.Store(true)
	if _, ok := app.matchActivitiesForDescription(t.Context(), "describe", 1, 100_000); ok || len(runner.inputs) != 1 {
		t.Fatal("closed-eyes matcher reached model")
	}
}

func TestSOPRecallFiltersBeforeEntryBudget(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	store, err := NewMemoryStore(filepath.Join(os.Getenv("HOME"), configDirName), MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app.memoryStore = store
	for _, dirFn := range []func() (string, error){memoryDir, knowledgeDir} {
		dir, err := dirFn()
		if err != nil {
			t.Fatal(err)
		}
		for i := range 35 {
			body := "# Cooking\n\n- Vegetables and pasta."
			if i < 4 {
				body = fmt.Sprintf("# Safe recall %d\n\n- authorrecallneedle allowed-recall-marker %d %s", i, i, activityPrivacySecret)
			} else if i < 9 {
				body = "# Protected recall\n\n- **Apps**: Diary\n\n- authorrecallneedle protected-recall-marker"
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-recall-%d.md", filepath.Base(dir), i)), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.Sync("author-recall-privacy"); err != nil {
		t.Fatal(err)
	}
	indexed, err := store.Search(t.Context(), "authorrecallneedle", 200)
	if err != nil {
		t.Fatal(err)
	}
	readableBySource := map[string]int{}
	for _, result := range indexed {
		if strings.Contains(result.Snippet, "allowed-recall-marker") {
			readableBySource[result.Source]++
		}
	}
	if readableBySource[string(MemorySourceMemory)] != 4 || readableBySource[string(MemorySourceKnowledge)] != 4 {
		t.Fatalf("fixture did not index all source-specific entries: %+v", readableBySource)
	}
	for _, kind := range []string{"sop"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			var err error
			switch kind {
			case "sop":
				err = app.writeSOPSeed(dir, sopAuthorSpec{Source: sop.SourceManual, Name: "authorrecallneedle"})
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"memory.md", "knowledge.md"} {
				body, err := os.ReadFile(filepath.Join(dir, "context", name))
				if err != nil {
					t.Fatal(err)
				}
				text := string(body)
				if strings.Contains(text, "Protected recall") || strings.Contains(text, "protected-recall-marker") || strings.Contains(text, activityPrivacySecret) {
					t.Fatalf("%s leaked protected recall: %s", name, text)
				}
				if count := strings.Count(text, "[id="); count != 3 || strings.Count(text, "allowed-recall-marker") != 3 {
					t.Fatalf("%s did not preserve three safe recall entries: %s", name, text)
				}
			}
		})
	}
}

func TestAuthorSeedsAndClassifiersRedactBeforeTruncation(t *testing.T) {
	for name, fixture := range map[string]struct {
		cap   int
		limit func(string) string
	}{
		"sop":             {sopSeedMaxBytes, limitSOPSeed},
		"classifier-line": {sopDescribeLineCap, func(value string) string { return oneLineClamp(value, sopDescribeLineCap) }},
	} {
		t.Run(name, func(t *testing.T) {
			got := fixture.limit(strings.Repeat("x", fixture.cap-20) + " " + activityPrivacySecret + " trailing")
			if strings.Contains(got, "sk-proj-") || !strings.Contains(got, "[REDACTED:") {
				t.Fatalf("seed truncation split a secret: %q", got)
			}
		})
	}
	for name, prompt := range map[string]string{
		"activity-match": buildSOPActivityMatchPrompt(activityPrivacySecret, []ActivityRow{{Title: activityPrivacySecret, Description: activityPrivacySecret}}),
		"dedup":          buildSOPDedupGatePrompt(activityPrivacySecret, []sop.Entry{{Title: activityPrivacySecret}}),
	} {
		if strings.Contains(prompt, activityPrivacySecret) || !strings.Contains(prompt, "[REDACTED:") {
			t.Errorf("%s classifier leaked candidate content", name)
		}
	}
}

func TestSOPAuthorRechecksEveryCapturedTriageSource(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	seedSOPPrivacyActivity(t, app, "first", "Editor", "", "Public work", 1000)
	seedSOPPrivacyActivity(t, app, "second", "Diary", "", "Private work", 2000)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET app_names = '["Diary"]' WHERE id = 'second-session'`); err != nil {
		t.Fatal(err)
	}
	spec := sopAuthorSpec{
		Source: sop.SourceTriage, SourceID: "first-session",
		SourceSessionIDs: []string{"first-session", "second-session"},
		Name:             "captured process", GroupSummary: "captured private detail",
	}
	if !app.sopSourceAllowsModelRead(spec) {
		t.Fatal("public captured inputs should be readable")
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	if err := app.writeSOPSeed(t.TempDir(), spec); err == nil {
		t.Fatal("second source became protected but its captured summary was written")
	}
	runner := &modelPrivacyTaskRecorder{}
	if _, reason := app.applySOPDedupGate(t.Context(), runner, spec); reason == "" || len(runner.inputs) > 0 {
		t.Fatalf("protected captured input reached classifier: reason=%q calls=%d", reason, len(runner.inputs))
	}
}
