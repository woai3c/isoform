package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCachedActivityNarrativeStaysRawLocallyAndRedactedAtOutboundBoundaries(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	now := time.Now()
	date := now.Format("2006-01-02")
	week := now.AddDate(0, 0, -(int(now.Weekday())+6)%7).Format("2006-01-02") + "/week"
	seedActivitySummaryI18nSession(t, store, "source", date, 9, []string{"Code"})
	narrative := "Useful local context with token " + activityPrivacySecret
	for _, item := range []struct{ date, kind string }{{date, "daily"}, {week, "weekly"}} {
		if _, err := store.SaveSummary(item.date, item.kind, narrative, "test", false); err != nil {
			t.Fatal(err)
		}
	}
	daily, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatal(err)
	}
	weekly, err := app.GetWeeklySummary(date)
	if err != nil {
		t.Fatal(err)
	}
	if daily.AISummary != narrative || weekly.AISummary != narrative {
		t.Fatalf("local narrative was rewritten: daily=%q weekly=%q", daily.AISummary, weekly.AISummary)
	}
	recorder := httptest.NewRecorder()
	(&cronAPIServer{app: app}).handleActivitySummary(recorder, httptest.NewRequest("GET", "/activity/summary?date="+date, nil))
	var response struct {
		Journal string `json:"journal"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	inputs := suggestionPromptInputs{}
	(&SuggestionGenerator{app: app}).collectDailySummaryInputs(&inputs)
	for name, text := range map[string]string{
		"CLI journal":  response.Journal,
		"quick chat":   app.quickChatGeneralContext(),
		"suggestions":  inputs.TodayAISummary,
		"weekly model": buildWeeklySummaryPrompt(weekly, "English"),
	} {
		if !strings.Contains(text, "Useful local context") || strings.Contains(text, activityPrivacySecret) {
			t.Errorf("%s boundary lost context or leaked secret: %q", name, text)
		}
	}
	// Redaction must not rewrite the stored derivative either.
	if cached, _, _, found := store.GetCachedSummary(date, "daily"); !found || cached != narrative {
		t.Fatal("outbound read modified local cached narrative")
	}
}
