package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPReadSparseSearchBoundedRows(t *testing.T) {
	testMCPReadSparseSearchStore(t, 1003, 10)
}

// The 10-million-event fixture is an explicit scale check, not a default
// unit-suite workload. The small fixture above retains the sparse-match and
// provenance scan bounds on every run.
func TestMCPReadSparseSearchLargeStore(t *testing.T) {
	if testing.Short() || os.Getenv("YANSU_MCP_SCALE_TEST") != "1" {
		t.Skip("set YANSU_MCP_SCALE_TEST=1 without -short to run the 10-million-event scale check")
	}
	testMCPReadSparseSearchStore(t, 1003, 10000)
}

// A real SQLite fixture uses compact rows and one INSERT SELECT. The oldest
// readable match sits behind more than 1000 unrelated sessions.
func testMCPReadSparseSearchStore(t *testing.T, sessions, eventsPerSession int) {
	t.Helper()
	a := newMCPReadTestApp(t)
	base := time.Now().Add(-4 * time.Hour).UnixMilli()
	seedStarted := time.Now()
	tx, err := a.activity.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?)
		INSERT INTO activity_sessions(id,start_time,end_time,trigger_kind,app_names,event_count,segment_count,created_at)
		SELECT printf('scale-%04d',i),?+i*10000,?+i*10000+9999,'test','["Editor"]',?,0,?+i*10000 FROM n`, sessions-1, base, base, eventsPerSession, base); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?)
		INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order)
		SELECT s.id,s.start_time+n.i,'test',CASE s.id
		WHEN 'scale-0000' THEN '{"appBundleId":"x","windowTitle":"needle"}'
		WHEN 'scale-0001' THEN '{"appBundleId":"com.apple.Safari","windowTitle":"needle (Private Browsing)"}'
		ELSE '{"appBundleId":"x","windowTitle":"other"}' END,n.i
		FROM activity_sessions s CROSS JOIN n WHERE s.id LIKE 'scale-%'`, eventsPerSession-1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Seed principal metadata against the real sessions database without
	// starting any resolver or ingestion service.
	db, err := sql.Open("sqlite", filepath.Join(configDirOrFallback(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?)
		INSERT INTO principals(id,kind,display_name,created_from,confidence,created_at,updated_at)
		SELECT printf('person-%04d',i),'contact',CASE WHEN i<2 THEN 'needle person' ELSE 'unrelated person' END,'activity',1,?,? FROM n`, sessions-1, base, base); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?)
		INSERT INTO principal_session_sightings(principal_id,session_id,first_seen_at,last_seen_at)
		SELECT printf('person-%04d',i),printf('scale-%04d',i),?,? FROM n`, sessions-1, base, base); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d sessions and %d events in %s", sessions, sessions*eventsPerSession, time.Since(seedStarted))
	for _, tool := range []string{"activity", "people"} {
		t.Run(tool, func(t *testing.T) {
			// Work counters prove the bounds; the timeout only guards hangs.
			ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
			defer cancel()
			p, err := a.mcpReadPrivacy(ctx)
			if err != nil {
				t.Fatal(err)
			}
			result := mcpReadResult{}
			started := time.Now()
			switch tool {
			case "activity":
				err = p.activitySearch(mcpReadArguments{Query: "needle", Limit: 20}, &result)
			case "people":
				err = p.peopleLookup(mcpReadArguments{Name: "needle", Limit: 20}, &result)
			}
			elapsed := time.Since(started)
			t.Logf("%s query=%s checked_sessions=%d decoded_events=%d", tool, elapsed, p.sessionChecks, p.eventChecks)
			if err != nil {
				t.Fatalf("bounded query failed: %v", err)
			}
			if s := &a.activity.store.readPrivacySources; s.sourceScans.Load() > 2 || s.eventRows.Load() > uint64(2*eventsPerSession) {
				t.Fatalf("shared source gate scanned unrelated captures: scans=%d events=%d", s.sourceScans.Load(), s.eventRows.Load())
			}
			if len(result.Items) != 1 || result.Withheld != 0 || !result.Truncated || p.sessionChecks != 2 || p.eventChecks > eventsPerSession+1 {
				t.Fatalf("query checked unrelated sources or exposed private match: result=%+v checks=%d events=%d", result, p.sessionChecks, p.eventChecks)
			}
		})
	}
}

// Invoke the real scale test in short mode so a missing fixture gate cannot
// silently bring ten million database rows into a quick test run.
func TestMCPReadSparseSearchLargeStoreSkipsShort(t *testing.T) {
	t.Setenv("YANSU_MCP_SCALE_TEST", "1")
	previous := flag.Lookup("test.short").Value.String()
	if err := flag.Set("test.short", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := flag.Set("test.short", previous); err != nil {
			t.Fatal(err)
		}
	})
	skipped := false
	t.Run("scale_fixture", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		TestMCPReadSparseSearchLargeStore(t)
	})
	if !skipped {
		t.Fatal("the large-store fixture must skip in short mode")
	}
}

func TestMCPReadSearchPreservesEscapedTextAndLocalTimeOffsets(t *testing.T) {
	a := newMCPReadTestApp(t)
	seedMCPReadActivity(t, a, "escaped", "Editor", "com.example.editor", "ordinary", "", "")
	if _, err := a.activity.store.db.Exec(`UPDATE activity_events SET data='{"appBundleId":"x","windowTitle":"\u006e\u0065\u0065\u0064\u006c\u0065"}' WHERE session_id='escaped'`); err != nil {
		t.Fatal(err)
	}
	got := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "needle"})
	if len(got.Items) != 1 {
		t.Fatalf("JSON escape prefilter lost match: %+v", got)
	}
	raw, _ := json.Marshal(got)
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["timezone"]; exists {
		t.Fatal("ambiguous timezone label remains")
	}
	parsed, err := time.Parse(time.RFC3339, got.Items[0].AtLocal)
	if err != nil || parsed.UnixMilli() != got.Items[0].At {
		t.Fatalf("timestamp offset lost: %+v", got.Items[0])
	}
}
