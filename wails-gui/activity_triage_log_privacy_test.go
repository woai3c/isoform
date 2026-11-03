package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"wails-gui/internal/platform/activitymon"
)

func isolateActivityPrivacyLogs(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "w4p-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(home, configDirName))
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	triage.mu.Lock()
	previousFile, previousApp := triage.file, triage.app
	triage.file, triage.app = nil, nil
	triage.mu.Unlock()
	t.Cleanup(func() {
		triage.mu.Lock()
		defer triage.mu.Unlock()
		if triage.file != nil {
			_ = triage.file.Close()
		}
		triage.file, triage.app = previousFile, previousApp
	})
	initTriageLog()
	return &output, filepath.Join(home, configDirName, triageLogName)
}

func assertActivityLogContentVisibility(t *testing.T, logs string, debug bool, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(logs, secret) != debug {
			t.Errorf("debug=%t: unexpected content visibility for %q in logs: %s", debug, secret, logs)
		}
	}
}

func TestSpeechMemoryLogsKeepTitlesAndPathsPrivate(t *testing.T) {
	for _, flag := range []string{"", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			if flag == "" {
				if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
					t.Fatal(err)
				}
			}
			output, _ := isolateActivityPrivacyLogs(t)
			group := sessionGroup{Sessions: []ActivitySessionRow{{ID: "speech-log", TriggerKind: "speech_idle_transition"}}}
			const title = "private-title-amber"
			const path = "/private/private-file-meadow.md"
			logSpeechMemoryTriage(group, "speech-log", title)
			logSpeechMemorySaved(group, "speech-log", "created", title, path, 2)
			if !strings.Contains(output.String(), "memory triage accepted") || !strings.Contains(output.String(), "points=2") {
				t.Fatalf("missing operational metadata: %s", output.String())
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", title, path)
		})
	}
}

func TestActivityTriageLogsKeepMixedSpeechContentPrivate(t *testing.T) {
	for _, flag := range []string{"", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			if flag == "" {
				if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
					t.Fatal(err)
				}
			}
			output, triagePath := isolateActivityPrivacyLogs(t)
			const title = "private-triage-amber"
			const description = "private-description-meadow"
			raw, err := json.Marshal(activityTriageResponse{Title: title, Description: description})
			if err != nil {
				t.Fatal(err)
			}
			runner := &activityMarkdownTranslationFakeRunner{response: string(raw)}
			app := newActivityTranslationTestApp(t, runner)
			app.activity = &activityMonitorState{store: newTestStore(t)}
			for _, key := range []string{triageEnableKnowledgeKey, triageEnableShadowKey, triageEnableAutomationKey, triageEnableHandoffKey, triageEnableSOPKey} {
				if err := app.SaveUIState(key, "false"); err != nil {
					t.Fatal(err)
				}
			}
			group := sessionGroup{Sessions: []ActivitySessionRow{
				{ID: "mixed-speech", TriggerKind: "speech_idle_transition", StartTime: 1000, EndTime: 2000, AppNames: `["Mail"]`, EventCount: 1},
				{ID: "mixed-screen", TriggerKind: "idle_transition", StartTime: 2000, EndTime: 3000, AppNames: `["Code"]`, EventCount: 1},
			}}
			for _, session := range group.Sessions {
				if err := app.activity.store.SaveSession(session, []activitymon.ActivityEvent{{Timestamp: session.StartTime, Kind: activitymon.EventSpeech, SpeechText: "test speech"}}, nil); err != nil {
					t.Fatal(err)
				}
			}
			triager := NewActivityTriager(app, nil)
			triager.triageGroup(context.Background(), group)
			triager.wg.Wait()
			if !strings.Contains(output.String(), "decision=triage") {
				t.Fatalf("triage decision was not logged: %s", output.String())
			}
			diskLog, err := os.ReadFile(triagePath)
			if err != nil {
				t.Fatal(err)
			}
			assertActivityLogContentVisibility(t, output.String(), false, title, description)
			// Disk history never retains source text, even with debug enabled.
			assertActivityLogContentVisibility(t, string(diskLog), false, title, description)
			entries := app.GetTriageLogs()
			found := false
			for _, entry := range entries {
				if entry.ID == group.primaryID() && entry.Decision == "triage" {
					found = (entry.Title == "" || entry.Title == title) && entry.Description == "" && entry.TitleBytes != nil && *entry.TitleBytes == len(title) && entry.DescriptionBytes != nil && *entry.DescriptionBytes == len(description)
				}
			}
			if !found {
				t.Fatal("accepted triage lost the Learning decisions title or description")
			}
			runs, err := app.activity.store.ListActivityPipelineRuns(10)
			if err != nil || len(runs) != 1 {
				t.Fatalf("expected one canonical pipeline run: runs=%+v error=%v", runs, err)
			}
			run, err := app.activity.store.GetActivityPipelineRunByID(runs[0].ID)
			if err != nil || run.TriageStatus != "dismissed" || run.TriageReason != "triage complete" {
				t.Fatalf("privacy logging changed completion: run=%+v error=%v", run, err)
			}
			result, rawResponse, _, _, err := triager.runActivityTriage(context.Background(), "test summary", "")
			if err != nil || result.Title != title || result.Description != description || rawResponse != string(raw) {
				t.Fatalf("privacy logging changed canonical model output: result=%+v response=%q error=%v", result, rawResponse, err)
			}
		})
	}
}

