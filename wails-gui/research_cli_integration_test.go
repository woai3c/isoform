package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestClaudeResearchIntegration runs the two research jobs on the real,
// signed-in Claude Code with only WebSearch, against the real
// web, with its own data folder. It costs a few cents of the user's Claude
// allowance, so it runs only when asked:
//
//	cd wails-gui && YANSU_RESEARCH_INTEGRATION=1 go test -run TestClaudeResearchIntegration -count=1 -v .
func TestClaudeResearchIntegration(t *testing.T) {
	if os.Getenv("YANSU_RESEARCH_INTEGRATION") != "1" {
		t.Skip("set YANSU_RESEARCH_INTEGRATION=1 to run against the real Claude Code and web")
	}
	if !claudeResearchReady() {
		t.Skip("Claude Code is not signed in on this Mac")
	}
	// The app's data lives in a temporary folder (newTopicFindsTestApp); the
	// CLI keeps the real home folder, where its sign-in is.
	ctx, cancel := context.WithTimeout(context.Background(), 2*researchCLITimeout)
	defer cancel()

	// New on your topics.
	app := newTopicFindsTestApp(t)
	svc := app.topicsSvc()
	svc.now = time.Now
	sent := []topicSent{
		{ID: "t1", Key: "wails desktop framework", Label: "Wails desktop framework", Rank: 0},
		{ID: "t2", Key: "go programming language releases", Label: "Go programming language releases", Rank: 1},
	}
	if _, err := svc.store.update(func(st *topicsStateFile) { st.Finds.Sent = sent }); err != nil {
		t.Fatal(err)
	}
	prompt := buildTopicFindsPrompt("English", []string{sent[0].Label, sent[1].Label}, researchViaClaudeCode)
	final, meta, err := runClaudeResearch(ctx, prompt)
	if err != nil {
		t.Fatalf("topic search: %v", err)
	}
	t.Logf("topic search: %d results, %d failures, %d links", meta.Results, meta.Failures, len(meta.URLs))
	if !meta.searched() {
		t.Fatalf("the topic search returned nothing from its tools; answer:\n%s", final)
	}
	app.processTopicFindsRun("integration-1", withResearchMeta(final, meta))
	state := svc.store.load()
	t.Logf("kept %d finds (unavailable=%v)", len(state.Finds.Items), state.Finds.Unavailable)
	for _, f := range state.Finds.Items {
		t.Logf("  %s | %s | %s | %s", f.Topic, f.Title, f.URL, f.WhatsNew)
		if !meta.hasURL(f.URL) {
			t.Fatalf("a find kept a link the searches did not return: %s", f.URL)
		}
	}
	if state.Finds.Unavailable {
		t.Fatal("a search that ran was marked unable to search")
	}

	// Daily News.
	newsPrompt := (&App{}).buildTodayNewsPrompt("English", []string{"Wails desktop framework", "Go programming language"}, researchViaClaudeCode)
	newsFinal, newsMeta, err := runClaudeResearch(ctx, newsPrompt)
	if err != nil {
		t.Fatalf("news: %v", err)
	}
	data := todayNewsDataFromRuns(time.Now().Format("2006-01-02"),
		[]CronRunState{{ID: "n1", Status: "ok", ResultSummary: withResearchMeta(newsFinal, newsMeta), FinishedAt: time.Now().UnixMilli()}})
	t.Logf("news: status %s, %d stories, %d results, %d failures", data.Status, len(data.Items), newsMeta.Results, newsMeta.Failures)
	for _, item := range data.Items {
		t.Logf("  %s | %s | %s", item.Source, item.Title, item.URL)
	}
	if data.Status != "ready" || len(data.Items) == 0 {
		t.Fatalf("Daily News found nothing it could link; answer:\n%s", newsFinal)
	}
}
