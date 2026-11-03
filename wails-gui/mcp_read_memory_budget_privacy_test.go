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

func TestMCPReadMemorySearchBudgetDoesNotRevealPrivateMatch(t *testing.T) {
	for _, budget := range []string{"history", "session_events", "session_snapshots", "session_checks"} {
		t.Run(budget, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			const session = "private-budget-source"
			at := seedMCPReadActivity(t, a, session, "Editor", "com.example.editor", "ordinary work", "", "")
			switch budget {
			case "history":
				id := activityMemoryEntryID(session)
				content := fmt.Sprintf("# Legacy\n\n- **Updated**: 2026-10-16 10:00\n- **Source**: activity\n- **Dimension**: user_insight\n- **Apps**: Editor\n- **Activity Session**: %s\n\n## Memory Points\n\n- hidden marker\n", session)
				writeMCPReadMarkdown(t, id, content)
				history, err := memoryHistoryDir(filepath.Join(configDirOrFallback(), "memory"), id)
				if err != nil {
					t.Fatal(err)
				}
				large := content + strings.Repeat("x", (256<<10)-len(content))
				for i := 0; i < 33; i++ {
					if err := os.WriteFile(filepath.Join(history, fmt.Sprintf("%02d.md", i)), []byte(large), 0600); err != nil {
						t.Fatal(err)
					}
				}
				// A connector revision beyond the history budget makes the
				// current activity-labeled memory private even after a full scan.
				private := buildOKFMemoryMarkdown(id, "Private source", time.Now(), memorySourceSlack, "user_insight", []string{"hidden marker"})
				if err := os.WriteFile(filepath.Join(history, "99-private.md"), []byte(private), 0600); err != nil {
					t.Fatal(err)
				}
			case "session_events":
				writeMCPReadMemory(t, "private-budget-memory", session, "hidden marker")
				// The existing first event plus these rows exceed the per-session
				// budget before the final private-browser capture can be checked.
				if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<10000)
					INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order)
					SELECT ?,?+i,'test',CASE i WHEN 10000 THEN '{"appBundleId":"com.apple.Safari","windowTitle":"Private Browsing"}'
					ELSE '{"appBundleId":"com.example.editor","windowTitle":"ordinary work"}' END,i FROM n`, session, at); err != nil {
					t.Fatal(err)
				}
			case "session_snapshots":
				writeMCPReadMemory(t, "private-budget-memory", session, "hidden marker")
				if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<10001)
                    INSERT INTO activity_snapshots(session_id,timestamp,file_path,app,parsed_json)
                    SELECT ?,?+i,'','Editor','{}' FROM n`, session, at); err != nil {
					t.Fatal(err)
				}
			case "session_checks":
				// Absent source IDs also consume validation work, so existing
				// database row counts alone cannot bound all memory dependencies.
				if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<999)
					INSERT INTO activity_sessions(id,start_time,end_time,trigger_kind,app_names,event_count,segment_count,created_at)
					SELECT printf('budget-source-%03d',i),?,?+60000,'test','["Editor"]',1,0,? FROM n`, at, at, at); err != nil {
					t.Fatal(err)
				}
				if _, err := a.activity.store.db.Exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order)
					SELECT id,start_time,'test','{"appBundleId":"com.example.editor","windowTitle":"ordinary work"}',0
					FROM activity_sessions WHERE id LIKE 'budget-source-%'`); err != nil {
					t.Fatal(err)
				}
				if _, err := a.activity.store.db.Exec(`UPDATE activity_events SET data='{"appBundleId":"com.apple.Safari","windowTitle":"Private Browsing"}' WHERE session_id=?`, session); err != nil {
					t.Fatal(err)
				}
				writeMCPReadMemory(t, "z-budget-memory", session, "hidden marker")
				older := time.Now().Add(-time.Hour)
				if err := os.Chtimes(filepath.Join(configDirOrFallback(), "memory", "z-budget-memory.md"), older, older); err != nil {
					t.Fatal(err)
				}
				var sources []string
				for i := 1; i <= 999; i++ {
					sources = append(sources, fmt.Sprintf("budget-source-%03d", i))
				}
				sources = append(sources, "missing-source")
				content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Unresolved source", "source": "activity", "dimension": "topic_highlight", "read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 1, Kinds: []string{"activity"}, Sessions: sources})}, "hidden marker")
				writeMCPReadMarkdown(t, "a-budget-spender", content)
			}
			present := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "hidden marker"})
			absent := callMCPReadTest(t, a, "memory_search", map[string]any{"query": "absent marker"})
			one, err := json.Marshal(present)
			if err != nil {
				t.Fatal(err)
			}
			two, err := json.Marshal(absent)
			if err != nil {
				t.Fatal(err)
			}
			if len(present.Items) != 0 || string(one) != string(two) {
				t.Fatalf("private memory budget query oracle: %s vs %s", one, two)
			}
			if !present.Truncated || present.Bounded || present.Omitted != 0 || present.Withheld != 0 {
				t.Fatalf("budget potential must be reported without private match counts: %+v", present)
			}
		})
	}
}

