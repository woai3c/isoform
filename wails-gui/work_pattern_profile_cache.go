package main

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

const (
	workPatternProfileMaximumSessions = 2_000
	workPatternProfileCacheRetention  = 45 * 24 * time.Hour
)

type workPatternSourceSession struct {
	ID                 string
	StartAt            int64
	EndAt              int64
	MaxEventID         int64
	EventCount         int
	EventTimestampSum  int64
	EventDataByteCount int64
}

type workPatternSourceQueryStats struct {
	SessionCount    int
	CacheHits       int
	CacheMisses     int
	RawEventRows    int
	CacheRowsPurged int64
}

func queryWorkPatternSourceSessions(
	store *ActivityStore,
	windowStart, cutoffAt int64,
) ([]workPatternSourceSession, bool, error) {
	rows, err := store.db.Query(`
		SELECT
			s.id,
			s.start_time,
			s.end_time,
			MAX(e.id),
			COUNT(e.id),
			COALESCE(SUM(e.timestamp), 0),
			COALESCE(SUM(LENGTH(e.data)), 0)
		FROM activity_sessions s
		INNER JOIN activity_events e ON e.session_id = s.id
		WHERE e.timestamp >= ?
			AND e.timestamp <= ?
			AND COALESCE(s.triage_status, '') != 'protected'
			AND NOT EXISTS (
				SELECT 1
				FROM first_run_exclusions_v1 exclusion
				WHERE exclusion.source_kind = 'activity_session'
					AND exclusion.source_id = s.id
			)
		GROUP BY s.id, s.start_time, s.end_time
		ORDER BY MAX(e.timestamp) DESC, s.id ASC
		LIMIT ?`,
		windowStart,
		cutoffAt,
		workPatternProfileMaximumSessions+1,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]workPatternSourceSession, 0)
	for rows.Next() {
		var item workPatternSourceSession
		if err := rows.Scan(
			&item.ID,
			&item.StartAt,
			&item.EndAt,
			&item.MaxEventID,
			&item.EventCount,
			&item.EventTimestampSum,
			&item.EventDataByteCount,
		); err != nil {
			return nil, false, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(result) > workPatternProfileMaximumSessions
	if truncated {
		result = result[:workPatternProfileMaximumSessions]
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].StartAt != result[j].StartAt {
			return result[i].StartAt < result[j].StartAt
		}
		return result[i].ID < result[j].ID
	})
	return result, truncated, nil
}

func workPatternSessionCacheEligible(
	session workPatternSourceSession,
	windowStart, cutoffAt int64,
) bool {
	return session.StartAt >= windowStart &&
		session.EndAt > session.StartAt &&
		session.EndAt <= cutoffAt
}

func loadWorkPatternSessionCache(
	store *ActivityStore,
	session workPatternSourceSession,
) ([]workPatternSourceEvent, bool) {
	var (
		startAt, endAt                int64
		maxEventID                    int64
		eventCount                    int
		eventTimestampSum, eventBytes int64
		eventsJSON, eventsSHA256      string
	)
	err := store.db.QueryRow(`
		SELECT
			session_start, session_end, source_max_event_id,
			source_event_count, source_timestamp_sum, source_data_bytes,
			events_json, events_sha256
		FROM work_pattern_profile_session_cache_v1
		WHERE session_id = ? AND policy_version = ?`,
		session.ID,
		WorkPatternProfilePolicyVersion,
	).Scan(
		&startAt,
		&endAt,
		&maxEventID,
		&eventCount,
		&eventTimestampSum,
		&eventBytes,
		&eventsJSON,
		&eventsSHA256,
	)
	if err != nil {
		return nil, false
	}
	if startAt != session.StartAt ||
		endAt != session.EndAt ||
		maxEventID != session.MaxEventID ||
		eventCount != session.EventCount ||
		eventTimestampSum != session.EventTimestampSum ||
		eventBytes != session.EventDataByteCount ||
		workPatternHash([]byte(eventsJSON)) != eventsSHA256 {
		return nil, false
	}
	var events []workPatternSourceEvent
	if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil {
		return nil, false
	}
	for _, event := range events {
		if event.SessionID != session.ID ||
			event.RowID <= 0 ||
			event.Timestamp < session.StartAt ||
			event.Timestamp > session.EndAt ||
			strings.TrimSpace(event.AppKey) == "" {
			return nil, false
		}
	}
	return events, true
}

