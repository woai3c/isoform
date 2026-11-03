package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"yansu-agent/agent"
)

func TestMemoryGeneratorTriageReasonsKeepContentPrivate(t *testing.T) {
	for _, worth := range []bool{false, true} {
		decision := "not_worth"
		if worth {
			decision = "worth"
		}
		for _, flag := range []string{"unset", "0", "1"} {
			t.Run(decision+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-model-reason-cedar"
				reason := secret + strings.Repeat("x", 300) + "private-tail-cedar"
				raw, err := json.Marshal(memoryTriageResponse{Worth: worth, Reason: reason})
				if err != nil {
					t.Fatal(err)
				}
				runner := &activityMarkdownTranslationFakeRunner{responses: []string{string(raw), `{"memoryPoints":[]}`}}
				app := newActivityTranslationTestApp(t, runner)
				NewMemoryGenerator(app).runMemoryPipeline(context.Background(), memorySource("wechat"), "conversation-id", "[SELF] a durable preference", "")
				for _, prefix := range []string{"[memory] triage:", "[triage] memory id=conversation-id decision=" + decision} {
					line := contentLogLine(t, output.String(), prefix)
					assertActivityLogContentVisibility(t, line, flag == "1" && !strings.HasPrefix(prefix, "[triage]"), secret)
					if !strings.Contains(line, "reason_bytes=") || strings.Contains(line, "private-tail-cedar") {
						t.Errorf("reason metadata or bounded preview incorrect: %s", line)
					}
				}
			})
		}
	}
}

func TestMemoryGeneratorFailureLogsKeepModelContentPrivate(t *testing.T) {
	for _, stage := range []string{"triage", "generation"} {
		for _, failure := range []string{"parse", "timeout"} {
			for _, flag := range []string{"unset", "0", "1"} {
				t.Run(stage+"/"+failure+"/debug="+flag, func(t *testing.T) {
					setContentLogDebugFlag(t, flag)
					output, _ := isolateActivityPrivacyLogs(t)
					const secret = "private-model-failure-cedar"
					responses := []string{`{"title":"` + secret + strings.Repeat("x", 300) + `private-response-tail"`}
					if stage == "generation" {
						responses = append([]string{`{"worth":true}`}, responses...)
					}
					runner := &activityMarkdownTranslationFakeRunner{responses: responses}
					app := newActivityTranslationTestApp(t, runner)
					if failure == "timeout" {
						failAt := 1
						if stage == "generation" {
							failAt = 2
						}
						app.runner = &memoryLogPrivacyRunner{failAt: failAt, failure: errors.New("provider timeout: " + secret + strings.Repeat("x", 300) + "private-response-tail")}
					}
					result := NewMemoryGenerator(app).runMemoryPipeline(context.Background(), memorySource("wechat"), "conversation-id", "[SELF] a durable preference", "")
					if result != memPipelineFailed {
						t.Fatalf("result = %v, want failed", result)
					}
					prefixes := []string{"[memory] " + stage + " failed"}
					if stage == "triage" {
						prefixes = append(prefixes, "[triage] memory id=conversation-id decision=error")
					}
					for _, prefix := range prefixes {
						line := contentLogLine(t, output.String(), prefix)
						assertActivityLogContentVisibility(t, line, flag == "1" && !strings.HasPrefix(prefix, "[triage]"), secret)
						if !strings.Contains(line, "error_kind="+failure) || !strings.Contains(line, "error_bytes=") || strings.Contains(line, "private-response-tail") {
							t.Errorf("error metadata or bounded preview incorrect: %s", line)
						}
					}
				})
			}
		}
	}
}

type memoryLogPrivacyRunner struct {
	activityMarkdownTranslationFakeRunner
	failAt  int
	failure error
	stream  string
}

func (r *memoryLogPrivacyRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	if r.failure != nil && r.calls+1 == r.failAt {
		r.calls++
		return "", r.failure
	}
	if r.stream != "" {
		if err := callback(&agent.StreamEvent{Type: "error", Error: r.stream}); err != nil {
			return "", err
		}
	}
	if len(r.responses) == 0 {
		r.response = `{"worth":true,"memoryPoints":["a durable preference"]}`
	}
	return r.activityMarkdownTranslationFakeRunner.ExecuteTask(ctx, input, callback)
}

