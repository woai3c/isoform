package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

// Opt in with -run '^$' -bench '^BenchmarkSearchEverythingLargeStores$'
// -benchtime=1x. Setup is excluded; all databases/files belong to the fixture.
func BenchmarkSearchEverythingLargeStores(b *testing.B) {
	b.Setenv("HOME", b.TempDir())
	root := b.TempDir()
	store, err := storage.New(filepath.Join(root, "sessions.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	activity, err := NewActivityStore(root)
	if err != nil {
		b.Fatal(err)
	}
	defer activity.Close()
	a := &App{ctx: context.Background(), store: store, activity: &activityMonitorState{store: activity}}
	ctx := b.Context()
	if err := store.EnsureProjectStructure(ctx, storage.ProjectRow{ID: "p", Path: root, Name: "Fixture"}, []storage.WorkspaceRow{{ID: "w", ProjectID: "p", Label: "Default", IsDefault: true}}, nil); err != nil {
		b.Fatal(err)
	}
	if err := store.SaveSession(ctx, storage.SessionRow{LocalID: "main", ProjectID: "p", WorkspaceID: "w", Label: "Main conversation", Kind: "main"}); err != nil {
		b.Fatal(err)
	}
	items := make([]storage.StreamItemRow, 100000)
	for i := range items {
		items[i] = storage.StreamItemRow{ID: fmt.Sprint("message-", i), Kind: "user", Content: "Discuss the Isoform release plan", CreatedAt: int64(1000 + i)}
	}
	if err := store.SaveStreamItems(ctx, "main", items); err != nil {
		b.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UnixMilli()
	if err := activity.SaveSession(ActivitySessionRow{ID: "capture", AppNames: `["Editor"]`, StartTime: at, EndTime: at + 60000, CreatedAt: at, TriageStatus: "dismissed"}, []activitymon.ActivityEvent{{Timestamp: at, AppName: "Editor", AppBundleID: "com.example.editor", WindowTitle: "Release"}}, nil); err != nil {
		b.Fatal(err)
	}
	var run string
	if err := activity.db.QueryRow(`SELECT id FROM activity_timeline_groups LIMIT 1`).Scan(&run); err != nil {
		run = "fixture-run"
		if _, err := activity.db.Exec(`INSERT INTO activity_timeline_groups(id,first_event_row_id,last_event_row_id,app_key,app_name,start_ms,end_ms,active_ms,event_count,session_ids,trigger_kinds,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run, 1, 1, "com.example.editor", "Editor", at, at+60000, 60000, 1, `["capture"]`, `[]`, "closed", at, at); err != nil {
			b.Fatal(err)
		}
	}
	tx, err := activity.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO activities(id,day_start_ms,status,title,description,latest_change,category,category_color,entities,run_ids,active_ms_by_app,start_ms,end_ms,continues_activity_id,created_at,updated_at) VALUES(?,0,'settled','Isoform release','Summary','','','','[]',?,'{}',?,?,'',?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 50000; i++ {
		if _, err := stmt.Exec(fmt.Sprint("moment-", i), `["`+run+`"]`, at, at+60000, at, at); err != nil {
			b.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	// No production directories are created by the search.
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), configDirName)); !os.IsNotExist(err) {
		b.Fatalf("unexpected state directory: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := a.SearchEverything("isoform", 5)
		if err != nil || len(result.FailedPlaces) != 0 {
			b.Fatalf("search failed: %v %s", err, strings.Join(result.FailedPlaces, ","))
		}
		counts := map[string]int{}
		for _, g := range result.Groups {
			counts[g.Place] = len(g.Results)
		}
		if counts["chat"] != 5 || counts["activity"] != 5 {
			b.Fatalf("fixture results: %v", counts)
		}
	}
}
