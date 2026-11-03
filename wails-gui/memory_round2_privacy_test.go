package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func writePrivacyMemory(t *testing.T, id, content string) {
	t.Helper()
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCronMemoryRoutesApplyPrivacyBeforeLimit(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	insertTestSession(t, app.activity.store, "private-memory-source", "dismissed", 1000)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data = '{"windowTitle":"Bank - Incognito"}' WHERE session_id = 'private-memory-source'`); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"safe":      "# Public note\n\n- **Updated**: 2026-01-01T00:00:00Z\n\nordinary customer@example.com",
		"sensitive": "# Secret diary title\n\n- **Apps**: Diary\n\nprivate diary marker",
		"private":   "# Private browsing title\n\n- **Activity Session**: private-memory-source\n\nprivate browser marker",
		"flattened": withMemoryReadProvenance(buildOKFMemoryMarkdown("flattened", "Flattened protected title", time.Now(), "manual", "custom", []string{"flattened private marker"}), memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Apps: []string{"Diary"}}),
	}
	for id, content := range fixtures {
		writePrivacyMemory(t, id, content)
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	api := &cronAPIServer{app: app}
	for _, route := range []string{"list?limit=1", "show/sensitive", "show/private", "show/flattened", "show/safe"} {
		t.Run(route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/memory/"+route, nil)
			response := httptest.NewRecorder()
			if strings.HasPrefix(route, "list") {
				api.handleMemoryList(response, req)
				var entries []memoryEntrySummary
				if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil || len(entries) != 1 || entries[0].ID != "safe" {
					t.Fatalf("privacy filtering did not precede limit: %s err=%v", response.Body.String(), err)
				}
			} else {
				api.handleMemoryShow(response, req)
				want := http.StatusNotFound
				if route == "show/safe" {
					want = http.StatusOK
				}
				if response.Code != want {
					t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
				}
			}
			for _, forbidden := range []string{"diary marker", "browser marker", "protected title", "customer@example.com"} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Errorf("response leaked %q", forbidden)
				}
			}
		})
	}
	// Direct UI reads remain available for inspection and correction.
	if _, err := app.GetMemoryEntry("sensitive"); err != nil {
		t.Fatalf("local inspection changed: %v", err)
	}
	app.closedEyes.enabled.Store(true)
	response := httptest.NewRecorder()
	api.handleMemoryList(response, httptest.NewRequest(http.MethodGet, "/memory/list", nil))
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("closed-eyes list leaked: %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	api.handleMemoryShow(response, httptest.NewRequest(http.MethodGet, "/memory/show/safe", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("closed-eyes detail leaked: %s", response.Body.String())
	}
}

func TestSearchMemoryPrivacyDoesNotConsumeResultLimit(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	store, err := NewMemoryStore(filepath.Join(os.Getenv("HOME"), "search-budget"), MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app.memoryStore = store
	for i := range 100 {
		body := "# Ordinary cooking\n\nRoast vegetables for dinner."
		if i < 12 {
			body = "# privacybudgetneedle\n\n- **Apps**: Diary\n\nprivacybudgetneedle"
		} else if i < 16 {
			body = "# Readable result\n\nprivacybudgetneedle " + strings.Repeat("routine workflow detail ", 10)
		}
		if err := os.WriteFile(filepath.Join(store.baseDir, fmt.Sprintf("note-%02d.md", i)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Sync("privacy-budget"); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.Search(t.Context(), "privacybudgetneedle", 3)
	if err != nil || len(baseline) != 3 {
		t.Fatalf("invalid fixture: %v %v", paths(baseline), err)
	}
	filtered, err := app.filterMemorySearchReads(store, baseline)
	if err != nil || len(filtered) >= 3 {
		t.Fatalf("fixture did not place protected entries before the cap: %v %v", paths(filtered), err)
	}
	results, err := app.SearchMemory("privacybudgetneedle", 3)
	if err != nil || len(results) != 3 {
		t.Fatalf("protected hits consumed the result limit: %v %v", paths(results), err)
	}
	for _, result := range results {
		if strings.Contains(result.Snippet, "Diary") {
			t.Fatalf("protected result: %+v", result)
		}
	}
}

func TestMemoryDreamFiltersSourcesAgainBeforeModelPass(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{response: `{"delete":[],"synthesize":[]}`, ready: true}}
	app.runner = runner
	app.ctx = context.Background()
	for id, content := range map[string]string{
		"safe-one": "# Safe one " + activityPrivacySecret + "\n\n- public fact " + activityPrivacySecret,
		"safe-two": "# Safe two\n\n- public fact",
		"private":  "# Protected captured title\n\n- **Apps**: Diary\n\n- protected-source-marker",
	} {
		writePrivacyMemory(t, id, content)
	}
	service := NewMemoryDreamService(app)
	captured, err := service.gatherCandidates(t.Context())
	if err != nil || len(captured) != 3 {
		t.Fatalf("gather fixture: candidates=%d err=%v", len(captured), err)
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	current, err := service.gatherCandidates(t.Context())
	if err != nil || len(current) != 2 {
		t.Fatalf("gather did not remove protected source: candidates=%d err=%v", len(current), err)
	}
	if err := service.runL3Merge(&DreamRun{}, captured, true, &DreamPreview{}); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 {
		t.Fatalf("safe pair should still be considered: calls=%d", len(runner.inputs))
	}
	prompt := runner.inputs[0].Prompt
	for _, forbidden := range []string{"protected-source-marker", "Protected captured title", activityPrivacySecret} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("dream prompt leaked %q", forbidden)
		}
	}
	if !strings.Contains(prompt, "Safe one") || !strings.Contains(prompt, "[REDACTED:") {
		t.Fatalf("safe evidence was lost: %q", prompt)
	}
	app.closedEyes.enabled.Store(true)
	if err := service.runL3Merge(&DreamRun{}, captured, true, &DreamPreview{}); err != nil || len(runner.inputs) != 1 {
		t.Fatalf("closed-eyes dream called model: calls=%d err=%v", len(runner.inputs), err)
	}
}

func TestMemoryDreamRedactsBeforeTruncation(t *testing.T) {
	content := strings.Repeat("x", dreamL3MaxCharsPerMemory-20) + " " + activityPrivacySecret + " trailing"
	prompt := buildL3Prompt([]DreamMemoryCandidate{{ID: "one", Title: activityPrivacySecret, Content: content}})
	if strings.Contains(prompt, "sk-proj-") || !strings.Contains(prompt, "[REDACTED:") {
		t.Fatalf("dream truncated before redacting: %q", prompt)
	}
}

func TestMemoryGeneratorsGateDedupSourcesBeforeModelPass(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	for _, dirFn := range []func() (string, error){memoryDir, knowledgeDir} {
		dir, err := dirFn()
		if err != nil {
			t.Fatal(err)
		}
		for id, content := range map[string]string{
			"private": "# Protected dedup title\n\n- **Apps**: Diary\n\n- protected-dedup-marker",
			"safe":    "# Allowed dedup title\n\n- allowed-dedup-marker " + activityPrivacySecret,
		} {
			// Explicit authored lineage keeps this control independent of legacy-source policy.
			content = memoryWithResolvedProvenance(id, content, memoryReadSource("manual"))
			if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{response: `{"action":"skip","memoryPoints":[],"knowledgePoints":[]}`, ready: true}}
	app.runner = runner
	mc := NewMemoryGenerator(app)
	if _, err := mc.runTriage(t.Context(), memorySourceConversation, "current conversation", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mc.runGeneration(t.Context(), memorySourceConversation, "current", "current conversation", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mc.runKnowledgeGeneration(t.Context(), "current conversation", ""); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 3 {
		t.Fatalf("calls=%d", len(runner.inputs))
	}
	for _, input := range runner.inputs {
		if strings.Contains(input.Prompt, "protected-dedup-marker") || strings.Contains(input.Prompt, "Protected dedup title") || strings.Contains(input.Prompt, activityPrivacySecret) {
			t.Fatalf("generator sent protected dedup source: %q", input.Prompt)
		}
		if !strings.Contains(input.Prompt, "allowed-dedup-marker") || !strings.Contains(input.Prompt, "[REDACTED:") {
			t.Fatalf("generator lost allowed comparison: %q", input.Prompt)
		}
	}
}

func TestMemoryUpdateKeepsProtectedTargetsLocal(t *testing.T) {
	for _, writer := range []string{"conversation", "activity"} {
		t.Run(writer, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			previous := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
			pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
			if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
				t.Fatal(err)
			}
			runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"memoryPoints":["old private fact","new current fact"]}`}}
			app.runner = runner
			// A generated target is eligible for a local merge. A manual record
			// instead rejects automated updates before privacy is considered.
			original := withMemoryReadProvenance(buildOKFMemoryMarkdown("target", "Protected target", time.Now(), memorySourceConversation, "user_insight", []string{"old private fact"}), memoryReadProvenance{Version: 2, Kinds: []string{"conversation"}, Apps: []string{"Diary"}})
			writePrivacyMemory(t, "target", original)
			response := &mcMemoryGenerationResponse{Title: "Updated target", Dimension: "user_insight", MemoryPoints: []string{"new current fact"}}
			var err error
			switch writer {
			case "conversation":
				_, _, err = NewMemoryGenerator(app).updateMemoryFileDetailed(t.Context(), "target", memorySourceConversation, "current", response, "")
			case "activity":
				insertTestSession(t, app.activity.store, "current", "dismissed", 1000)
				session, readErr := app.activity.store.GetSessionByID("current")
				if readErr != nil {
					t.Fatal(readErr)
				}
				_, err = (&ActivityTriager{app: app}).updateActivityMemory(t.Context(), "target", *session, sessionGroup{Sessions: []ActivitySessionRow{*session}}, response)
			}
			if err != nil {
				t.Fatalf("local update failed: %v", err)
			}
			if len(runner.inputs) != 0 {
				t.Fatalf("protected target reached rewrite/translation model: calls=%d", len(runner.inputs))
			}
			entry, err := app.GetMemoryEntry("target")
			if err != nil || !strings.Contains(entry.Content, "old private fact") || !strings.Contains(entry.Content, "new current fact") {
				t.Fatalf("local merge lost facts: %+v err=%v", entry, err)
			}
			provenance := memoryReadProvenanceFromContent(entry.Content)
			if !containsString(provenance.Apps, "Diary") || app.memorySourceAllowsRead(entry.Content) {
				t.Fatalf("local update lost source privacy: %+v", provenance)
			}
		})
	}
}

