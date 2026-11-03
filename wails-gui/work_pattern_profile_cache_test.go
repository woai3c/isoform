package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func saveWorkPatternPrivacyFixture(
	t testing.TB,
	store *ActivityStore,
	id string,
	startAt int64,
) int64 {
	t.Helper()
	events := []activitymon.ActivityEvent{
		{
			Timestamp:   startAt,
			Kind:        activitymon.EventAppSwitch,
			AppBundleID: "test.safari",
			AppName:     "Safari",
			WindowTitle: "secret customer contract",
			TypedText:   "private typed text",
			SpeechText:  "confidential transcript",
		},
		{
			Timestamp:   startAt + 10_000,
			Kind:        activitymon.EventAppSwitch,
			AppBundleID: "test.terminal",
			AppName:     "Terminal",
			WindowTitle: "private repository",
		},
	}
	endAt := startAt + 20_000
	if err := store.SaveSession(ActivitySessionRow{
		ID:           id,
		StartTime:    startAt,
		EndTime:      endAt,
		TriggerKind:  "test",
		AppNames:     `[]`,
		EventCount:   len(events),
		TriageStatus: "done",
		CreatedAt:    endAt,
	}, events, nil); err != nil {
		t.Fatal(err)
	}
	return endAt
}

func TestWorkPatternProfileSessionCacheIsIncrementalAndRedacted(
	t *testing.T,
) {
	store := newTestStore(t)
	startAt := int64(10_000)
	cutoffAt := saveWorkPatternPrivacyFixture(
		t,
		store,
		"cache-session",
		startAt,
	)

	first, _, _, firstStats, err :=
		queryWorkPatternSourceEventsIncremental(
			store,
			1,
			cutoffAt,
		)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 ||
		firstStats.CacheHits != 0 ||
		firstStats.CacheMisses != 1 ||
		firstStats.RawEventRows != 2 {
		t.Fatalf("first query stats=%+v events=%d", firstStats, len(first))
	}
	var cachedJSON string
	if err := store.db.QueryRow(`
		SELECT events_json
		FROM work_pattern_profile_session_cache_v1
		WHERE session_id = 'cache-session'
	`).Scan(&cachedJSON); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"secret customer contract",
		"private typed text",
		"confidential transcript",
		"private repository",
	} {
		if strings.Contains(cachedJSON, forbidden) {
			t.Fatalf("cache retained private source content %q", forbidden)
		}
	}
	var rawBytes, cacheBytes int
	if err := store.db.QueryRow(`
		SELECT COALESCE(SUM(LENGTH(data)), 0)
		FROM activity_events
		WHERE session_id = 'cache-session'
	`).Scan(&rawBytes); err != nil {
		t.Fatal(err)
	}
	cacheBytes = len(cachedJSON)
	if cacheBytes >= rawBytes {
		t.Fatalf(
			"normalized cache grew beyond raw source: cache=%d raw=%d",
			cacheBytes,
			rawBytes,
		)
	}

	second, _, _, secondStats, err :=
		queryWorkPatternSourceEventsIncremental(
			store,
			1,
			cutoffAt,
		)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 ||
		secondStats.CacheHits != 1 ||
		secondStats.RawEventRows != 0 {
		t.Fatalf(
			"cached query stats=%+v events=%d",
			secondStats,
			len(second),
		)
	}

	appended := activitymon.ActivityEvent{
		Timestamp:   cutoffAt - 1,
		Kind:        activitymon.EventAppSwitch,
		AppBundleID: "test.notes",
		AppName:     "Notes",
	}
	raw, err := json.Marshal(appended)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO activity_events (
			session_id, timestamp, kind, data, sort_order
		) VALUES (?, ?, ?, ?, ?)`,
		"cache-session",
		appended.Timestamp,
		string(appended.Kind),
		string(raw),
		2,
	); err != nil {
		t.Fatal(err)
	}
	third, _, _, thirdStats, err :=
		queryWorkPatternSourceEventsIncremental(
			store,
			1,
			cutoffAt,
		)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 3 ||
		thirdStats.CacheHits != 0 ||
		thirdStats.CacheMisses != 1 ||
		thirdStats.RawEventRows != 3 {
		t.Fatalf(
			"invalidated query stats=%+v events=%d",
			thirdStats,
			len(third),
		)
	}
}

func TestWorkPatternProfileSessionCachePurgesProtectedAndExpiredRows(
	t *testing.T,
) {
	store := newTestStore(t)
	cutoffAt := saveWorkPatternPrivacyFixture(
		t,
		store,
		"protected-cache-session",
		10_000,
	)
	if _, _, _, _, err :=
		queryWorkPatternSourceEventsIncremental(store, 1, cutoffAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE activity_sessions
		SET triage_status = 'protected'
		WHERE id = 'protected-cache-session'
	`); err != nil {
		t.Fatal(err)
	}
	events, _, exclusions, stats, err :=
		queryWorkPatternSourceEventsIncremental(store, 1, cutoffAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 ||
		exclusions.Protected != 1 ||
		stats.CacheRowsPurged != 1 {
		t.Fatalf(
			"protected cache query events=%d exclusions=%+v stats=%+v",
			len(events),
			exclusions,
			stats,
		)
	}
	var count int
	if err := store.db.QueryRow(`
		SELECT COUNT(*)
		FROM work_pattern_profile_session_cache_v1
		WHERE session_id = 'protected-cache-session'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("protected cache rows=%d, want 0", count)
	}

	oldEnd := cutoffAt -
		workPatternProfileCacheRetention.Milliseconds() - 1
	if _, err := store.db.Exec(`
		INSERT INTO work_pattern_profile_session_cache_v1 (
			session_id, policy_version, session_start, session_end,
			source_max_event_id, source_event_count, source_timestamp_sum,
			source_data_bytes, events_json, events_sha256, generated_at
		) VALUES (?, ?, ?, ?, 0, 0, 0, 0, '[]', ?, ?)`,
		"expired-cache-session",
		WorkPatternProfilePolicyVersion,
		oldEnd-1,
		oldEnd,
		workPatternHash([]byte("[]")),
		oldEnd,
	); err != nil {
		t.Fatal(err)
	}
	purged, err := purgeWorkPatternSessionCache(store, cutoffAt)
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("expired rows purged=%d, want 1", purged)
	}
}

func BenchmarkWorkPatternProfileCachedGeneration(b *testing.B) {
	store := newTestStore(b)
	cutoffAt := int64(0)
	for session := 0; session < 100; session++ {
		startAt := int64(10_000 + session*1_000_000)
		events := make([]activitymon.ActivityEvent, 0, 20)
		for event := 0; event < 20; event++ {
			app := "Safari"
			if event%2 == 1 {
				app = "Terminal"
			}
			events = append(events, activitymon.ActivityEvent{
				Timestamp:   startAt + int64(event)*10_000,
				Kind:        activitymon.EventAppSwitch,
				AppBundleID: "test." + strings.ToLower(app),
				AppName:     app,
			})
		}
		cutoffAt = startAt + int64(len(events))*10_000
		if err := store.SaveSession(ActivitySessionRow{
			ID:           "benchmark-" + strconv.Itoa(session),
			StartTime:    startAt,
			EndTime:      cutoffAt,
			TriggerKind:  "benchmark",
			AppNames:     `[]`,
			EventCount:   len(events),
			TriageStatus: "done",
			CreatedAt:    cutoffAt,
		}, events, nil); err != nil {
			b.Fatal(err)
		}
	}
	if _, _, err := store.EnsureWorkPatternProfileSnapshot(
		cutoffAt,
		cutoffAt+1,
	); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, _, err := store.EnsureWorkPatternProfileSnapshot(
			cutoffAt,
			cutoffAt+1,
		); err != nil {
			b.Fatal(err)
		}
	}
}