func TestActivityKnowledgeDedupLogKeepsTitlePrivate(t *testing.T) {
	for _, flag := range []string{"", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			if flag == "" {
				if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
					t.Fatal(err)
				}
			}
			output, triagePath := isolateActivityPrivacyLogs(t)
			const title = "private-knowledge-amber"
			runner := &activityMarkdownTranslationFakeRunner{response: `{"action":"skip","title":"` + title + `","knowledgePoints":[]}`}
			app := newActivityTranslationTestApp(t, runner)
			app.activity = &activityMonitorState{store: newTestStore(t)}
			triager := NewActivityTriager(app, nil)
			session := ActivitySessionRow{ID: "knowledge-log", StartTime: 1000, EndTime: 2000, AppNames: `[]`}
			if err := app.activity.store.SaveSession(session, nil, nil); err != nil {
				t.Fatal(err)
			}
			triager.generateAndSaveKnowledge(context.Background(), 0, sessionGroup{Sessions: []ActivitySessionRow{session}}, "test summary", SessionContentProfile{})
			if !strings.Contains(output.String(), "decision=dedup_skip") {
				t.Fatalf("dedup decision was not logged: %s", output.String())
			}
			diskLog, err := os.ReadFile(triagePath)
			if err != nil {
				t.Fatal(err)
			}
			assertActivityLogContentVisibility(t, output.String(), false, title)
			assertActivityLogContentVisibility(t, string(diskLog), false, title)
			assertActivityHistoryMetadata(t, app, title, "", "")
		})
	}
}

// History retains structured counts without persisting source text.
func assertActivityHistoryMetadata(t *testing.T, app *App, title, description, decision string) {
	t.Helper()
	for _, entry := range app.GetTriageLogs() {
		if (entry.Title == "" || entry.Title == title) && entry.Description == "" && entry.TitleBytes != nil && *entry.TitleBytes == len(title) && entry.DescriptionBytes != nil && *entry.DescriptionBytes == len(description) && (decision == "" || entry.Decision == decision) {
			return
		}
	}
	t.Fatalf("private history lost structured title/description for %s", decision)
}

