package main

import (
	"context"
	"strings"
	"testing"
	"time"
	"wails-gui/internal/pii"
)

func TestQuickChatSuggestionsRecheckDailyCacheAndRedact(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTimelineTestStore(t)
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `[{"title":"Personal cached suggestion","prompt":"Discuss the diary"}]`}}
	app := &App{ctx: context.Background(), runner: runner, activity: &activityMonitorState{store: store}}
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	for i, day := range []string{today, yesterday} {
		id := []string{"today-source", "yesterday-source"}[i]
		seedActivitySummaryI18nSession(t, store, id, day, 9, []string{"Diary"})
		if _, err := store.SaveSummary(day, "daily", "historical-diary-marker "+activityPrivacySecret, "test", false); err != nil {
			t.Fatal(err)
		}
	}
	recommendedQuestionsStore.mu.Lock()
	prior := recommendedQuestionsStore.byLang
	recommendedQuestionsStore.byLang = make(map[string]*recommendedQuestionsCacheEntry)
	recommendedQuestionsStore.mu.Unlock()
	t.Cleanup(func() {
		recommendedQuestionsStore.mu.Lock()
		recommendedQuestionsStore.byLang = prior
		recommendedQuestionsStore.mu.Unlock()
	})
	questions, err := app.GetQuickChatSuggestionsForApp("", "en")
	if err != nil || len(questions) != 1 || len(runner.inputs) != 1 {
		t.Fatalf("safe suggestions=%v calls=%d err=%v", questions, len(runner.inputs), err)
	}
	prompt := runner.inputs[0].Prompt
	if strings.Contains(prompt, activityPrivacySecret) || !strings.Contains(prompt, "[REDACTED:") || !strings.Contains(prompt, "Today") || !strings.Contains(prompt, "Yesterday") {
		t.Fatalf("unsafe/missing context: %s", prompt)
	}
	pii.AddUserSensitiveApp("com.example.diary", "Diary")
	if got := app.quickChatGeneralContext(); got != "" {
		t.Fatalf("protected day leaked: %s", got)
	}
	questions, err = app.GetQuickChatSuggestionsForApp("", "en")
	if err != nil || len(runner.inputs) != 1 {
		t.Fatalf("protected day entered a new model call: %v calls=%d", err, len(runner.inputs))
	}
	for _, q := range questions {
		if q.Title == "Personal cached suggestion" {
			t.Fatal("protected cached suggestion reused")
		}
	}
	app.closedEyes.enabled.Store(true)
	if got := app.quickChatGeneralContext(); got != "" {
		t.Fatalf("closed-eyes context: %s", got)
	}
}