func queryWorkPatternSessionEvents(
	store *ActivityStore,
	session workPatternSourceSession,
	windowStart, cutoffAt int64,
) ([]workPatternSourceEvent, int, error) {
	rows, err := store.db.Query(`
		SELECT id, timestamp, data
		FROM activity_events
		WHERE session_id = ?
			AND timestamp >= ?
			AND timestamp <= ?
		ORDER BY timestamp ASC, id ASC`,
		session.ID,
		windowStart,
		cutoffAt,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]workPatternSourceEvent, 0, session.EventCount)
	rawRows := 0
	for rows.Next() {
		var (
			rowID, occurredAt int64
			raw               string
		)
		if err := rows.Scan(&rowID, &occurredAt, &raw); err != nil {
			return nil, rawRows, err
		}
		rawRows++
		var event activitymon.ActivityEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			continue
		}
		appKey := normalizeWorkPatternAppKey(
			event.AppBundleID,
			event.AppName,
		)
		if appKey == "" {
			continue
		}
		result = append(result, workPatternSourceEvent{
			RowID:        rowID,
			SessionID:    session.ID,
			SessionStart: session.StartAt,
			SessionEnd:   session.EndAt,
			Timestamp:    occurredAt,
			AppKey:       appKey,
			AppName:      workPatternDisplayAppName(event.AppName),
		})
	}
	return result, rawRows, rows.Err()
}

func storeWorkPatternSessionCache(
	store *ActivityStore,
	session workPatternSourceSession,
	events []workPatternSourceEvent,
	generatedAt int64,
) error {
	payload, err := json.Marshal(events)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`
		INSERT INTO work_pattern_profile_session_cache_v1 (
			session_id, policy_version, session_start, session_end,
			source_max_event_id, source_event_count, source_timestamp_sum,
			source_data_bytes, events_json, events_sha256, generated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, policy_version) DO UPDATE SET
			session_start = excluded.session_start,
			session_end = excluded.session_end,
			source_max_event_id = excluded.source_max_event_id,
			source_event_count = excluded.source_event_count,
			source_timestamp_sum = excluded.source_timestamp_sum,
			source_data_bytes = excluded.source_data_bytes,
			events_json = excluded.events_json,
			events_sha256 = excluded.events_sha256,
			generated_at = excluded.generated_at`,
		session.ID,
		WorkPatternProfilePolicyVersion,
		session.StartAt,
		session.EndAt,
		session.MaxEventID,
		session.EventCount,
		session.EventTimestampSum,
		session.EventDataByteCount,
		string(payload),
		workPatternHash(payload),
		generatedAt,
	)
	return err
}