func TestMemoryGeneratorStreamLogsKeepContentPrivate(t *testing.T) {
	for _, stage := range []string{"triage", "generation", "memory point rewrite"} {
		for _, flag := range []string{"unset", "0", "1"} {
			t.Run(stage+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-stream-failure-cedar"
				app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{})
				app.runner = &memoryLogPrivacyRunner{stream: "provider timeout: " + secret}
				mc := NewMemoryGenerator(app)
				var err error
				switch stage {
				case "triage":
					_, err = mc.runTriage(context.Background(), memorySourceConversation, "source", "")
				case "generation":
					_, err = mc.runGeneration(context.Background(), memorySourceConversation, "conversation-id", "source", "")
				case "memory point rewrite":
					_, err = app.rewriteMemoryPointsFirstPerson(context.Background(), []string{"old point"}, []string{"new point"}, "user", "")
				}
				if err != nil {
					t.Fatal(err)
				}
				line := contentLogLine(t, output.String(), "[memory] "+stage+" stream error:")
				assertActivityLogContentVisibility(t, line, flag == "1", secret)
				if !strings.Contains(line, "error_kind=timeout error_bytes=") {
					t.Errorf("missing safe error diagnostic: %s", line)
				}
			})
		}
	}
}

func TestMemoryGeneratorArtifactHistoryKeepsTitleMetadata(t *testing.T) {
	for _, action := range []string{"skip", "update", "update_without_id", "create"} {
		for _, flag := range []string{"unset", "0", "1"} {
			t.Run(action+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, triagePath := isolateActivityPrivacyLogs(t)
				const title = "private-memory-title-cedar"
				mem := mcMemoryGenerationResponse{Action: action, Title: title, MemoryPoints: []string{"I prefer unit tests"}, Dimension: "topic_highlight"}
				decision := "saved"
				switch action {
				case "skip":
					decision = "dedup_skip"
				case "update":
					decision = "dedup_update"
					mem.UpdateID = "private-generated-update-id-cedar"
					dir, err := memoryDir()
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, mem.UpdateID+".md"), []byte("# Previous title\n"), 0600); err != nil {
						t.Fatal(err)
					}
				case "update_without_id":
					mem.Action = "update"
				}
				raw, err := json.Marshal(mem)
				if err != nil {
					t.Fatal(err)
				}
				runner := &activityMarkdownTranslationFakeRunner{responses: []string{`{"worth":true}`, string(raw)}}
				app := newActivityTranslationTestApp(t, runner)
				if err := app.SaveUIState("ui.language", "en"); err != nil {
					t.Fatal(err)
				}
				result := NewMemoryGenerator(app).runMemoryPipelineDetailed(context.Background(), memorySource("wechat"), "conversation-id", "[SELF] I prefer unit tests", "")
				line := contentLogLine(t, output.String(), "[triage] memory id=conversation-id decision="+decision)
				assertActivityLogContentVisibility(t, line, false, title)
				if mem.UpdateID != "" {
					assertActivityLogContentVisibility(t, line, false, mem.UpdateID)
				}
				if !strings.Contains(line, "title_bytes=") {
					t.Errorf("title size missing: %s", line)
				}
				disk, err := os.ReadFile(triagePath)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(disk), title) {
					t.Fatal("disk triage history contains generated title")
				}
				found := false
				for _, entry := range app.GetTriageLogs() {
					if entry.ID == "conversation-id" && entry.Decision == decision {
						// This fixture is an unverified connector source;
						// current provenance withholds its title from panel reads.
						found = entry.Title == "" && entry.TitleBytes != nil && *entry.TitleBytes == len(title)
					}
				}
				if !found {
					t.Fatal("Learning decisions lost structured generated title")
				}
				if action != "skip" && (result.Result != memPipelineSuccess || len(result.Materializations) != 1 || result.Materializations[0].Title != title) {
					t.Fatalf("canonical materialization changed: %+v", result)
				}
			})
		}
	}
}

