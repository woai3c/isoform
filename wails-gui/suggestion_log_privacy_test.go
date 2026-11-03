package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"wails-gui/storage"
	"yansu-agent/agent"
)

func setContentLogDebugFlag(t *testing.T, flag string) {
	t.Helper()
	t.Setenv(backgroundTranscriptDebugEnv, flag)
	if flag == "unset" {
		if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
			t.Fatal(err)
		}
	}
}

func contentLogLine(t *testing.T, logs, prefix string) string {
	t.Helper()
	start := strings.Index(logs, prefix)
	if start < 0 {
		t.Fatalf("missing log site %q in %s", prefix, logs)
	}
	return strings.SplitN(logs[start:], "\n", 2)[0]
}

func TestSuggestionInputLogsKeepEveryContentFieldPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			inputs := suggestionPromptInputs{
				UserName: "private-user-cobalt", RecentActivity: []string{"private-activity-cobalt"},
				RecentAppNames: []string{"private-app-cobalt"}, RecentConvTitles: []string{"private-conversation-cobalt"},
				TodayAISummary: "private-today-cobalt", YesterdayAISummary: "private-yesterday-cobalt",
				TodayTopApps: []string{"private-top-app-cobalt"}, TodayKeyMoments: []string{"private-moment-cobalt"},
				TodayTopWindowTitles: []string{"private-window-cobalt"}, TodayTopOCRKeywords: []string{"private-ocr-cobalt"},
			}
			logSuggestionInputs(inputs, hashInputs(inputs))
			for _, site := range []struct{ prefix, field, secret string }{
				{"[suggestions][inputs] hash=", "user", inputs.UserName},
				{"activity[0]:", "activity", inputs.RecentActivity[0]},
				{"apps:", "apps", inputs.RecentAppNames[0]},
				{"convTitle[0]:", "title", inputs.RecentConvTitles[0]},
				{"todayAISummary:", "summary", inputs.TodayAISummary},
				{"yesterdayAISummary:", "summary", inputs.YesterdayAISummary},
				{"topApps:", "apps", inputs.TodayTopApps[0]},
				{"keyMoment[0]:", "moment", inputs.TodayKeyMoments[0]},
				{"topWindowTitles:", "titles", inputs.TodayTopWindowTitles[0]},
				{"topOCRKeywords:", "keywords", inputs.TodayTopOCRKeywords[0]},
			} {
				t.Run(site.field+"/"+site.secret, func(t *testing.T) {
					line := contentLogLine(t, output.String(), site.prefix)
					assertActivityLogContentVisibility(t, line, flag == "1", site.secret)
					if !strings.Contains(line, site.field+"_bytes=") {
						t.Errorf("missing content size at %s: %s", site.prefix, line)
					}
				})
			}
		})
	}
}

func TestSuggestionGenerationLogsKeepPromptResponseAndChipsPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			chips := []string{"private-chip-cobalt", "private-chip-amber", "private-chip-sage"}
			raw, err := json.Marshal(suggestionGenerationResponse{Suggestions: chips})
			if err != nil {
				t.Fatal(err)
			}
			runner := &activityMarkdownTranslationFakeRunner{response: string(raw)}
			app := newActivityTranslationTestApp(t, runner)
			app.cachedDisplayName = "private-name-cobalt"
			const conversation = "private-conversation-sage"
			if err := app.store.SaveProject(context.Background(), storage.ProjectRow{ID: "test", Name: "test", Path: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			if err := app.store.SaveSession(context.Background(), storage.SessionRow{LocalID: "private-log-test", ProjectID: "test", Label: conversation}); err != nil {
				t.Fatal(err)
			}
			if err := NewSuggestionGenerator(app).generate(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			logs := output.String()
			for _, site := range []struct{ prefix, field, preview string }{
				{"[suggestions][prompt]", "prompt", "You generate short"},
				{"[suggestions][response] prompt", "prompt", "You generate short"},
				{"[suggestions][response] raw", "response", chips[0]},
				{"[suggestions] published batch", "chips", chips[0]},
			} {
				t.Run(site.field+"/"+site.prefix, func(t *testing.T) {
					line := contentLogLine(t, logs, site.prefix)
					if !strings.Contains(line, site.field+"_bytes=") {
						t.Errorf("missing size: %s", line)
					}
					assertActivityLogContentVisibility(t, line, flag == "1", site.preview)
				})
			}
			assertActivityLogContentVisibility(t, logs, flag == "1", app.cachedDisplayName, conversation, chips[0])
			if !strings.Contains(runner.lastPrompt, conversation) || !strings.Contains(runner.lastPrompt, app.cachedDisplayName) {
				t.Fatalf("logging changed canonical prompt: %s", runner.lastPrompt)
			}
			row, err := app.store.LatestComposerSuggestions(context.Background())
			if err != nil || row == nil || !strings.Contains(row.Suggestions, chips[0]) {
				t.Fatalf("logging changed canonical suggestions: row=%+v error=%v", row, err)
			}
		})
	}
}

type suggestionErrorPrivacyRunner struct {
	activityMarkdownTranslationFakeRunner
	err error
}

func (r *suggestionErrorPrivacyRunner) ExecuteTask(context.Context, *agent.TaskInput, agent.StreamCallback) (string, error) {
	return "", r.err
}

func TestSuggestionFailureLogsKeepModelContentPrivate(t *testing.T) {
	for _, stage := range []string{"execute", "parse"} {
		for _, flag := range []string{"unset", "0", "1"} {
			t.Run(stage+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-suggestion-failure-cobalt"
				runner := &activityMarkdownTranslationFakeRunner{response: `{"suggestions":["` + secret + `"`}
				app := newActivityTranslationTestApp(t, runner)
				app.cachedDisplayName = "user"
				prefix := "[suggestions] parse failed"
				if stage == "execute" {
					app.runner = &suggestionErrorPrivacyRunner{err: errors.New(secret)}
					prefix = "[suggestions] ExecuteTask failed"
				}
				err := NewSuggestionGenerator(app).generate(context.Background(), nil)
				if err == nil || !strings.Contains(err.Error(), secret) {
					t.Fatalf("canonical error was lost: %v", err)
				}
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, flag == "1", secret)
				if !strings.Contains(line, "error_bytes=") {
					t.Errorf("missing error size: %s", line)
				}
			})
		}
	}
}