func TestActivitySavedArtifactsKeepTitlesPrivate(t *testing.T) {
	for _, kind := range []string{"memory", "knowledge"} {
		for _, action := range []string{"create", "skip", "update", "update_without_id"} {
			for _, flag := range []string{"", "0", "1"} {
				t.Run(kind+"/"+action+"/debug="+flag, func(t *testing.T) {
					t.Setenv(backgroundTranscriptDebugEnv, flag)
					if flag == "" {
						if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
							t.Fatal(err)
						}
					}
					output, triagePath := isolateActivityPrivacyLogs(t)
					const title = "private-saved-title-amber"
					description := ""
					if kind == "knowledge" {
						description = "private-knowledge-description"
					}
					modelAction, updateID, decision := action, "", "saved"
					switch action {
					case "skip":
						decision = "dedup_skip"
					case "update":
						updateID, decision = "missing-entry", "dedup_update"
					case "update_without_id":
						modelAction = "update"
					}
					raw, err := json.Marshal(map[string]any{
						"action": modelAction, "updateId": updateID, "title": title, "description": description,
						"memoryPoints": []string{"Remember the source"}, "knowledgePoints": []string{"Remember the source"},
					})
					if err != nil {
						t.Fatal(err)
					}
					runner := &activityMarkdownTranslationFakeRunner{responses: []string{string(raw), "# " + title + "\n\n## Memory Points\n\n- Remember the source\n"}}
					app := newActivityTranslationTestApp(t, runner)
					app.activity = &activityMonitorState{store: newTestStore(t)}
					app.ctx, app.eventBroadcaster = context.Background(), NewEventBroadcaster()
					events := app.eventBroadcaster.AddClient("saved-artifact-panel")
					defer app.eventBroadcaster.RemoveClient("saved-artifact-panel")
					setTriageApp(app)
					triager := NewActivityTriager(app, nil)
					group := sessionGroup{Sessions: []ActivitySessionRow{{ID: "saved-log", TriggerKind: "speech_idle_transition", StartTime: 1000, EndTime: 2000, AppNames: `[]`}}}
					if err := app.activity.store.SaveSession(group.Sessions[0], nil, nil); err != nil {
						t.Fatal(err)
					}
					entryID := activityMemoryEntryID("saved-log")
					switch kind {
					case "memory":
						triager.generateAndSaveMemory(context.Background(), 0, group, "test summary", SessionContentProfile{})
					case "knowledge":
						triager.generateAndSaveKnowledge(context.Background(), 0, group, "test summary", SessionContentProfile{})
						entryID = activityKnowledgeEntryID("saved-log")
					}
					if !strings.Contains(output.String(), "decision="+decision) {
						t.Fatalf("decision missing: %s", output.String())
					}
					if action != "skip" {
						artifact, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), configDirName, kind, entryID+".md"))
						if err != nil || !strings.Contains(string(artifact), title) || !strings.Contains(string(artifact), "Remember the source") {
							t.Fatalf("persisted content changed: content=%s error=%v", artifact, err)
						}
					}
					diskLog, err := os.ReadFile(triagePath)
					if err != nil {
						t.Fatal(err)
					}
					assertActivityLogContentVisibility(t, contentLogLine(t, output.String(), "[triage] "+kind+" id=saved-log decision="+decision), false, title)
					if description != "" && strings.Contains(output.String(), description) {
						t.Fatal("structured description leaked to app log")
					}
					assertActivityLogContentVisibility(t, string(diskLog), false, title)
					assertActivityHistoryMetadata(t, app, title, description, decision)
					found := false
					for len(events) > 0 {
						var event struct {
							Event string            `json:"event"`
							Args  []json.RawMessage `json:"args"`
						}
						if err := json.Unmarshal(<-events, &event); err != nil {
							t.Fatal(err)
						}
						if event.Event != "triage:log" {
							continue
						}
						var entry TriageLogEntry
						if err := json.Unmarshal(event.Args[0], &entry); err != nil {
							t.Fatal(err)
						}
						if entry.Decision == decision {
							found = entry.TitleBytes != nil && *entry.TitleBytes == len(title) && entry.DescriptionBytes != nil && *entry.DescriptionBytes == len(description)
							// Invalid memory update IDs create a withheld fallback; its title
							// stays neutral even though the owner can open the artifact.
							showTitle := action != "skip" && !(kind == "memory" && action == "update")
							assertActivityLogContentVisibility(t, string(event.Args[0]), showTitle, title)
							if description != "" {
								assertActivityLogContentVisibility(t, string(event.Args[0]), false, description)
							}
						}
					}
					if !found {
						t.Fatal("live panel event lost structured artifact details")
					}
				})
			}
		}
	}
}

func TestMeetingCommitmentFailureLogKeepsTranscriptPrivate(t *testing.T) {
	for _, flag := range []string{"", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			if flag == "" {
				if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
					t.Fatal(err)
				}
			}
			output, _ := isolateActivityPrivacyLogs(t)
			store := newTestStore(t)
			app := &App{activity: &activityMonitorState{store: store}}
			if err := store.db.Close(); err != nil {
				t.Fatal(err)
			}
			const text = "private-followup-amber"
			app.writeMeetingCommitments(ActivitySessionRow{ID: "followup-log", EndTime: time.Now().Add(-time.Second).UnixMilli()}, "meeting", "transcript", meetingFollowupExtraction{Followups: []meetingFollowup{{Text: text, Owner: meetingFollowupOwnerMe}}})
			if !strings.Contains(output.String(), "[meeting-followup]") || !strings.Contains(output.String(), "error_bytes=") {
				t.Fatalf("commitment failure diagnostics missing: %s", output.String())
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", text)
		})
	}
}