func purgeWorkPatternSessionCache(
	store *ActivityStore,
	cutoffAt int64,
) (int64, error) {
	retentionCutoff := cutoffAt -
		workPatternProfileCacheRetention.Milliseconds()
	result, err := store.db.Exec(`
		DELETE FROM work_pattern_profile_session_cache_v1
		WHERE policy_version != ?
			OR session_end < ?
			OR session_id IN (
				SELECT id
				FROM activity_sessions
				WHERE COALESCE(triage_status, '') = 'protected'
			)
			OR session_id IN (
				SELECT source_id
				FROM first_run_exclusions_v1
				WHERE source_kind = 'activity_session'
			)`,
		WorkPatternProfilePolicyVersion,
		retentionCutoff,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func queryWorkPatternSourceExclusions(
	store *ActivityStore,
	windowStart, cutoffAt int64,
) (workPatternSourceExclusionCounts, error) {
	result := workPatternSourceExclusionCounts{}
	if err := store.db.QueryRow(`
		SELECT COUNT(DISTINCT s.id)
		FROM activity_sessions s
		WHERE s.start_time <= ?
			AND s.end_time >= ?
			AND COALESCE(s.triage_status, '') = 'protected'`,
		cutoffAt,
		windowStart,
	).Scan(&result.Protected); err != nil {
		return workPatternSourceExclusionCounts{}, err
	}
	if err := store.db.QueryRow(`
		SELECT COUNT(DISTINCT s.id)
		FROM activity_sessions s
		INNER JOIN first_run_exclusions_v1 exclusion
			ON exclusion.source_kind = 'activity_session'
			AND exclusion.source_id = s.id
		WHERE s.start_time <= ?
			AND s.end_time >= ?`,
		cutoffAt,
		windowStart,
	).Scan(&result.FirstRun); err != nil {
		return workPatternSourceExclusionCounts{}, err
	}
	return result, nil
}

func queryWorkPatternSourceEventsIncremental(
	store *ActivityStore,
	windowStart, cutoffAt int64,
) (
	[]workPatternSourceEvent,
	bool,
	workPatternSourceExclusionCounts,
	workPatternSourceQueryStats,
	error,
) {
	if store == nil || store.db == nil {
		return nil, false, workPatternSourceExclusionCounts{},
			workPatternSourceQueryStats{},
			errors.New("activity store is unavailable")
	}
	stats := workPatternSourceQueryStats{}
	if purged, err := purgeWorkPatternSessionCache(
		store,
		cutoffAt,
	); err == nil {
		stats.CacheRowsPurged = purged
	}
	sessions, truncated, err := queryWorkPatternSourceSessions(
		store,
		windowStart,
		cutoffAt,
	)
	if err != nil {
		events, legacyTruncated, exclusions, legacyErr :=
			queryWorkPatternSourceEventsLegacy(store, windowStart, cutoffAt)
		return events, legacyTruncated, exclusions, stats, legacyErr
	}
	stats.SessionCount = len(sessions)
	events := make([]workPatternSourceEvent, 0)
	for _, session := range sessions {
		if workPatternSessionCacheEligible(session, windowStart, cutoffAt) {
			if cached, ok := loadWorkPatternSessionCache(
				store,
				session,
			); ok {
				stats.CacheHits++
				events = append(events, cached...)
				continue
			}
		}
		stats.CacheMisses++
		sessionEvents, rawRows, err := queryWorkPatternSessionEvents(
			store,
			session,
			windowStart,
			cutoffAt,
		)
		if err != nil {
			return nil, false, workPatternSourceExclusionCounts{},
				stats, err
		}
		stats.RawEventRows += rawRows
		events = append(events, sessionEvents...)
		if workPatternSessionCacheEligible(
			session,
			windowStart,
			cutoffAt,
		) {
			_ = storeWorkPatternSessionCache(
				store,
				session,
				sessionEvents,
				cutoffAt,
			)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Timestamp != events[j].Timestamp {
			return events[i].Timestamp < events[j].Timestamp
		}
		return events[i].RowID < events[j].RowID
	})
	if len(events) > workPatternProfileMaximumEvents {
		truncated = true
		events = events[len(events)-workPatternProfileMaximumEvents:]
	}
	exclusions, err := queryWorkPatternSourceExclusions(
		store,
		windowStart,
		cutoffAt,
	)
	if err != nil {
		return nil, false, workPatternSourceExclusionCounts{},
			stats, err
	}
	return events, truncated, exclusions, stats, nil
}

func queryWorkPatternSourceEvents(
	store *ActivityStore,
	windowStart, cutoffAt int64,
) ([]workPatternSourceEvent, bool, workPatternSourceExclusionCounts, error) {
	events, truncated, exclusions, _, err :=
		queryWorkPatternSourceEventsIncremental(
			store,
			windowStart,
			cutoffAt,
		)
	return events, truncated, exclusions, err
}
