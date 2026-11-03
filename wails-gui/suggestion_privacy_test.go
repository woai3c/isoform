package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

func TestSuggestionPromptRedactsAllFieldsBeforeTruncation(t *testing.T) {
	secret := activityPrivacySecret
	in := suggestionPromptInputs{UserName: secret, RecentActivity: []string{secret}, RecentAppNames: []string{secret},
		RecentConvTitles: []string{secret}, TodayAISummary: secret, YesterdayAISummary: secret, TodayTopApps: []string{secret},
		TodayKeyMoments: []string{secret}, TodayTopWindowTitles: []string{secret}, TodayTopOCRKeywords: []string{secret}}
	prompt := buildSuggestionPrompt(in)
	if strings.Contains(prompt, secret) || !strings.Contains(prompt, "[REDACTED:api_key]") {
		t.Fatalf("suggestion prompt leaks content: %s", prompt)
	}
	// A secret crossing the prose cap must be redacted while still intact.
	in.RecentActivity = []string{strings.Repeat("x", 480) + " " + secret}
	if prompt = buildSuggestionPrompt(in); strings.Contains(prompt, "sk-proj-") {
		t.Fatal("truncation exposed the beginning of a secret")
	}
}

func TestSuggestionLogsContainOnlyMetadata(t *testing.T) {
	t.Setenv(backgroundTranscriptDebugEnv, "")
	const content = "private-user-content-marker"
	in := suggestionPromptInputs{UserName: content, RecentActivity: []string{content}, RecentAppNames: []string{content},
		RecentConvTitles: []string{content}, TodayAISummary: content, YesterdayAISummary: content, TodayTopApps: []string{content},
		TodayKeyMoments: []string{content}, TodayTopWindowTitles: []string{content}, TodayTopOCRKeywords: []string{content}}
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(old) })
	logSuggestionInputs(in, "source-0123456789")
	if strings.Contains(output.String(), content) {
		t.Fatalf("input log exposed private user content: %s", output.String())
	}
	for _, metadata := range []string{"hash=source-01234", "activities=1", "user_bytes=27"} {
		if !strings.Contains(output.String(), metadata) {
			t.Errorf("missing useful metadata %q: %s", metadata, output.String())
		}
	}
}

func TestSuggestionInputsExcludeProtectedDerivedContext(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newBindingsTestApp(t)
	store := newTestStore(t)
	app.activity = &activityMonitorState{store: store}
	start := time.Now().Add(-time.Minute).UnixMilli()
	for _, session := range []ActivitySessionRow{
		{ID: "public", StartTime: start, EndTime: start + 1000, AppNames: `["Notes"]`, TriageStatus: "done"},
		{ID: "private", StartTime: start, EndTime: start + 1000, AppNames: `["Private Notes"]`, TriageStatus: "done"},
	} {
		if err := store.SaveSession(session, nil, nil); err != nil {
			t.Fatal(err)
		}
		summary := session.ID + "-derived-marker"
		if _, err := store.SaveActivityPipelineRun(ActivityPipelineRunRow{Kind: "group", StartedAt: start, CompletedAt: start + 1000, SessionIDs: `["` + session.ID + `"]`, AppNames: session.AppNames, AISummary: summary}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SaveSummary(time.Now().Format("2006-01-02"), "daily", "private-cached-summary", "test", false); err != nil {
		t.Fatal(err)
	}
	generator := &SuggestionGenerator{app: app}
	inputs := generator.collectInputs(t.Context())
	prompt := buildSuggestionPrompt(inputs)
	if strings.Contains(prompt, "private-derived-marker") || strings.Contains(prompt, "private-cached-summary") || strings.Contains(prompt, "Private Notes") {
		t.Fatalf("protected derivatives reached suggestion prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "public-derived-marker") {
		t.Fatal("public pipeline summary must remain available")
	}
}

func TestSuggestionModelLogsExcludePromptResponseAndChips(t *testing.T) {
	t.Setenv(backgroundTranscriptDebugEnv, "")
	isolateActivityPrivacyTest(t)
	const privateOutput = "private-model-output-marker"
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{
		response: `{"suggestions":["private-model-output-marker","Read my day","Plan my week"]}`,
		ready:    true,
	}}
	app := newBindingsTestApp(t)
	app.runner = runner
	generator := &SuggestionGenerator{app: app}
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(old) })
	if err := generator.generate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("model calls = %d, want 1", runner.calls)
	}
	if runner.inputs[0].Priority != agent.PriorityBackground {
		t.Fatalf("suggestions priority = %q, want background", runner.inputs[0].Priority)
	}
	for _, private := range []string{runner.lastPrompt, runner.response, privateOutput} {
		if strings.Contains(output.String(), private) {
			t.Fatalf("model prompt or output escaped into info log: %s", output.String())
		}
	}
	for _, metadata := range []string{"[suggestions][prompt] prompt_bytes=", "[suggestions][response] raw: response_bytes=", "chips_bytes=", "published batch id="} {
		if !strings.Contains(output.String(), metadata) {
			t.Errorf("missing metadata %q: %s", metadata, output.String())
		}
	}
}
