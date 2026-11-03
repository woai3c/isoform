package main

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"wails-gui/storage"
)

func replaceTopicRegistryWriter(t *testing.T, app *App, mode string) {
	t.Helper()
	db := app.activity.store.db
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var exists bool
	if err := conn.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM sqlite_temp_master WHERE type='table' AND name='topic_registry_revision')`).Scan(&exists); err != nil || !exists {
		t.Fatalf("observer missing before replacement: exists=%t err=%v", exists, err)
	}
	switch mode {
	case "bad_connection":
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatalf("discard connection: %v", err)
		}
	case "query_deadline":
		// Run real SQLite work on the pinned writer, so the deadline must
		// interrupt an executing statement rather than time out in the pool.
		if _, err := conn.ExecContext(t.Context(), `CREATE TEMP TABLE topic_writer_deadline_marker(value INTEGER)`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		var sum int64
		err := conn.QueryRowContext(ctx, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n`).Scan(&sum)
		if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("writer query was not canceled by its deadline: %v", err)
		}
		t.Logf("canceled writer query kind=%s", topicRegistryErrorKind(err))
	default:
		t.Fatalf("unknown replacement mode %q", mode)
	}
	_ = conn.Close()
	// Losing the TEMP marker proves a real physical connection replacement.
	// The *sql.DB and persistent schema remain exactly the same.
	if err := db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM sqlite_temp_master WHERE type='table' AND name='topic_registry_revision')`).Scan(&exists); err != nil || exists {
		t.Fatalf("writer connection was not replaced: observer exists=%t err=%v", exists, err)
	}
	if mode == "query_deadline" {
		if err := db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM sqlite_temp_master WHERE name='topic_writer_deadline_marker')`).Scan(&exists); err != nil || exists {
			t.Fatalf("deadline did not discard the writer's TEMP state: exists=%t err=%v", exists, err)
		}
	}
}

func TestTopicRegistryWriterConnectionReplacementRecoversReads(t *testing.T) {
	for _, mode := range []string{"bad_connection", "query_deadline"} {
		t.Run(mode, func(t *testing.T) {
			app, _, _ := registryChatTestApp(t)
			before, err := collectAndReadTopicViewsForTest(app)
			if err != nil || len(before) != 1 {
				t.Fatalf("initial views: %d %v", len(before), err)
			}
			replaceTopicRegistryWriter(t, app, mode)
			for attempt := 0; attempt < 3; attempt++ {
				after, err := collectAndReadTopicViewsForTest(app)
				if err != nil || len(after) != 1 || after[0].ID != before[0].ID || after[0].Name != before[0].Name {
					t.Fatalf("read %d after writer replacement: %+v %v", attempt, after, err)
				}
			}
		})
	}
}

func TestTopicRegistryWriterConnectionReplacementInvalidatesCache(t *testing.T) {
	for _, mode := range []string{"bad_connection", "query_deadline"} {
		t.Run(mode, func(t *testing.T) {
			app, _, now := registryChatTestApp(t)
			if views, err := collectAndReadTopicViewsForTest(app); err != nil || len(views) != 1 {
				t.Fatalf("initial views: %d %v", len(views), err)
			}
			svc := app.topicsSvc()
			old := svc.loadRegistryCache()
			replaceTopicRegistryWriter(t, app, mode)
			// A write before the new observer is installed has no TEMP source
			// counter. Both connections can report Source=0/data_version=2.
			const latest = "Rebuilt after writer replacement"
			if _, err := app.activity.store.db.ExecContext(t.Context(), `UPDATE activities SET latest_change=? WHERE id='atlas'`, latest); err != nil {
				t.Fatal(err)
			}
			collections := 0
			svc.registryCollectContext = func(parent context.Context) (context.Context, context.CancelFunc) {
				collections++
				return parent, func() {}
			}
			views, err := collectAndReadTopicViewsForTest(app)
			if err != nil {
				t.Fatal(err)
			}
			if collections != 1 || len(views) != 1 || views[0].Latest == nil || views[0].Latest.Text != latest {
				t.Fatalf("pre-replacement cache served: collections=%d views=%+v", collections, views)
			}
			current := svc.loadRegistryCache()
			if current == old || topicRegistryCacheMatches(old, current.revision, svc.store.load(), now) {
				t.Fatal("pre-replacement cache proof matched the recreated observer")
			}
			if current.revision.activitySources.Source != old.revision.activitySources.Source || current.revision.activitySources.External != old.revision.activitySources.External {
				t.Fatalf("fixture did not exercise colliding connection-local stamps: before=%+v after=%+v", old.revision.activitySources, current.revision.activitySources)
			}
			release, err := lockTopicActivityRevision(t.Context(), app.activity.store.db, old.revision.activitySources)
			if release != nil {
				release()
			}
			if !errors.Is(err, storage.ErrReadRevisionChanged) {
				t.Fatalf("old publication proof survived replacement: %v", err)
			}
		})
	}
}
