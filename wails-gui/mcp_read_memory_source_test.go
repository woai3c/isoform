package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPReadTriggeredConnectorMemoryKeepsRealSource(t *testing.T) {
	a := newMCPReadTestApp(t)
	mc := NewMemoryGenerator(a)
	a.memoryGenerator = mc
	sources := []memorySource{memorySourceTelegram, memorySourceWeChat, memorySourceSlack, memorySourceDiscord, memorySourceFeishu, memorySourceTeams, memorySourceRingCentral}
	for _, source := range sources {
		thread := string(source) + ":owner:thread"
		summary, rounds := appendAndSummarizeConversation(a.store, thread, "private connector preference", "noted")
		a.triggerMemory(source, thread, summary, rounds)
		cs := mc.convSessions[thread]
		if cs == nil || cs.source != source || cs.sourceID != thread || cs.lastSummary != summary {
			t.Fatalf("trigger lost %s source: %+v", source, cs)
		}
		// Exercise the deterministic writer used after extraction. No model
		// or connector API is replaced; generation itself is outside this
		// source-propagation regression and no debounced task is started.
		id, err := mc.saveMemoryFile(cs.source, cs.sourceID, &mcMemoryGenerationResponse{Title: "Connector preference", MemoryPoints: []string{cs.lastSummary}, Dimension: "user_insight"})
		if err != nil {
			t.Fatal(err)
		}
		entry, err := a.GetMemoryEntry(id)
		if err != nil || entry.Source != string(source) || !strings.HasPrefix(id, string(source)+"-") || !memoryReadProvenanceFromContent(entry.Content).Withheld {
			t.Fatalf("wrong persisted source %s: %+v %v", source, entry, err)
		}
	}
	for _, tool := range []string{"memory_search", "memory_list"} {
		if got := callMCPReadTest(t, a, tool, map[string]any{"query": "private connector"}); len(got.Items) != 0 {
			t.Fatalf("connector thread exported by %s: %+v", tool, got)
		}
	}
	// Native Wails calls retain their signature and establish the corrected
	// native conversation source, independently of connector thread routing.
	a.TriggerMemory("native-session", "native preference", 1)
	cs := mc.convSessions["native-session"]
	if cs == nil || cs.source != memorySourceConversation {
		t.Fatal("native trigger source changed")
	}
	if _, err := mc.saveMemoryFile(cs.source, cs.sourceID, &mcMemoryGenerationResponse{Title: "Native preference", MemoryPoints: []string{cs.lastSummary}, Dimension: "user_insight"}); err != nil {
		t.Fatal(err)
	}
	if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "native preference"}); len(got.Items) != 1 {
		t.Fatalf("corrected native writer withheld: %+v", got)
	}
}

func TestMCPReadAmbiguousConversationProvenanceStaysWithheldAcrossWriters(t *testing.T) {
	a := newMCPReadTestApp(t)
	old := buildOKFMemoryMarkdown("legacy", "Legacy preference", time.Now(), memorySourceConversation, "user_insight", []string{"private legacy preference"})
	old = withMemoryReadProvenance(old, memoryReadProvenance{Version: 1, Kinds: []string{"conversation"}})
	writeMCPReadMarkdown(t, "legacy", old)
	writeMCPReadMarkdown(t, "old-dream", withMemoryReadProvenance(buildOKFMemoryMarkdown("old-dream", "Old synthesis", time.Now(), "dream-synthesis", "user_insight", []string{"private legacy preference"}), memoryReadProvenance{Version: 1, Kinds: []string{"conversation", "manual"}}))
	mc := NewMemoryGenerator(a)
	if err := mc.updateMemoryFile(a.ctx, "legacy", memorySourceConversation, "native-session", &mcMemoryGenerationResponse{Title: "Updated legacy", MemoryPoints: []string{"new native preference"}, Dimension: "user_insight"}, ""); err != nil {
		t.Fatal(err)
	}
	dream := &MemoryDreamService{app: a}
	if _, err := dream.writeSynthesizedMemory("New synthesis", "private legacy preference", "user_insight", []string{"legacy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveMemoryEntry("legacy", "# Edited\n\nprivate legacy preference"); err != nil {
		t.Fatal(err)
	}
	if got := callMCPReadTest(t, a, "memory_list", map[string]any{}); len(got.Items) != 0 || got.Withheld != 3 {
		t.Fatalf("legacy conversation laundering: %+v", got)
	}
	writeMCPReadMarkdown(t, "manual-v1", withMemoryReadProvenance(buildOKFMemoryMarkdown("manual-v1", "User text", time.Now(), "manual", "custom", []string{"explicit user text"}), memoryReadProvenance{Version: 1, Kinds: []string{"manual"}}))
	if got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "explicit user text"}); len(got.Items) != 1 {
		t.Fatal("unaffected manual provenance withheld")
	}
}

