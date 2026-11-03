package main

import (
	"os"
	"testing"
)

func TestTodayNewsDataFromRunsKeepsCachedItemsWhileRefreshing(t *testing.T) {
	runs := []CronRunState{
		{ID: "running", Status: "running", StartedAt: 200},
		{
			ID:         "cached",
			Status:     "ok",
			FinishedAt: 100,
			ResultSummary: withResearchMeta(`<news_json>[{
                "title":"Cached headline",
                "summary":"Cached summary",
                "source":"Example",
                "url":"https://example.com/story"
            }]</news_json>`, researchMeta{Results: 1, URLs: []string{"https://example.com/story"}}),
		},
	}

	got := todayNewsDataFromRuns("2026-07-20", runs)
	if got.Status != "generating" {
		t.Fatalf("status = %q, want generating", got.Status)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Cached headline" {
		t.Fatalf("cached items were not preserved: %+v", got.Items)
	}
	if got.UpdatedAt != 100 {
		t.Fatalf("updatedAt = %d, want 100", got.UpdatedAt)
	}
}

func TestTodayNewsDataFromRunsReturnsReadyWithoutActiveRefresh(t *testing.T) {
	runs := []CronRunState{{
		ID:         "complete",
		Status:     "ok",
		FinishedAt: 300,
		ResultSummary: withResearchMeta(`<news_json>[{
            "title":"Fresh headline",
            "summary":"Fresh summary",
            "source":"Example",
            "url":"https://example.com/fresh"
        }]</news_json>`, researchMeta{Results: 1, URLs: []string{"https://example.com/fresh"}}),
	}}

	got := todayNewsDataFromRuns("2026-07-20", runs)
	if got.Status != "ready" {
		t.Fatalf("status = %q, want ready", got.Status)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Fresh headline" {
		t.Fatalf("unexpected items: %+v", got.Items)
	}
}

// The Daily News run needs something to search with. Managed mode (the
// default since the sign-in removal) counts once the sidecar serves the
// active group, and so does an external assistant (Isoform's own loop runs
// through the sidecar); a signed-in Claude Code or an own API key counts on
// its own. An external assistant alone used to count, with no sign-in check.
func TestTodayNewsProviderReady(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previousReady, previousKnown := claudeResearchReady, claudeResearchKnown
	t.Cleanup(func() { claudeResearchReady, claudeResearchKnown = previousReady, previousKnown })
	signedIn := false
	claudeResearchReady = func() bool { return signedIn }
	claudeResearchKnown = func() (bool, bool) { return signedIn, true }

	healthy := &magpieSidecar{}
	healthy.setHealthy(true)
	readyGroups := []magpieGroupPlan{{ID: magpieIsoformGroupID, Members: []string{"claude/claude-sonnet-5-5"}}}

	cases := []struct {
		name    string
		file    string // empty means no file (managed)
		sidecar *magpieSidecar
		groups  []magpieGroupPlan
		signed  bool
		want    bool
	}{
		{name: "managed with a served group is ready", sidecar: healthy, groups: readyGroups, want: true},
		{name: "managed with an empty group is not ready", sidecar: healthy, groups: []magpieGroupPlan{{ID: magpieIsoformGroupID}}},
		{name: "managed without a sidecar is not ready", groups: readyGroups},
		{name: "claude code without sign-in or the sidecar is not ready", file: `{"provider":"claude-code"}`},
		{name: "a signed-in claude code is ready without the sidecar", file: `{"provider":"claude-code"}`, signed: true, want: true},
		{name: "codex without the sidecar is not ready", file: `{"provider":"codex"}`},
		{name: "codex with a served group is ready", file: `{"provider":"codex"}`, sidecar: healthy, groups: readyGroups, want: true},
		{name: "own anthropic key is ready", file: `{"provider":"anthropic","apiKey":"k"}`, want: true},
		{name: "anthropic without a key is not ready", file: `{"provider":"anthropic"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.file != "" {
				writeAIProviderFile(t, tc.file)
			} else {
				path, err := aiProviderConfigPath()
				if err != nil {
					t.Fatal(err)
				}
				_ = os.Remove(path)
			}
			signedIn = tc.signed
			app := &App{magpie: tc.sidecar}
			app.magpieModels.applyBootstrap(magpieBootstrapState{External: true, ActiveGroup: magpieIsoformGroupID, Groups: tc.groups}, nil)
			if got := app.todayNewsProviderReady(); got != tc.want {
				t.Fatalf("todayNewsProviderReady() = %v, want %v", got, tc.want)
			}
		})
	}
}