func TestActivityMalformedModelResponseLogsKeepContentPrivate(t *testing.T) {
	for _, stage := range []string{"triage", "memory", "knowledge"} {
		for _, flag := range []string{"", "0", "1"} {
			t.Run(stage+"/debug="+flag, func(t *testing.T) {
				t.Setenv(backgroundTranscriptDebugEnv, flag)
				if flag == "" {
					if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
						t.Fatal(err)
					}
				}
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-model-error-amber"
				const raw = `{"title":"` + secret + `"`
				runner := &activityMarkdownTranslationFakeRunner{response: raw}
				app := newActivityTranslationTestApp(t, runner)
				app.activity = &activityMonitorState{store: newTestStore(t)}
				group := sessionGroup{Sessions: []ActivitySessionRow{{ID: "malformed-log", TriggerKind: "speech_idle_transition", StartTime: 1000, EndTime: 2000, AppNames: `[]`, EventCount: 1}}}
				triager := NewActivityTriager(app, nil)
				session := group.Sessions[0]
				if err := app.activity.store.SaveSession(session, []activitymon.ActivityEvent{{Timestamp: session.StartTime, Kind: activitymon.EventSpeech, SpeechText: "test speech"}}, nil); err != nil {
					t.Fatal(err)
				}
				switch stage {
				case "triage":
					triager.triageGroup(context.Background(), group)
					triager.wg.Wait()
					runs, err := app.activity.store.ListActivityPipelineRuns(10)
					if err != nil || len(runs) != 1 {
						t.Fatalf("expected one canonical pipeline run: runs=%+v error=%v", runs, err)
					}
					run, err := app.activity.store.GetActivityPipelineRunByID(runs[0].ID)
					if err != nil || strings.Contains(run.Error, secret) || !strings.Contains(run.Error, "error_kind=parse error_bytes=") {
						t.Fatalf("run error must retain safe metadata without the model secret: run=%+v error=%v", run, err)
					}
					_, rawResponse, _, _, err := triager.runActivityTriage(context.Background(), "test summary", "")
					if err == nil || rawResponse != raw || !strings.Contains(err.Error(), secret) {
						t.Fatalf("privacy logging changed returned error evidence: response=%q error=%v", rawResponse, err)
					}
				case "memory":
					triager.generateAndSaveMemory(context.Background(), 0, group, "test summary", SessionContentProfile{})
				case "knowledge":
					triager.generateAndSaveKnowledge(context.Background(), 0, group, "test summary", SessionContentProfile{})
				}
				if !strings.Contains(output.String(), "failed for group malformed-log") {
					t.Fatalf("model failure diagnostics missing: %s", output.String())
				}
				assertActivityLogContentVisibility(t, output.String(), flag == "1", secret)
			})
		}
	}
}

func TestSpeechMemoryDebugLoggingRequiresExactFlagAndBoundsText(t *testing.T) {
	const prefix = "private-debug-prefix\n"
	const tail = "private-debug-tail"
	title := prefix + strings.Repeat("界", 100) + tail
	for _, flag := range []string{"", "0", "true", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			if flag == "" {
				if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
					t.Fatal(err)
				}
			}
			output, _ := isolateActivityPrivacyLogs(t)
			group := sessionGroup{Sessions: []ActivitySessionRow{{ID: "bounded-log", TriggerKind: "meeting_flush"}}}
			logSpeechMemoryTriage(group, "bounded-log", title)
			logs := output.String()
			if !strings.Contains(logs, fmt.Sprintf("title_bytes=%d", len(title))) || strings.Count(logs, "\n") != 1 {
				t.Fatalf("missing size or unquoted multiline output: %s", logs)
			}
			if flag != "1" {
				if strings.Contains(logs, "private-debug") || strings.Contains(logs, " title=") {
					t.Fatalf("non-explicit opt-in exposed text: %s", logs)
				}
				return
			}
			_, quoted, found := strings.Cut(logs, " title=")
			if !found {
				t.Fatalf("explicit opt-in omitted debug field: %s", logs)
			}
			decoded, err := strconv.Unquote(strings.TrimSpace(quoted))
			if err != nil || !strings.HasPrefix(decoded, prefix) || !utf8.ValidString(decoded) {
				t.Fatalf("debug field is not valid quoted UTF-8: value=%q error=%v", decoded, err)
			}
			if len(decoded) > 256+len("...(truncated)") || !strings.HasSuffix(decoded, "...(truncated)") || strings.Contains(decoded, tail) {
				t.Fatalf("debug field is not bounded: bytes=%d value=%q", len(decoded), decoded)
			}
		})
	}
}