func TestMemoryGeneratorCorrectionLogsKeepContentPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, triagePath := isolateActivityPrivacyLogs(t)
			const title = "private corrected amber workspace"
			const reason = "private correction reason cedar"
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(MemoryRejectionEntry{MemoryID: "rejected-id", Title: title, Reason: reason})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, memoryRejectionsLogName), raw, 0600); err != nil {
				t.Fatal(err)
			}
			runner := &activityMarkdownTranslationFakeRunner{responses: []string{`{"worth":true}`, `{"title":"` + title + `","memoryPoints":["I use a workspace"]}`}}
			app := newActivityTranslationTestApp(t, runner)
			if result := NewMemoryGenerator(app).runMemoryPipeline(context.Background(), memorySource("wechat"), "conversation-id", "[SELF] I use a workspace", ""); result != memPipelineSkipped {
				t.Fatalf("correction did not skip: %v", result)
			}
			assertMemoryDecisionMetadata(t, app, triagePath, "correction_match_skip", title)
			for _, prefix := range []string{"[memory] correction-match skip", "[triage] memory id=conversation-id decision=correction_match_skip"} {
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, flag == "1" && !strings.HasPrefix(prefix, "[triage]"), title, reason)
				if !strings.Contains(line, "reason_bytes=") {
					t.Errorf("reason size missing: %s", line)
				}
			}
		})
	}
}

func TestMemoryGeneratorConnectorFailureLogsKeepPathsPrivate(t *testing.T) {
	for _, connector := range []string{"slack", "discord", "feishu", "telegram"} {
		for _, failure := range []string{"read", "parse"} {
			for _, flag := range []string{"unset", "0", "1"} {
				t.Run(connector+"/"+failure+"/debug="+flag, func(t *testing.T) {
					setContentLogDebugFlag(t, flag)
					output, _ := isolateActivityPrivacyLogs(t)
					const secret = "private-scan-path-cedar"
					scanRoot := filepath.Join(os.Getenv("HOME"), secret)
					t.Setenv("YANSU_AGENT_HOME", scanRoot)
					scanDir := filepath.Join(scanRoot, connector+"-scan")
					if err := os.MkdirAll(filepath.Join(scanDir, "conversations"), 0700); err != nil {
						t.Fatal(err)
					}
					if failure == "parse" {
						if err := os.WriteFile(filepath.Join(scanDir, "conversations", "conversation-id.json"), []byte(`{"messages":`), 0600); err != nil {
							t.Fatal(err)
						}
					}
					mc := NewMemoryGenerator(&App{})
					var err error
					var failed []string
					prefix := ""
					switch connector {
					case "slack":
						_, failed, err = mc.evaluateSlackNow(context.Background(), []string{"conversation-id"})
						prefix = "evaluateSlackNow"
					case "discord":
						_, _, failed, err = mc.evaluateDiscordConversations(context.Background(), []string{"conversation-id"})
						prefix = "evaluateDiscordConversations"
					case "feishu":
						_, failed, err = mc.evaluateFeishuNow(context.Background(), scanDir, []string{"conversation-id"})
						prefix = "evaluateFeishuNow"
					case "telegram":
						_, failed, err = mc.evaluateTelegramNow(context.Background(), scanDir, []string{"conversation-id"})
						prefix = "evaluateTelegramNow"
					}
					if err != nil || len(failed) != 1 || failed[0] != "conversation-id" {
						t.Fatalf("retry tracking changed: failed=%v error=%v", failed, err)
					}
					line := contentLogLine(t, output.String(), "[memory] "+prefix+": "+failure)
					preview := secret
					if failure == "parse" {
						preview = "unexpected end of JSON input"
					}
					assertActivityLogContentVisibility(t, line, flag == "1", preview)
					if !strings.Contains(line, "error_bytes=") {
						t.Errorf("missing safe error metadata: %s", line)
					}
				})
			}
		}
	}
}