func TestMCPReadMemoryQueryPrecedesProvenanceBudget(t *testing.T) {
	a := newMCPReadTestApp(t)
	dir := filepath.Join(configDirOrFallback(), "memory")
	legacy := func(session, point string) (string, string) {
		seedMCPReadActivity(t, a, session, "Editor", "com.example.editor", "ordinary", "", "")
		id := activityMemoryEntryID(session)
		content := fmt.Sprintf("# Legacy\n\n- **Updated**: 2026-10-16 10:00\n- **Source**: activity\n- **Dimension**: user_insight\n- **Apps**: Editor\n- **Activity Session**: %s\n\n## Memory Points\n\n- %s\n", session, point)
		writeMCPReadMarkdown(t, id, content)
		return id, content
	}
	olderID, older := legacy("older-match", "specific target")
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, olderID+".md"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	olderHistory, err := memoryHistoryDir(dir, olderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(olderHistory, "1.md"), []byte(older), 0600); err != nil {
		t.Fatal(err)
	}
	newerID, newer := legacy("newer-unrelated", "unrelated context")
	newerHistory, err := memoryHistoryDir(dir, newerID)
	if err != nil {
		t.Fatal(err)
	}
	large := newer + strings.Repeat("x", (256<<10)-len(newer))
	for i := 0; i < 33; i++ {
		if err := os.WriteFile(filepath.Join(newerHistory, fmt.Sprintf("%02d.md", i)), []byte(large), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "specific target"})
	// History presence makes the partial flag query-independent, while
	// actual provenance work still skips the unrelated large history.
	if len(got.Items) != 1 || !got.Truncated || got.Bounded || got.Omitted != 0 {
		t.Fatalf("unmatched history starved target: %+v", got)
	}
	got = callMCPReadTest(t, a, "memory_list", map[string]any{})
	if !got.Truncated || got.Bounded || got.Omitted != 2 || got.Withheld != 0 {
		t.Fatalf("history exhaustion misreported: %+v", got)
	}
}

func TestMCPReadMessagingAndSensitiveSourcesStayExcluded(t *testing.T) {
	a := newMCPReadTestApp(t)
	for _, input := range []struct{ id, app, bundle string }{
		{"slack", "Slack", "com.tinyspeck.slackmacgap"},
		{"wechat", "微信", "com.tencent.xinWeChat"},
		{"telegram", "Telegram", "org.telegram.desktop"},
		{"ringcentral-name", "RingCentral", "com.example.communications"},
		{"ringcentral-bundle", "Renamed Communications", "com.ringcentral.glip"},
		{"ringcentral-prefix", "Renamed Communications", "com.ringcentral.ringcentral"},
		{"localized", "Renamed Chat", "com.hnc.Discord"},
		{"mail", "Mail", "com.apple.mail"},
		{"sensitive", "1Password", "com.1password.1password"},
	} {
		seedMCPReadActivity(t, a, input.id, input.app, input.bundle, "private source marker", "private source marker", "")
		writeMCPReadMemory(t, input.id, input.id, "private source marker")
	}
	for _, tool := range []string{"memory_search", "activity_search"} {
		present := callMCPReadTest(t, a, tool, map[string]any{"query": "private source marker"})
		absent := callMCPReadTest(t, a, tool, map[string]any{"query": "absent marker"})
		one, _ := json.Marshal(present)
		two, _ := json.Marshal(absent)
		if len(present.Items) != 0 || string(one) != string(two) {
			t.Fatalf("private messaging query oracle %s: %s vs %s", tool, one, two)
		}
	}
}
