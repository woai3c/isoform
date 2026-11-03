package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/sop"
	"yansu-agent/agent"
)

func TestSOPTriageDispatchLogKeepsNamePrivate(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			store, err := sop.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Save(sop.Entry{ID: "recent", Title: "Recent process", Status: sop.StatusConfirmed}); err != nil {
				t.Fatal(err)
			}
			app := &App{sopStore: store}
			const name = "private-sop-triage-amber"
			result := app.dispatchTriageSOP(&activityTriageResponse{SOPName: name}, "speech-log", "test summary", 1000, 2000)
			if result.Status != "skipped" || !strings.Contains(result.Reason, "min-gap throttle") || !strings.Contains(output.String(), "triage dispatch skipped") {
				t.Fatalf("throttle behavior changed: result=%+v logs=%s", result, output.String())
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", name)
		})
	}
}

type privacySOPAuthorRunner struct {
	activityMarkdownTranslationFakeRunner
	title string
}

func (r *privacySOPAuthorRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	content := "---\ntitle: " + r.title + "\n---\n\n# Steps\n\nKeep the original source evidence.\n"
	if err := os.WriteFile(filepath.Join(input.WorkDir, "SOP.md"), []byte(content), 0600); err != nil {
		return "", err
	}
	return "privacy-sop-test", callback(&agent.StreamEvent{Type: "assistant", Content: "SOP.md written"})
}

func TestSOPAuthorLogsKeepNamesAndPathsPrivate(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			store, err := sop.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			const name = "private-sop-candidate-meadow"
			const title = "Private authored amber"
			const slug = "private-authored-amber"
			app := &App{sopStore: store, runner: &privacySOPAuthorRunner{title: title}, activity: &activityMonitorState{store: newTestStore(t)}}
			insertTestSession(t, app.activity.store, "speech-log", "done", 1000)
			app.sopAuthorMu.Lock()
			saved, err := app.authorSOP(context.Background(), sopAuthorSpec{Action: "create", Name: name, Source: sop.SourceTriage, SourceID: "speech-log"})
			app.sopAuthorMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if saved.Title != title || saved.ID != slug || saved.Source != sop.SourceTriage || saved.Status != sop.StatusDraft || len(saved.SourceIDs) != 1 || saved.SourceIDs[0] != "speech-log" {
				t.Fatalf("privacy logging changed SOP evidence or lifecycle: %+v", saved)
			}
			persisted, err := store.Get(saved.ID)
			if err != nil || persisted.Title != title || !strings.Contains(persisted.Body, "original source evidence") || app.GetSOPAuthorStatus().Title != name {
				t.Fatalf("privacy logging changed saved content or UI status: saved=%+v error=%v status=%+v", persisted, err, app.GetSOPAuthorStatus())
			}
			if !strings.Contains(output.String(), "bounded authoring") || !strings.Contains(output.String(), "[sop-author] saved") {
				t.Fatalf("author lifecycle diagnostics missing: %s", output.String())
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", name, title, slug)
		})
	}
}

func TestSOPDedupLogsKeepModelContentPrivate(t *testing.T) {
	for _, verdict := range []string{"update", "skip", "unknown", "malformed"} {
		for _, flag := range []string{"", "1"} {
			t.Run(verdict+"/debug="+flag, func(t *testing.T) {
				t.Setenv(backgroundTranscriptDebugEnv, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				store, err := sop.NewStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				const id = "private-sop-slug-meadow"
				const reason = "private-sop-reason-amber"
				if _, err := store.Save(sop.Entry{ID: id, Title: "Existing process", Source: sop.SourceManual, Status: sop.StatusConfirmed}); err != nil {
					t.Fatal(err)
				}
				response := `{"verdict":"` + verdict + `","updateId":"` + id + `","reason":"` + reason + `"}`
				secrets := []string{id, reason}
				switch verdict {
				case "unknown":
					response = `{"verdict":"update","updateId":"` + id + `-missing"}`
					secrets = []string{id + "-missing"}
				case "malformed":
					response = `{"reason":"` + reason + `"`
					secrets = []string{reason}
				}
				app := &App{sopStore: store}
				spec, message := app.applySOPDedupGate(context.Background(), &activityMarkdownTranslationFakeRunner{response: response}, sopAuthorSpec{Action: "create", Name: "Candidate", Source: sop.SourceManual})
				switch verdict {
				case "update":
					if spec.Action != "update" || spec.UpdateID != id || message != "" {
						t.Fatalf("dedup update semantics changed: spec=%+v message=%q", spec, message)
					}
				case "skip":
					if !strings.Contains(message, id) || !strings.Contains(message, reason) {
						t.Fatalf("canonical skip message was redacted: %q", message)
					}
				default:
					if spec.Action != "create" || message != "" {
						t.Fatalf("dedup fallback semantics changed: spec=%+v message=%q", spec, message)
					}
				}
				assertActivityLogContentVisibility(t, output.String(), flag == "1", secrets...)
			})
		}
	}
}