func TestMemoryGeneratorCorrectionReadErrorsKeepPathsPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const secret = "private-corrections-path-cedar"
			t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), secret))
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, memoryRejectionsLogName), 0700); err != nil {
				t.Fatal(err)
			}
			runner := &activityMarkdownTranslationFakeRunner{responses: []string{`{"worth":true}`, `{"action":"skip","memoryPoints":["one point"]}`}}
			app := newActivityTranslationTestApp(t, runner)
			NewMemoryGenerator(app).runMemoryPipeline(context.Background(), memorySourceConversation, "conversation-id", "source", "")
			for _, prefix := range []string{"[memory] load corrections for prompt failed", "[memory] load corrections for chat-source gate failed"} {
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, flag == "1" && !strings.HasPrefix(prefix, "[triage]"), secret)
				if !strings.Contains(line, "error_bytes=") {
					t.Errorf("missing safe error metadata: %s", line)
				}
			}
		})
	}
}

func TestMemoryGeneratorFileLogsKeepPathsAndModelIDsPrivate(t *testing.T) {
	for _, operation := range []string{"create", "update", "invalid_id", "missing_id", "read_error", "snapshot_error", "write_error", "update_write_error"} {
		for _, flag := range []string{"unset", "0", "1"} {
			t.Run(operation+"/debug="+flag, func(t *testing.T) {
				if (operation == "write_error" || operation == "update_write_error") && (os.Geteuid() == 0 || runtime.GOOS == "windows") {
					t.Skip("chmod denial requires an unprivileged Unix process")
				}
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-file-cedar"
				t.Setenv("YANSU_AGENT_HOME", filepath.Join(os.Getenv("HOME"), secret))
				app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{})
				if err := app.SaveUIState("ui.language", "en"); err != nil {
					t.Fatal(err)
				}
				mc := NewMemoryGenerator(app)
				dir, err := memoryDir()
				if err != nil {
					t.Fatal(err)
				}
				id := "private-model-id-cedar"
				path := filepath.Join(dir, id+".md")
				mem := &mcMemoryGenerationResponse{Title: "A memory", Dimension: "topic_highlight", MemoryPoints: []string{"I prefer unit tests"}}
				prefix := ""
				preview := secret
				expectError := false
				switch operation {
				case "create":
					prefix = "[memory] saved memory to"
				case "update":
					prefix = "[memory] updated existing memory at"
				case "invalid_id":
					prefix = "[memory] invalid updateID"
					id = "../private-model-id-cedar"
					preview = "private-model-id-cedar"
				case "missing_id":
					prefix = "[memory] updateID"
					preview = id
				case "read_error":
					prefix = "[memory] failed to read existing memory"
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "snapshot_error":
					prefix = "[memory] failed to snapshot"
					expectError = true
					if err := os.WriteFile(filepath.Join(dir, memoryHistoryDirName), []byte("not a directory"), 0600); err != nil {
						t.Fatal(err)
					}
				case "write_error":
					prefix = "[memory] failed to write memory file"
					expectError = true
					if err := os.Chmod(dir, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
				case "update_write_error":
					prefix = "[memory] failed to update memory file"
					expectError = true
				}
				switch operation {
				case "update", "snapshot_error", "update_write_error":
					if err := os.WriteFile(path, []byte("# Previous title\n"), 0600); err != nil {
						t.Fatal(err)
					}
					if operation == "update_write_error" {
						if err := os.Chmod(path, 0400); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = os.Chmod(path, 0600) })
					}
				}
				switch operation {
				case "create", "write_error":
					_, err = mc.saveMemoryFile(memorySourceConversation, "conversation-id", mem)
				default:
					_, _, err = mc.updateMemoryFileDetailed(context.Background(), id, memorySourceConversation, "conversation-id", mem, "")
				}
				if (err != nil) != expectError {
					t.Fatalf("error=%v, want error=%t", err, expectError)
				}
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, flag == "1", preview)
				if !strings.Contains(line, "_bytes=") {
					t.Errorf("missing private field metadata: %s", line)
				}
			})
		}
	}
}

