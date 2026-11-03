package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"yansu-agent/agent"
)

type activityPrivacyProviderErrorRunner struct {
	activityMarkdownTranslationFakeRunner
	providerErr error
	streamError bool
}

func (r *activityPrivacyProviderErrorRunner) ExecuteTask(_ context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.calls++
	r.lastPrompt = input.Prompt
	if r.streamError {
		if err := callback(&agent.StreamEvent{Type: "error", Error: r.providerErr.Error()}); err != nil {
			return "", err
		}
	}
	return "", r.providerErr
}

func TestGeneratedMarkdownNormalizationLogKeepsProviderErrorPrivate(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const secret = "private-normalization-provider-amber"
			providerErr := errors.New(secret)
			runner := &activityPrivacyProviderErrorRunner{providerErr: providerErr}
			app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{})
			app.runner = runner
			const original = "# Original source\n\n## Memory Points\n\n- Keep the exact original source.\n"
			if got := app.normalizeGeneratedMarkdownToCurrentUILanguage("memory", original); got != original {
				t.Fatalf("normalization failure changed the original markdown: %q", got)
			}
			if !strings.Contains(runner.lastPrompt, "Keep the exact original source.") || !strings.Contains(output.String(), "failed to normalize generated memory markdown") {
				t.Fatalf("normalization did not reach the failing provider: prompt=%q logs=%s", runner.lastPrompt, output.String())
			}
			if _, err := app.translateMarkdownToCurrentUILanguage("memory", original); !errors.Is(err, providerErr) || !strings.Contains(err.Error(), secret) {
				t.Fatalf("canonical translation error changed: %v", err)
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", secret)
		})
	}
}

func TestMemoryRewriteLogKeepsProviderErrorPrivate(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const secret = "private-rewrite-provider-meadow"
			providerErr := errors.New(secret)
			runner := &activityPrivacyProviderErrorRunner{providerErr: providerErr, streamError: true}
			app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{})
			app.runner = runner
			existing := []string{"I keep the original source."}
			incoming := []string{"I retain the incoming evidence."}
			points, err := app.rewriteMemoryPointsFirstPerson(context.Background(), existing, incoming, "", "")
			if points != nil || !errors.Is(err, providerErr) || !strings.Contains(err.Error(), secret) {
				t.Fatalf("canonical rewrite error changed: points=%v error=%v", points, err)
			}
			if existing[0] != "I keep the original source." || incoming[0] != "I retain the incoming evidence." || !strings.Contains(runner.lastPrompt, existing[0]) || !strings.Contains(runner.lastPrompt, incoming[0]) {
				t.Fatalf("rewrite changed source evidence: existing=%v incoming=%v prompt=%q", existing, incoming, runner.lastPrompt)
			}
			if !strings.Contains(output.String(), "memory point rewrite stream error") {
				t.Fatalf("rewrite failure was not diagnosed: %s", output.String())
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", secret)
		})
	}
}

func TestTranscriptCleanupLogKeepsProviderErrorPrivate(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const secret = "private-cleanup-provider-cedar"
			runner := &activityPrivacyProviderErrorRunner{providerErr: errors.New(secret)}
			app := mainChatTestApp(t)
			app.runner = runner
			const original = "# Original meeting\n\n## Transcript\n\n[10:00:00] Me: Keep the exact original source.\n"
			got, count := app.cleanTranscriptWithAI(original, "Zoom", 5)
			if got != original || count != 5 || !strings.Contains(runner.lastPrompt, "Keep the exact original source.") {
				t.Fatalf("cleanup failure changed the original transcript: count=%d content=%q prompt=%q", count, got, runner.lastPrompt)
			}
			if !strings.Contains(output.String(), "AI cleanup failed, using raw transcript") {
				t.Fatalf("cleanup failure was not diagnosed: %s", output.String())
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", secret)
		})
	}
}

func TestMeetingFollowupCallerLogKeepsProviderErrorPrivate(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const secret = "private-followup-provider-birch"
			const sourceText = "Keep the exact original source."
			providerErr := errors.New(secret)
			previous := meetingFollowupModel
			t.Cleanup(func() { meetingFollowupModel = previous })
			calls := 0
			meetingFollowupModel = func(_ *App, prompt string) (string, error) {
				calls++
				if !strings.Contains(prompt, sourceText) {
					t.Errorf("follow-up prompt lost source evidence: %q", prompt)
				}
				return "", providerErr
			}
			app := mainChatTestApp(t)
			app.runner = &activityMarkdownTranslationFakeRunner{response: "[10:00:00] Me: " + sourceText}
			start := time.Date(2026, 10, 16, 10, 0, 0, 0, time.Local)
			session := ActivitySessionRow{ID: "followup-private-call", StartTime: start.UnixMilli(), EndTime: start.Add(time.Minute).UnixMilli(), MeetingApp: "Zoom"}
			app.saveMeetingTranscriptWithAutomaticEpoch(session, []activitymon.ActivityEvent{speech(start.UnixMilli(), sourceText)}, "Zoom", nil, "")
			if calls != 1 || !strings.Contains(output.String(), "[meeting-followup] skipped for session "+session.ID) {
				t.Fatalf("follow-up provider failure was not diagnosed: calls=%d logs=%s", calls, output.String())
			}
			paths, err := filepath.Glob(filepath.Join(os.Getenv("HOME"), configDirName, "transcripts", "*.md"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("transcript was not retained: files=%v error=%v", paths, err)
			}
			markdown, err := os.ReadFile(paths[0])
			if err != nil || !strings.Contains(string(markdown), sourceText) {
				t.Fatalf("saved transcript changed: content=%q error=%v", markdown, err)
			}
			if _, err := app.extractMeetingFollowups(context.Background(), session, string(markdown), nil, nil); !errors.Is(err, providerErr) || !strings.Contains(err.Error(), secret) {
				t.Fatalf("canonical follow-up error changed: %v", err)
			}
			if _, found, err := app.store.GetMeetingFollowup(context.Background(), session.ID); err != nil || found {
				t.Fatalf("failed extraction created follow-up evidence: found=%t error=%v", found, err)
			}
			assertActivityLogContentVisibility(t, output.String(), flag == "1", secret)
		})
	}
}