func TestMemoryRewriteRedactsEveryPointBeforeModelPass(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true, response: `{"memoryPoints":["merged redacted fact"]}`}}
	app.runner = runner
	_, err := app.rewriteMemoryPointsFirstPerson(t.Context(), []string{"old " + activityPrivacySecret}, []string{"new " + activityPrivacySecret}, activityPrivacySecret, "")
	if err != nil || len(runner.inputs) != 1 {
		t.Fatalf("rewrite failed: calls=%d err=%v", len(runner.inputs), err)
	}
	if strings.Contains(runner.inputs[0].Prompt, activityPrivacySecret) || !strings.Contains(runner.inputs[0].Prompt, "[REDACTED:") {
		t.Fatal("rewrite model received unredacted points or display name")
	}
}

func TestMemoryReadGateValidatesExplicitEnvelopeDependencies(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	for name, envelope := range map[string]string{
		"malformed":               `{"version":2,"apps":[`,
		"unknown-version":         `{"version":3,"kinds":["manual"]}`,
		"missing-version":         `{"kinds":["manual"]}`,
		"v1-conversation-app":     `{"version":1,"kinds":["conversation"],"apps":["Diary"]}`,
		"v1-conversation-session": `{"version":1,"kinds":["conversation"],"sessions":["unavailable"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Explicit envelope", "read_provenance": envelope}, "Private source text")
			if app.memorySourceAllowsRead(content) {
				t.Fatal("invalid or protected explicit provenance was accepted")
			}
		})
	}
	for _, content := range []string{
		"# Legacy ordinary note\n\nPlain local comparison",
		renderOKFDocument(map[string]any{"read_provenance": `{"version":1,"kinds":["conversation"]}`}, "Unprotected local comparison"),
		renderOKFDocument(map[string]any{"read_provenance": `{"version":2,"withheld":true}`}, "Connector local comparison"),
	} {
		if !app.memorySourceAllowsRead(content) {
			t.Fatal("internal recall inherited the separate MCP sharing restriction")
		}
	}
}
