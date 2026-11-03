package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yansu-agent/agent"
)

func TestResearchDenialsAreRecordedAsFailedRuns(t *testing.T) {
	const answer = `<news_json>[{"title":"A real story","summary":"Summary","source":"Example","url":"https://example.com/story"}]</news_json>`
	cases := []struct {
		name       string
		output     string
		toolFailed bool
		wantStatus string
		wantToday  string
		wantReason string
	}{
		{"permission after progress", "Starting the search.\nThe user denied permission to execute this command.", true, "error", "denied", "denied permission to execute"},
		{"guard after progress", "Starting the search.\n  Blocked: this run needs the user's approval", true, "error", "denied", "Blocked:"},
		{"authority after progress", "Starting the search.\nBlocked by authority policy: no matching grant", true, "error", "denied", "Blocked by authority policy"},
		{"nonzero tool exit status", "response: partial https://example.com/story\n(exit code: 77)", true, "ok", "unavailable", ""},
		{"browser disconnected", "ok: false\nerror:\n  code: BROWSER_CONNECT\n  exitCode: 69", true, "ok", "unavailable", ""},
		{"piped browser failure", "ok: false\nerror:\n  code: BROWSER_CONNECT", false, "ok", "unavailable", ""},
		{"search timed out", "[command timed out]\npartial https://example.com/story", true, "ok", "unavailable", ""},
		{"normal run", "response: a real story https://example.com/story", false, "ok", "ready", ""},
		{"ordinary blocked prose", "response: a real story https://example.com/story\nThe article says the proposal was blocked: by a court.", false, "ok", "ready", ""},
		{"ordinary permission prose", "response: a real story https://example.com/story\nThe article explains how to fix a Permission denied error.", false, "ok", "ready", ""},
		{"first line blocked headline", "Blocked: a proposed merger\nhttps://example.com/story", false, "ok", "ready", ""},
		{"later line blocked headline", "Search results:\nBlocked: a proposed merger\nhttps://example.com/story", false, "ok", "ready", ""},
		{"first line permission headline", "Permission denied: understanding file access\nhttps://example.com/story", false, "ok", "ready", ""},
		{"later line permission headline", "Search results:\nPermission denied: understanding file access\nhttps://example.com/story", false, "ok", "ready", ""},
		{"quoted denied execution", "Search results:\nThe user denied permission to execute the example command.\nhttps://example.com/story", false, "ok", "ready", ""},
	}
	previousReady, previousRun := claudeResearchReady, runClaudeResearch
	t.Cleanup(func() { claudeResearchReady, runClaudeResearch = previousReady, previousRun })
	for _, via := range []string{researchViaOpencli, researchViaClaudeCode} {
		for _, tc := range cases {
			t.Run(via+"/"+tc.name, func(t *testing.T) {
				rig := newHandoffTestRig(t)
				claudeResearchReady = func() bool { return via == researchViaClaudeCode }
				runClaudeResearch = func(context.Context, string) (string, researchMeta, error) {
					meta := researchMeta{Via: researchViaClaudeCode}
					meta.addSearchResult(tc.output, tc.toolFailed)
					return answer, meta, nil
				}
				rig.app.runner = &writeGateRunner{events: []agent.StreamEvent{
					{Type: "tool_use", ToolName: "Bash", ToolUseID: "search", ToolInput: map[string]any{"command": "opencli grok ask news --site-session ephemeral"}},
					{Type: "tool_result", ToolName: "Bash", ToolUseID: "search", Content: tc.output, IsError: tc.toolFailed, ToolSucceeded: !tc.toolFailed},
					{Type: "assistant", RunID: "answer", Content: answer},
				}}
				if _, err := rig.app.topicsSvc().store.update(func(st *topicsStateFile) {
					st.Finds.Manual = true
					st.Finds.Sent = findsSelection().Sent
				}); err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				job := upcomingTestJob("topic-denial-test", topicFindsJobName, "manual", `{}`, true)
				job.SurfaceKind = todayNewsSurfaceKind
				job.NotifyPlatform = "none"
				if err := rig.state.SaveCronJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				scheduler := NewCronScheduler(rig.state, func(string, any) {}, func(string, string) {}, rig.app.executeAgentJob)
				if err := scheduler.loadJobs(ctx); err != nil {
					t.Fatal(err)
				}
				scheduler.executeJob(ctx, &job, "manual")
				runs, err := scheduler.GetRuns(ctx, job.ID)
				if err != nil || len(runs) != 1 {
					t.Fatalf("recorded runs = %+v, err = %v", runs, err)
				}
				if runs[0].Status != tc.wantStatus || !strings.Contains(runs[0].Error, tc.wantReason) {
					t.Fatalf("recorded run = %+v, want %s with reason %q", runs[0], tc.wantStatus, tc.wantReason)
				}
				if tc.wantStatus == "ok" && runs[0].Error != "" {
					t.Fatalf("successful run retained an error: %s", runs[0].Error)
				}
				saved, err := rig.state.LoadCronJob(ctx, job.ID)
				if err != nil || saved.LastRunStatus != tc.wantStatus || saved.LastError != runs[0].Error {
					t.Fatalf("job status disagrees with recorded run: %+v, err = %v", saved, err)
				}
				data := todayNewsDataFromRuns("2026-10-16", runs)
				if data.Status != tc.wantToday {
					t.Fatalf("Today status = %q, want %q", data.Status, tc.wantToday)
				}
				if tc.wantToday == "ready" && (len(data.Items) != 1 || data.Items[0].URL != "https://example.com/story") {
					t.Fatalf("successful search lost its story URL: %+v", data.Items)
				}
				if tc.wantToday != "ready" && len(data.Items) != 0 {
					t.Fatalf("failed run supplied stories: %+v", data.Items)
				}
			})
		}
	}
}