func TestMCPReadMemoryKindBudgetDoesNotRevealPrivateMatch(t *testing.T) {
	a := newMCPReadTestApp(t)
	const privateSession = "kind-private-source"
	at := seedMCPReadActivity(t, a, privateSession, "Google Chrome", "com.google.Chrome", "Private project - Incognito", "", "")
	// More than 1000 real readable references precede the private reference.
	// This memory can never be returned, but checking it reaches the budget
	// before its final private-window dependency can be inspected.
	if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1001)
		INSERT INTO activity_sessions(id,start_time,end_time,trigger_kind,app_names,event_count,segment_count,created_at)
		SELECT printf('kind-source-%04d',i),?,?+60000,'test','["Editor"]',1,0,? FROM n`, at, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activity.store.db.Exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order)
		SELECT id,start_time,'test','{"appBundleId":"com.example.editor","windowTitle":"ordinary work"}',0
		FROM activity_sessions WHERE id LIKE 'kind-source-%'`); err != nil {
		t.Fatal(err)
	}
	sources := make([]string, 0, 1002)
	for i := 1; i <= 1001; i++ {
		sources = append(sources, fmt.Sprintf("kind-source-%04d", i))
	}
	sources = append(sources, privateSession)
	content := renderOKFDocument(map[string]any{
		"type": "Isoform Memory", "title": "Private dependency", "source": "activity", "dimension": "user_insight",
		"read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: sources}),
	}, "hidden kind marker")
	writeMCPReadMarkdown(t, "kind-private-memory", content)
	for _, tc := range []struct {
		tool    string
		present map[string]any
		absent  map[string]any
	}{
		{tool: "memory_list", present: map[string]any{"kind": "user_insight"}, absent: map[string]any{"kind": "deep_user_model"}},
		{tool: "memory_search", present: map[string]any{"query": "hidden kind marker"}, absent: map[string]any{"query": "absent kind marker"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			present := callMCPReadTest(t, a, tc.tool, tc.present)
			absent := callMCPReadTest(t, a, tc.tool, tc.absent)
			one, err := json.Marshal(present)
			if err != nil {
				t.Fatal(err)
			}
			two, err := json.Marshal(absent)
			if err != nil {
				t.Fatal(err)
			}
			if len(present.Items) != 0 || string(one) != string(two) {
				t.Fatalf("private memory filter oracle: %s vs %s", one, two)
			}
			if !present.Truncated || present.Bounded || present.Omitted != 0 || present.Withheld != 0 {
				t.Fatalf("filtered budget metadata exposes private counts: %+v", present)
			}
		})
	}
	unfiltered := callMCPReadTest(t, a, "memory_list", map[string]any{})
	if len(unfiltered.Items) != 0 || !unfiltered.Truncated || unfiltered.Omitted != 1 || unfiltered.Withheld != 0 {
		t.Fatalf("unfiltered list lost its omission accounting: %+v", unfiltered)
	}
}

