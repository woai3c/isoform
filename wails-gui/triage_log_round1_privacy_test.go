package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTriageDiagnosticStorageAndEventsPrivacy(t *testing.T) {
	for _, source := range []string{"activity", "knowledge", "memory"} {
		for _, flag := range []string{"unset", "0", "true", "1"} {
			t.Run(source+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, path := isolateActivityPrivacyLogs(t)
				app := &App{ctx: context.Background(), eventBroadcaster: NewEventBroadcaster()}
				events := app.eventBroadcaster.AddClient("privacy-round-one")
				defer app.eventBroadcaster.RemoveClient("privacy-round-one")
				setTriageApp(app)
				const title = "private-round-one-title界"
				const description = "private-round-one-description"
				const reason = "private-round-one-model-reason"
				worth := true
				logTriageEntry(TriageLogEntry{
					Source: source, ID: "stable-id", Decision: "saved", Title: title,
					Description: description, Reason: reason, WorthMemory: &worth,
				})
				disk, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				assertActivityLogContentVisibility(t, string(disk), false, title, description, reason)
				var persisted TriageLogEntry
				if err := json.Unmarshal(disk, &persisted); err != nil {
					t.Fatal(err)
				}
				if persisted.TitleBytes == nil || *persisted.TitleBytes != len(title) || persisted.DescriptionBytes == nil || *persisted.DescriptionBytes != len(description) || persisted.ReasonBytes == nil || *persisted.ReasonBytes != len(reason) || persisted.Reason != fmt.Sprintf("reason_bytes=%d", len(reason)) || persisted.Decision != "saved" || persisted.WorthMemory == nil || !*persisted.WorthMemory {
					t.Fatalf("disk lost decision metadata: %+v", persisted)
				}
				var event struct {
					Event string            `json:"event"`
					Args  []json.RawMessage `json:"args"`
				}
				select {
				case payload := <-events:
					if err := json.Unmarshal(payload, &event); err != nil || event.Event != "triage:log" || len(event.Args) != 1 {
						t.Fatalf("live diagnostic missing: %s error=%v", payload, err)
					}
				default:
					t.Fatal("no live triage event")
				}
				assertActivityLogContentVisibility(t, string(event.Args[0]), flag == "1" && source == "activity", title, description, reason)
				assertActivityLogContentVisibility(t, output.String(), false, reason)
				closeTriageLog()
				entries := app.GetTriageLogs()
				if len(entries) != 1 || entries[0].Reason != persisted.Reason || entries[0].Title != "" || entries[0].Description != "" {
					t.Fatalf("reload changed metadata or restored private text: %+v", entries)
				}
			})
		}
	}
}

func TestTriageLegacyHistoryPrivacy(t *testing.T) {
	for _, flag := range []string{"0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			_, path := isolateActivityPrivacyLogs(t)
			closeTriageLog()
			const secret = "private-legacy-diagnostic"
			entry := TriageLogEntry{Timestamp: "2026-10-22 10:00:00.000", Source: "knowledge", Decision: "saved", Title: secret, Description: secret, Reason: secret}
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			entries := (&App{}).GetTriageLogs()
			raw, err = json.Marshal(entries)
			if err != nil || len(entries) != 1 || strings.Contains(string(raw), secret) {
				t.Fatalf("history reader exposed retained source text: %s error=%v", raw, err)
			}
		})
	}
}

func TestActivityPipelineOutcomePrivacy(t *testing.T) {
	for _, kind := range []string{"knowledge", "memory"} {
		for _, operation := range []string{"skip", "generation_error", "save_error"} {
			for _, flag := range []string{"unset", "0", "1"} {
				t.Run(kind+"/"+operation+"/debug="+flag, func(t *testing.T) {
					setContentLogDebugFlag(t, flag)
					isolateActivityPrivacyLogs(t)
					const secret = "private-run-row-title-and-error"
					response := `{"action":"skip","title":"` + secret + `","memoryPoints":["a point"],"knowledgePoints":["a point"]}`
					if operation == "generation_error" {
						response = `{"title":"` + secret + `"`
					}
					if operation == "save_error" {
						response = strings.Replace(response, `"skip"`, `"create"`, 1)
						// Errors include the private filename, but the run outcome must not.
						t.Setenv("YANSU_AGENT_HOME", os.Getenv("HOME")+"/"+secret)
						if err := os.WriteFile(os.Getenv("YANSU_AGENT_HOME"), []byte("blocked directory"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{response: response})
					app.activity = &activityMonitorState{store: newTestStore(t)}
					group := sessionGroup{Sessions: []ActivitySessionRow{{ID: "outcome-source", StartTime: 1000, EndTime: 2000, AppNames: `[]`}}}
					if err := app.activity.store.SaveSession(group.Sessions[0], nil, nil); err != nil {
						t.Fatal(err)
					}
					id, err := app.activity.store.SaveActivityPipelineRun(ActivityPipelineRunRow{Kind: "group", StartedAt: time.Now().UnixMilli(), GroupID: group.primaryID()})
					if err != nil {
						t.Fatal(err)
					}
					triager := NewActivityTriager(app, nil)
					switch kind {
					case "knowledge":
						triager.generateAndSaveKnowledge(t.Context(), id, group, "summary", SessionContentProfile{})
					case "memory":
						triager.generateAndSaveMemory(t.Context(), id, group, "summary", SessionContentProfile{})
					}
					row, err := app.activity.store.GetActivityPipelineRunByID(id)
					if err != nil {
						t.Fatal(err)
					}
					status, reason := row.MemoryStatus, row.MemoryReason
					if kind == "knowledge" {
						status, reason = row.KnowledgeStatus, row.KnowledgeReason
					}
					if strings.Contains(reason, secret) {
						t.Fatalf("run outcome persisted private content: %s", reason)
					}
					if operation == "skip" {
						if status != "skipped" || !strings.Contains(reason, fmt.Sprintf("title_bytes=%d", len(secret))) {
							t.Fatalf("dedup outcome changed: status=%s reason=%s", status, reason)
						}
					} else if status != "error" || !strings.Contains(reason, "error_kind=") || !strings.Contains(reason, "error_bytes=") {
						t.Fatalf("failure lost safe outcome metadata: status=%s reason=%s", status, reason)
					}
				})
			}
		}
	}
}