func TestClaudeResearchStreamPreservesLaterDenial(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"s1","name":"WebSearch","input":{"query":"news"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"s1","is_error":true,"content":"Starting search.\nThe user denied permission to execute WebSearch."}]}}`,
		`{"type":"result","subtype":"success","result":"<news_json>[]</news_json>"}`,
	}, "\n")
	_, meta, err := parseClaudeResearchStream(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Results != 0 || meta.Failures != 1 || !meta.Denied || !strings.Contains(meta.FailureReason, "denied permission") {
		t.Fatalf("denial metadata = %+v", meta)
	}
	if err := meta.runError(); err == nil || !strings.Contains(err.Error(), "denied permission") {
		t.Fatalf("denied search error = %v", err)
	}
}

func TestResearchRecoveredSearchStillSucceeds(t *testing.T) {
	meta := researchMeta{Via: researchViaOpencli}
	meta.addSearchResult("Blocked: this adapter cannot run", true)
	meta.addSearchResult("response: found https://example.com/story", false)
	if err := meta.runError(); err != nil {
		t.Fatalf("a recovered search failed: %v", err)
	}
	if meta.Results != 1 || meta.Failures != 1 || !meta.hasURL("https://example.com/story") {
		t.Fatalf("recovered search metadata = %+v", meta)
	}
}

func TestClaudeResearchErrorFindsDenialBeforeBoundingDiagnostic(t *testing.T) {
	const reason = "Blocked: this run needs approval"
	line, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "error_during_execution", "is_error": true,
		"result": strings.Repeat("Progress output.\n", 100) + reason,
	})
	if err != nil {
		t.Fatal(err)
	}
	final, meta, err := parseClaudeResearchStream(strings.NewReader(string(line)))
	if err == nil || final != "" || !meta.Denied || meta.FailureReason != reason || !strings.Contains(err.Error(), reason) {
		t.Fatalf("later denial lost: final %q, meta %+v, err %v", final, meta, err)
	}
}

func TestTodayNewsDenialKeepsCachedStoriesAndClearsAfterSuccess(t *testing.T) {
	const date = "2026-10-16"
	denied := CronRunState{Status: "error", Error: "research denied: Blocked: this run needs approval"}
	cached := CronRunState{Status: "ok", FinishedAt: 100, ResultSummary: withResearchMeta(
		`<news_json>[{"title":"Cached story","url":"https://example.com/story"}]</news_json>`,
		researchMeta{Results: 1, URLs: []string{"https://example.com/story"}},
	)}
	data := todayNewsDataFromRuns(date, []CronRunState{denied, cached})
	if data.Status != "denied" || len(data.Items) != 1 || data.UpdatedAt != 100 {
		t.Fatalf("denied refresh hid cached news: %+v", data)
	}
	data = todayNewsDataFromRuns(date, []CronRunState{cached, denied})
	if data.Status != "ready" || len(data.Items) != 1 {
		t.Fatalf("old denial hid a later successful run: %+v", data)
	}
	data = todayNewsDataFromRuns(date, []CronRunState{{Status: "running"}, denied, cached})
	if data.Status != "generating" || len(data.Items) != 1 {
		t.Fatalf("denial hid an active refresh: %+v", data)
	}
}
