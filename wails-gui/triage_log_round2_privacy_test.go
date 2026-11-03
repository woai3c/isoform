package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
)

func TestTriageSystemReasonsStayReadable(t *testing.T) {
	for _, reason := range []string{"total=5 eligible=2 skippedLowMsg=1 skippedAlreadyProcessed=1 cachedTriage=1", "too few events: 3 < 5", "too short duration: 3s < 5m0s", "too few apps: 1 < 2 (events=10 duration=3m0s, below high-activity exemption 50 / 5m0s)", "all triage features disabled by settings", "AI returned no memory points", "ambient-dominant session: no extractable knowledge signal", "connector conversation has zero [SELF] lines (other=3): refused dimension=\"user_insight\"", "media playback app present in session: refused dimension=\"deep_user_model\", coerced to topic_highlight"} {
		t.Run(reason, func(t *testing.T) {
			_, path := isolateActivityPrivacyLogs(t)
			app := &App{ctx: context.Background(), eventBroadcaster: NewEventBroadcaster()}
			events := app.eventBroadcaster.AddClient("system-reason")
			defer app.eventBroadcaster.RemoveClient("system-reason")
			setTriageApp(app)
			logTriage("activity", "fixed-reason", "dismiss", reason)
			disk, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var saved TriageLogEntry
			if err := json.Unmarshal(disk, &saved); err != nil || saved.Reason != reason {
				t.Fatalf("system disk reason changed: %+v %v", saved, err)
			}
			entries := app.GetTriageLogs()
			if len(entries) != 1 || entries[0].Reason != reason {
				t.Fatalf("system panel reason changed: %+v", entries)
			}
			select {
			case raw := <-events:
				var event struct {
					Args []TriageLogEntry `json:"args"`
				}
				if err := json.Unmarshal(raw, &event); err != nil || len(event.Args) != 1 || event.Args[0].Reason != reason {
					t.Fatalf("system feed reason changed: %s %v", raw, err)
				}
			default:
				t.Fatal("missing live system reason")
			}
		})
	}
}

