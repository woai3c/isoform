//go:build !race

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTopicActivitiesLargeFixtureUnderBudget(t *testing.T) {
	a := largeTopicRegistryFixtureSized(t, topicColdFixtureSessions)
	for _, query := range []string{
		`UPDATE activity_sessions SET start_time=start_time-(rowid%42)*86400000,end_time=end_time-(rowid%42)*86400000 WHERE id LIKE 'capture-%'`,
		`UPDATE activities SET start_ms=start_ms-(rowid%42)*86400000,end_ms=end_ms-(rowid%42)*86400000 WHERE id LIKE 'load-%'`,
	} {
		if _, err := a.activity.store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	svc := a.topicsSvc()
	now := svc.now()
	state := svc.store.load()
	measure := func() time.Duration {
		t.Helper()
		cache := &a.activity.store.readPrivacySources
		cache.mu.Lock()
		cache.entries = nil
		cache.mu.Unlock()
		started := time.Now()
		if err := svc.refreshRegistryNames(t.Context(), now); err != nil {
			t.Fatal(err)
		}
		rows, _, truncated, err := a.topicRegistryActivitiesLimited(t.Context(), now, state.Projects)
		elapsed := time.Since(started)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != topicRegistryActivityLimit || !truncated {
			t.Fatalf("activities=%d truncated=%t", len(rows), truncated)
		}
		for i := 1; i < len(rows); i++ {
			previous, current := rows[i-1], rows[i]
			if topicActivityAt(previous) < topicActivityAt(current) || topicActivityAt(previous) == topicActivityAt(current) && previous.ID < current.ID {
				t.Fatalf("activity order at %d: %q before %q", i, previous.ID, current.ID)
			}
		}
		return elapsed
	}
	if _, err := a.activity.store.db.Exec(`DROP INDEX idx_activities_end_id`); err != nil {
		t.Fatal(err)
	}
	before := measure()
	if _, err := a.activity.store.db.Exec(`CREATE INDEX idx_activities_end_id ON activities(end_ms DESC, id DESC)`); err != nil {
		t.Fatal(err)
	}
	after := measure()
	t.Logf("activities=%d fixture=%d window=14d fixture_days=42 before=%s after=%s", topicRegistryActivityLimit, topicColdFixtureSessions, before, after)
	if after >= 2*time.Second {
		t.Fatalf("activities step exceeded budget: %s", after)
	}

	rowsSQL, err := a.activity.store.reader().QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+topicRegistryRecentQuery, now.Add(-topicWindow).UnixMilli(), now.UnixMilli(), topicRegistryActivityLimit+1)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsSQL.Close()
	usesIndex := false
	for rowsSQL.Next() {
		var id, parent, unused int
		var detail string
		if err := rowsSQL.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		usesIndex = usesIndex || strings.Contains(detail, "idx_activities_end_id")
		if strings.Contains(strings.ToUpper(detail), "TEMP B-TREE") {
			t.Error("recent activity query sorts the whole window")
		}
	}
	if err := rowsSQL.Err(); err != nil {
		t.Fatal(err)
	}
	if !usesIndex {
		t.Fatal("recent activity query does not use idx_activities_end_id")
	}
}

func TestTopicActivitiesIndexMigrationRepairsExistingDatabase(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	store := a.activity.store
	if _, err := store.db.Exec(`DROP INDEX idx_activities_end_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM migration_log WHERE name='activity_end_id_index'`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_activities_end_id'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("activity end/id index was not repaired")
	}
}

func TestTopicActivitiesIndexedLimitKeepsIDOrder(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	store := a.activity.store
	now := a.topicsSvc().now().UnixMilli()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < topicRegistryActivityLimit+8; i++ {
		id := fmt.Sprintf("tied-%04d", i)
		if _, err := tx.Exec(`INSERT INTO activities(id,day_start_ms,status,title,latest_change,start_ms,end_ms,created_at,updated_at) VALUES(?,0,'done','Code','Tied',?,?,?,?)`, id, now-1, now, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cursor, err := store.reader().QueryContext(t.Context(), `SELECT id FROM activities WHERE end_ms>=? AND start_ms<=? ORDER BY end_ms DESC,id DESC LIMIT ?`, now-topicWindow.Milliseconds(), now, topicRegistryActivityLimit+1)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	ids := []string{}
	for cursor.Next() {
		var id string
		if err := cursor.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != topicRegistryActivityLimit+1 {
		t.Fatalf("activities=%d", len(ids))
	}
	if ids[0] != fmt.Sprintf("tied-%04d", topicRegistryActivityLimit+7) {
		t.Fatalf("first tied activity=%q", ids[0])
	}
}