func TestMCPReadMemoryListStoreWideBoundMatchesKindFilter(t *testing.T) {
	a := newMCPReadTestApp(t)
	content := withMemoryReadProvenance(buildOKFMemoryMarkdown("manual-bound", "User text", time.Now(), "manual", "user_insight", []string{"explicit user text"}), memoryReadProvenance{Version: 2, Kinds: []string{"manual"}})
	writeMCPReadMarkdown(t, "manual-bound", content)
	for _, args := range []map[string]any{{}, {"kind": "user_insight"}, {"kind": "deep_user_model"}} {
		got := callMCPReadTest(t, a, "memory_list", args)
		if got.Truncated || !got.Bounded || got.Withheld != 0 || got.Omitted != 0 {
			t.Fatalf("small complete store reported a partial answer for %v: %+v", args, got)
		}
	}
	// Unrelated real sessions can exceed the store-wide privacy preflight
	// without consuming the manual memory's own source-validation budget.
	at := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1001)
		INSERT INTO activity_sessions(id,start_time,end_time,trigger_kind,app_names,event_count,segment_count,created_at)
		SELECT printf('unrelated-bound-%04d',i),?,?+60000,'test','["Editor"]',0,0,? FROM n`, at, at, at); err != nil {
		t.Fatal(err)
	}
	unfiltered := callMCPReadTest(t, a, "memory_list", map[string]any{})
	filtered := callMCPReadTest(t, a, "memory_list", map[string]any{"kind": "user_insight"})
	one, err := json.Marshal(unfiltered)
	if err != nil {
		t.Fatal(err)
	}
	two, err := json.Marshal(filtered)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfiltered.Items) != 1 || string(one) != string(two) {
		t.Fatalf("kind filter changed a store-wide completeness result: %s vs %s", one, two)
	}
	absent := callMCPReadTest(t, a, "memory_list", map[string]any{"kind": "deep_user_model"})
	for _, got := range []mcpReadResult{unfiltered, filtered, absent} {
		if !got.Truncated || got.Bounded || got.Withheld != 0 || got.Omitted != 0 || got.Reason != unfiltered.Reason {
			t.Fatalf("store-wide partial answer depends on the memory kind: %+v", got)
		}
	}
}

func TestMCPReadMemoryPrivacyBoundCountsSharedSessionsOnce(t *testing.T) {
	a := newMCPReadTestApp(t)
	at := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<200)
		INSERT INTO activity_sessions(id,start_time,end_time,trigger_kind,app_names,event_count,segment_count,created_at)
		SELECT printf('shared-bound-%03d',i),?,?+60000,'test','["Editor"]',1,0,? FROM n`, at, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activity.store.db.Exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order)
		SELECT id,start_time,'test','{"appBundleId":"com.example.editor","windowTitle":"ordinary work"}',0
		FROM activity_sessions WHERE id LIKE 'shared-bound-%'`); err != nil {
		t.Fatal(err)
	}
	sources := make([]string, 0, 200)
	for i := 1; i <= 200; i++ {
		sources = append(sources, fmt.Sprintf("shared-bound-%03d", i))
	}
	for i := 0; i < 6; i++ {
		// The same lineage appears in multiple memories and both metadata
		// fields. Normalized IDs match the actual request-scoped session cache.
		metadata := make([]string, 0, len(sources)+2)
		for _, id := range sources {
			metadata = append(metadata, " "+id+" ")
		}
		metadata = append(metadata, "", " ")
		content := renderOKFDocument(map[string]any{
			"type": "Isoform Memory", "title": "Shared readable context", "source": "activity", "dimension": "user_insight",
			"activity_sessions": metadata,
			"read_provenance":   memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: sources}),
		}, "shared readable memory")
		writeMCPReadMarkdown(t, fmt.Sprintf("shared-bound-memory-%d", i), content)
	}
	for _, args := range []map[string]any{{}, {"kind": "user_insight"}} {
		got := callMCPReadTest(t, a, "memory_list", args)
		if len(got.Items) != 6 || got.Truncated || !got.Bounded || got.Withheld != 0 || got.Omitted != 0 {
			t.Errorf("shared source references falsely exhausted privacy work for %v: %+v", args, got)
		}
	}
	p, err := a.mcpReadPrivacy(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := mcpReadResult{}
	if err := p.memoryRead(mcpReadArguments{Kind: "user_insight", Limit: 20}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 6 || p.sessionChecks != 200 || p.eventChecks != 200 || got.Truncated {
		t.Fatalf("preflight disagreed with real cached privacy work: items=%d sessions=%d events=%d truncated=%v", len(got.Items), p.sessionChecks, p.eventChecks, got.Truncated)
	}
}