func TestTriageLearningTitlesUseLiveEntriesPrivacy(t *testing.T) {
	for _, kind := range []string{"knowledge", "memory"} {
		for _, debug := range []string{"0", "1"} {
			t.Run(kind+"/debug="+debug, func(t *testing.T) {
				t.Setenv(backgroundTranscriptDebugEnv, debug)
				_, path := isolateActivityPrivacyLogs(t)
				app := &App{ctx: context.Background(), eventBroadcaster: NewEventBroadcaster()}
				configureTestMemoryCanonicalizer(t, app)
				events := app.eventBroadcaster.AddClient("live-title")
				defer app.eventBroadcaster.RemoveClient("live-title")
				setTriageApp(app)
				const id, title, oldTitle = "live-entry", "Current canonical title", "Old model title never on disk"
				dir, err := flavorDataDir()
				if err != nil {
					t.Fatal(err)
				}
				entryPath := filepath.Join(dir, kind, id+".md")
				if err := os.MkdirAll(filepath.Dir(entryPath), 0700); err != nil {
					t.Fatal(err)
				}
				content := "# " + title + "\n\n- **Source**: manual\n\n## Memory Points\n\n- I prefer clear explanations.\n"
				if err := os.WriteFile(entryPath, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				entry := TriageLogEntry{Source: kind, ID: "source-session", EntryID: id, Decision: "saved", Title: oldTitle, Description: "Private description", Reason: "Private model reason"}
				logTriageEntry(entry)
				assertPanelTitle := func(want string) {
					t.Helper()
					entries := app.GetTriageLogs()
					if len(entries) < 1 || entries[len(entries)-1].Title != want || entries[len(entries)-1].Description != "" {
						t.Fatalf("panel title=%q entries=%+v", want, entries)
					}
				}
				assertEventTitle := func(want string) {
					t.Helper()
					for len(events) > 0 {
						var event struct {
							Event string           `json:"event"`
							Args  []TriageLogEntry `json:"args"`
						}
						if err := json.Unmarshal(<-events, &event); err != nil {
							t.Fatal(err)
						}
						if event.Event == "triage:log" {
							if len(event.Args) != 1 || event.Args[0].Title != want || strings.Contains(event.Args[0].Reason, "Private") {
								t.Fatalf("unexpected panel feed: %+v", event)
							}
							return
						}
					}
					t.Fatal("missing panel feed")
				}
				assertPanelTitle(title)
				assertEventTitle(title)
				// Renaming proves the title is not cached in the triage file.
				renamed := strings.Replace(content, title, "Renamed live title", 1)
				if err := os.WriteFile(entryPath, []byte(renamed), 0600); err != nil {
					t.Fatal(err)
				}
				assertPanelTitle("Renamed live title")
				app.closedEyes.enabled.Store(true)
				assertPanelTitle("")
				app.closedEyes.enabled.Store(false)
				previous := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
				pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
				private := strings.Replace(renamed, "- **Source**: manual", "- **Source**: manual\n- **Apps**: Diary", 1)
				if err := os.WriteFile(entryPath, []byte(private), 0600); err != nil {
					t.Fatal(err)
				}
				assertPanelTitle("")
				logTriageEntry(entry)
				assertEventTitle("")
				if err := os.WriteFile(entryPath, []byte(renamed), 0600); err != nil {
					t.Fatal(err)
				}
				// Exercise the actual Forget path for memory and deletion for knowledge.
				switch kind {
				case "memory":
					preview, err := app.GetMemoryForgetPreview(id)
					if err != nil {
						t.Fatal(err)
					}
					if err := app.ForgetMemoryEntry(id, preview.ConfirmationToken); err != nil {
						t.Fatal(err)
					}
				case "knowledge":
					if err := app.DeleteKnowledgeEntry(id); err != nil {
						t.Fatal(err)
					}
				}
				assertPanelTitle("")
				logTriageEntry(entry)
				assertEventTitle("")
				if kind == "memory" {
					// An externally restored file is still hidden by the Forget marker.
					if err := os.WriteFile(entryPath, []byte(renamed), 0600); err != nil {
						t.Fatal(err)
					}
					assertPanelTitle("")
				}
				disk, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{title, oldTitle, "Renamed live title", "Private description", "Private model reason"} {
					if strings.Contains(string(disk), secret) {
						t.Fatalf("triage persisted %q: %s", secret, disk)
					}
				}
			})
		}
	}
}

func TestTriageReasonProjectionKeepsOriginalByteCounts(t *testing.T) {
	original := TriageLogEntry{Source: "memory", Reason: "model reason with private content", Title: "private title"}
	first := triageLogMetadata(original)
	second := triageLogMetadata(first)
	if *second.ReasonBytes != len(original.Reason) || *second.TitleBytes != len(original.Title) || second.Reason != first.Reason {
		t.Fatalf("projection counted its own redaction: %+v", second)
	}
	// A model cannot smuggle content by extending a recognizable system prefix.
	unsafe := triageLogMetadata(TriageLogEntry{Source: "activity", Reason: "too few events: 3 < 5 private title"})
	if strings.Contains(unsafe.Reason, "private") {
		t.Fatal("system reason prefix admitted private suffix")
	}
	for _, decision := range []string{"worth", "not_worth", "error"} {
		model := triageLogMetadata(TriageLogEntry{Source: "memory", Decision: decision, Reason: "too few events: 3 < 5"})
		if model.Reason == "too few events: 3 < 5" {
			t.Fatalf("model/failure text bypassed projection: %+v", model)
		}
	}

}

func TestTriageLearningNativeGenerationResolvesSavedIDs(t *testing.T) {
	for _, kind := range []string{"knowledge", "memory"} {
		t.Run(kind, func(t *testing.T) {
			_, path := isolateActivityPrivacyLogs(t)
			const title = "Readable native conversation title"
			response := `{"action":"create","title":"` + title + `","dimension":"topic_highlight","memoryPoints":["I prefer clear explanations"],"knowledgePoints":["Use the project build script"]}`
			responses := []string{response}
			if kind == "memory" {
				responses = []string{`{"worth":true}`, response}
			}
			app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{responses: responses})
			if err := app.SaveUIState("ui.language", "en"); err != nil {
				t.Fatal(err)
			}
			mc := NewMemoryGenerator(app)
			var artifacts []memoryPipelineMaterialization
			switch kind {
			case "memory":
				artifacts = mc.runMemoryPipelineDetailed(t.Context(), memorySourceConversation, "native-thread", "[SELF] I prefer clear explanations", "").Materializations
			case "knowledge":
				artifacts = mc.generateAndSaveKnowledgeDetailed(t.Context(), memorySourceConversation, "native-thread", "Use the project build script", "")
			}
			if len(artifacts) != 1 {
				t.Fatalf("generation did not materialize: %+v", artifacts)
			}
			entries := app.GetTriageLogs()
			found := false
			for _, entry := range entries {
				if entry.Source == kind && entry.Decision == "saved" {
					found = entry.EntryID == artifacts[0].ArtifactID && entry.Title == title
				}
			}
			if !found {
				t.Fatalf("saved entry ID did not resolve live title: %+v", entries)
			}
			disk, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(disk), title) {
				t.Fatalf("generation persisted title in triage: %s %v", disk, err)
			}
		})
	}
}