func TestMemoryGeneratorDirectoryErrorsKeepDetailsPrivate(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, flag := range []string{"unset", "0", "1"} {
			t.Run(operation+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				mc := NewMemoryGenerator(&App{})
				t.Setenv("YANSU_AGENT_HOME", "")
				t.Setenv("HOME", "")
				mem := &mcMemoryGenerationResponse{Title: "A memory", MemoryPoints: []string{"A preference"}}
				var err error
				switch operation {
				case "create":
					_, err = mc.saveMemoryFile(memorySourceConversation, "conversation-id", mem)
				case "update":
					_, _, err = mc.updateMemoryFileDetailed(context.Background(), "memory-id", memorySourceConversation, "conversation-id", mem, "")
				}
				if err == nil {
					t.Fatal("missing home did not fail")
				}
				line := contentLogLine(t, output.String(), "[memory] failed to get memory dir")
				assertActivityLogContentVisibility(t, line, flag == "1", err.Error())
				if !strings.Contains(line, "error_bytes=") {
					t.Errorf("missing safe error metadata: %s", line)
				}
			})
		}
	}
}

func TestMemoryGeneratorUnknownActionKeepsFeatDispatchAndGatesLog(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const action = "private-unknown-model-action-cedar"
			runner := &activityMarkdownTranslationFakeRunner{responses: []string{`{"worth":true}`, `{"action":"` + action + `","title":"A memory","memoryPoints":["I prefer unit tests"],"dimension":"topic_highlight"}`}}
			app := newActivityTranslationTestApp(t, runner)
			if err := app.SaveUIState("ui.language", "en"); err != nil {
				t.Fatal(err)
			}
			if result := NewMemoryGenerator(app).runMemoryPipeline(context.Background(), memorySourceConversation, "conversation-id", "source", ""); result != memPipelineSuccess {
				t.Fatalf("default create behavior changed: result=%v", result)
			}
			line := contentLogLine(t, output.String(), "[memory] action_bytes=")
			assertActivityLogContentVisibility(t, line, flag == "1", action)
			if !strings.Contains(line, "1 memory points") {
				t.Errorf("unknown action lost the existing creation behavior: %s", line)
			}
		})
	}
}

func assertMemoryDecisionMetadata(t *testing.T, app *App, path, decision, title string) {
	t.Helper()
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), title) {
		t.Fatal("disk triage history contains generated title")
	}
	for _, entry := range app.GetTriageLogs() {
		if entry.ID == "conversation-id" && entry.Decision == decision && (entry.Title == "" || entry.Title == title) && entry.TitleBytes != nil && *entry.TitleBytes == len(title) {
			return
		}
	}
	t.Fatal("Learning decisions lost structured generated title")
}

func TestMemoryGeneratorFilteredTitleHistoryKeepsMetadata(t *testing.T) {
	for _, decision := range []string{"skip", "attribution_skip", "attribution_coerce"} {
		for _, flag := range []string{"unset", "0", "1"} {
			t.Run(decision+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, triagePath := isolateActivityPrivacyLogs(t)
				const title = "private-filtered-memory-title-cedar"
				mem := mcMemoryGenerationResponse{Title: title, Dimension: "user_insight", MemoryPoints: []string{"I prefer unit tests"}}
				summary := "[OTHER] a workspace preference"
				switch decision {
				case "skip":
					mem.MemoryPoints = nil
				case "attribution_coerce":
					summary = "[SELF] source\n" + strings.Repeat("[OTHER] source\n", 3)
				}
				raw, err := json.Marshal(mem)
				if err != nil {
					t.Fatal(err)
				}
				runner := &activityMarkdownTranslationFakeRunner{responses: []string{`{"worth":true}`, string(raw)}}
				app := newActivityTranslationTestApp(t, runner)
				if err := app.SaveUIState("ui.language", "en"); err != nil {
					t.Fatal(err)
				}
				NewMemoryGenerator(app).runMemoryPipeline(context.Background(), memorySource("wechat"), "conversation-id", summary, "")
				assertMemoryDecisionMetadata(t, app, triagePath, decision, title)
				line := contentLogLine(t, output.String(), "[triage] memory id=conversation-id decision="+decision)
				if strings.Contains(line, title) {
					t.Errorf("structured private title entered operational gate reason: %s", line)
				}
			})
		}
	}
}
