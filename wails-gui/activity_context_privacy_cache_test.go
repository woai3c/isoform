package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"yansu-agent/agent"
)

func TestDeletingSourceInvalidatesPersistedAndRecentSummaryCaches(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	day := time.Now().Add(-time.Minute)
	dateKey := day.Format("2006-01-02")
	weekKey := day.AddDate(0, 0, -(int(day.Weekday())+6)%7).Format("2006-01-02") + "/week"
	unrelatedDate := day.AddDate(0, 0, -8).Format("2006-01-02")
	for _, session := range []ActivitySessionRow{
		{ID: "safe", StartTime: day.UnixMilli(), EndTime: day.Add(time.Minute).UnixMilli(), AppNames: `["Notes"]`},
		{ID: "private", StartTime: day.UnixMilli(), EndTime: day.Add(time.Minute).UnixMilli(), AppNames: `["Private Notes"]`},
	} {
		if err := store.SaveSession(session, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, language := range []string{"", "en", "zh-CN"} {
		for _, cache := range []struct{ key, kind string }{{dateKey, "daily"}, {weekKey, "weekly"}, {unrelatedDate, "daily"}} {
			if _, err := store.SaveSummaryWithLanguage(cache.key, cache.kind, "private-derived-cache", "test", false, language); err != nil {
				t.Fatal(err)
			}
		}
	}
	cache := newActivityContextCache()
	cache.summary, cache.generatedAt = "private-derived-cache", time.Now()
	cache.privacy, _ = app.activityContextPrivacy()
	cache.generating = true // Exercise invalidation without starting a model job.
	if app.activityDayAllowsModelRead(dateKey) {
		t.Fatal("mixed sensitive/public day must veto cached derivatives")
	}
	revision := store.readPrivacyRevision.Load()
	if err := store.DeleteSession("private"); err != nil {
		t.Fatal(err)
	}
	if store.readPrivacyRevision.Load() == revision || store.readPrivacyRevision.Load()%2 != 0 {
		t.Fatal("successful deletion did not advance the stable privacy revision")
	}
	if !app.activityDayAllowsModelRead(dateKey) {
		t.Fatal("safe source should remain after private source deletion")
	}
	for _, language := range []string{"", "en", "zh-CN"} {
		for _, item := range []struct{ key, kind string }{{dateKey, "daily"}, {weekKey, "weekly"}} {
			if _, _, _, _, found := store.GetCachedSummaryForLanguage(item.key, item.kind, language); found {
				t.Errorf("deleted source left %s %s cached in %q", item.kind, item.key, language)
			}
		}
		if _, _, _, _, found := store.GetCachedSummaryForLanguage(unrelatedDate, "daily", language); !found {
			t.Error("unrelated day's cache was removed")
		}
	}
	if got := cache.getSummary(app); got != "" {
		t.Fatalf("deleted source survived in the recent context cache: %s", got)
	}
	var published bool
	if err := store.withSummarySources(activitySummarySourceSet{Revision: revision, Sources: []activitySourceProvenance{{SessionID: "private"}}}, func() error { published = true; return nil }); err == nil || published {
		t.Fatal("in-flight model result restored a cache after source deletion")
	}
}

// Observe real SQLite connection attempts instead of relying on a wall-clock
// deadline that competes with other workers on the test machine.
type activityContextCountingConnector struct {
	driver driver.Driver
	path   string
	count  *atomic.Int64
}

func (c activityContextCountingConnector) Connect(context.Context) (driver.Conn, error) {
	c.count.Add(1)
	return c.driver.Open(c.path)
}

func (c activityContextCountingConnector) Driver() driver.Driver { return c.driver }

func TestRecentActivityCacheHitDoesNotReadTheDatabase(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	cache := newActivityContextCache()
	cache.summary, cache.generatedAt = "safe cached note", time.Now()
	cache.privacy, _ = app.activityContextPrivacy()
	original := store.db
	var sequence int
	var name, path string
	if err := original.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	connections := &atomic.Int64{}
	counting := sql.OpenDB(activityContextCountingConnector{driver: original.Driver(), path: path, count: connections})
	store.db = counting
	t.Cleanup(func() {
		store.db = original
		_ = counting.Close()
	})
	if got := cache.getSummary(app); got != "safe cached note" {
		t.Fatalf("fresh cache = %q", got)
	}
	if got := connections.Load(); got != 0 {
		t.Fatalf("chat cache hit opened %d SQL connections", got)
	}
	// An old cached result cannot cross a switch to another source store.
	app.activity.store = &ActivityStore{}
	cache.generating = true
	if got := cache.getSummary(app); got != "" {
		t.Fatalf("cache survived source-store replacement: %q", got)
	}
}

type activityContextPrivacyMutationRunner struct {
	memoryTranslationFakeRunner
	mutate func()
}

func (r *activityContextPrivacyMutationRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.mutate()
	return r.memoryTranslationFakeRunner.ExecuteTask(ctx, input, callback)
}

func TestRecentActivityGenerationRejectsPrivacyChangesDuringModelPass(t *testing.T) {
	for _, mutation := range []string{"protection", "deletion", "store-replacement"} {
		t.Run(mutation, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			store := newTestStore(t)
			app := &App{activity: &activityMonitorState{store: store}}
			now := time.Now()
			if err := store.SaveSession(ActivitySessionRow{ID: "source", StartTime: now.Add(-time.Minute).UnixMilli(), EndTime: now.UnixMilli(), AppNames: `["Notes"]`}, nil, nil); err != nil {
				t.Fatal(err)
			}
			runner := &activityContextPrivacyMutationRunner{
				memoryTranslationFakeRunner: memoryTranslationFakeRunner{response: "stale-private-note", ready: true},
				mutate: func() {
					switch mutation {
					case "protection":
						pii.AddUserSensitiveApp("com.apple.Notes", "Notes")
					case "deletion":
						if err := store.DeleteSession("source"); err != nil {
							t.Fatal(err)
						}
					case "store-replacement":
						app.activity.store = &ActivityStore{}
					}
				},
			}
			app.runner = runner
			cache := newActivityContextCache()
			if got := cache.regenerate(app); got != "" || strings.Contains(cache.summary, "stale-private-note") {
				t.Fatalf("model pass published across privacy change: result=%q cache=%q", got, cache.summary)
			}
			if runner.calls != 1 {
				t.Fatalf("model calls = %d, want 1", runner.calls)
			}
		})
	}
}
