package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"wails-gui/internal/platform/activitymon"
)

// activityRetentionSource distinguishes a retention deletion from an unknown
// source. Only opaque identity and the exclusion decision survive: app names,
// times, membership, titles and summaries expire with the original activity.
// Later privacy-setting changes cannot be re-evaluated against deleted data.
type activityRetentionSource struct {
	Kind     string
	ID       string
	Excluded bool
}

func migrateActivityRetentionSources(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS activity_retention_sources_v1 (
		source_kind TEXT NOT NULL CHECK (source_kind IN ('activity_session', 'activity_run')),
		source_id TEXT NOT NULL,
		excluded INTEGER NOT NULL CHECK (excluded IN (0, 1)),
		PRIMARY KEY (source_kind, source_id)
	)`)
	return err
}

// Legacy receipts did not fold app exclusion into the persisted decision.
// Strip the historical usage metadata and conservatively exclude those rows.
func migrateMinimalActivityRetentionSources(tx *sql.Tx) error {
	var legacyColumns int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('activity_retention_sources_v1')
		WHERE name NOT IN ('source_kind', 'source_id', 'excluded')`).Scan(&legacyColumns); err != nil {
		return err
	}
	if legacyColumns == 0 {
		return migrateActivityRetentionSources(tx)
	}
	if _, err := tx.Exec(`ALTER TABLE activity_retention_sources_v1 RENAME TO activity_retention_sources_legacy`); err != nil {
		return err
	}
	if err := migrateActivityRetentionSources(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO activity_retention_sources_v1 (source_kind, source_id, excluded)
		SELECT source_kind, source_id, 1 FROM activity_retention_sources_legacy`); err != nil {
		return err
	}
	_, err := tx.Exec(`DROP TABLE activity_retention_sources_legacy`)
	return err
}

func (s *ActivityStore) getActivityRetentionSource(kind, id string) (*activityRetentionSource, error) {
	row := activityRetentionSource{Kind: kind, ID: id}
	err := s.reader().QueryRow(`SELECT excluded FROM activity_retention_sources_v1
		WHERE source_kind=? AND source_id=?`, kind, id).Scan(&row.Excluded)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func recordActivityRetentionSource(tx *sql.Tx, row activityRetentionSource) error {
	_, err := tx.Exec(`INSERT INTO activity_retention_sources_v1 (source_kind, source_id, excluded)
		VALUES (?, ?, ?) ON CONFLICT(source_kind, source_id) DO UPDATE SET
		excluded = MAX(activity_retention_sources_v1.excluded, excluded.excluded)`, row.Kind, row.ID, row.Excluded)
	return err
}

// Retention uses persisted exclusion signals. The observation-pause log is
// short lived and mutable; consulting it at a thirty-day purge would freeze
// toggle history or a transient read error into a permanent privacy decision.
func activityRetentionSessionExcluded(appNames string, protected bool) bool {
	if protected {
		return true
	}
	var apps []string
	if json.Unmarshal([]byte(appNames), &apps) != nil {
		return true
	}
	for _, app := range apps {
		if activityEvidenceAppExcluded("", app) {
			return true
		}
	}
	return false
}

// deleteSessionForRetention is the one retention deletion path. deleteSession
// writes both receipts in the deleting transaction: the retained source privacy
// receipt (summary provenance) and the opaque retention receipt below. A failed
// deletion therefore never makes an unknown source look purged, and readers
// observe the privacy revision change around it.
func (s *ActivityStore) deleteSessionForRetention(id string) error {
	return s.deleteSession(id, true)
}

// recordSessionRetentionReceipt writes the opaque retention receipt of a
// session inside the transaction that deletes it. A missing session writes none.
func recordSessionRetentionReceipt(tx *sql.Tx, id string) error {
	row := activityRetentionSource{Kind: "activity_session", ID: id}
	var start, end int64
	var apps string
	var protected bool
	err := tx.QueryRow(`SELECT start_time, end_time, app_names, triage_status='protected'
		FROM activity_sessions WHERE id=?`, id).Scan(&start, &end, &apps, &protected)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	row.Excluded = activityRetentionSessionExcluded(apps, protected)
	// Bound candidates by the session span before decoding membership, as
	// GetTimelineRunsForSessions does. A purge must not parse every old run.
	rows, err := tx.Query(`SELECT run.app_key, run.app_name, run.session_ids,
		run.evidence_excluded, COALESCE(summary.title, '') FROM activity_timeline_groups run
		LEFT JOIN activity_timeline_group_summaries summary ON summary.group_id=run.id
		WHERE run.end_ms >= ? AND run.start_ms <= ?`, start, end)
	if err != nil {
		return err
	}
	for rows.Next() {
		var appKey, appName, sessionJSON, title string
		var excluded bool
		if err := rows.Scan(&appKey, &appName, &sessionJSON, &excluded, &title); err != nil {
			rows.Close()
			return err
		}
		var sessions []string
		if json.Unmarshal([]byte(sessionJSON), &sessions) != nil {
			row.Excluded = true
			continue
		}
		for _, sessionID := range sessions {
			if sessionID == id {
				row.Excluded = row.Excluded || excluded || activityEvidenceAppExcluded(appKey, appName) || activitymon.IsPrivateBrowsing(title)
				break
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return recordActivityRetentionSource(tx, row)
}

// Every transaction touches at most this many runs, including the privacy
// preparation pass. ActivityStore has one connection; committing each page
// allows queued foreground readers and writers to proceed during a backlog.
const activityRetentionBatchSize = 32

type timelineRetentionSource struct {
	receipt           activityRetentionSource
	sessions          []string
	start, end, rowID int64
	membershipUnknown bool
	unavailable       bool
}

type timelineRetentionCursor struct {
	end, rowID int64
}

func loadTimelineRetentionBatch(tx *sql.Tx, endMsBefore int64, after *timelineRetentionCursor) ([]timelineRetentionSource, error) {
	query := `SELECT run.id, run.start_ms, run.end_ms, run.rowid, run.app_key, run.app_name,
		run.session_ids, run.evidence_excluded, COALESCE(summary.title, ''),
		EXISTS (SELECT 1 FROM activity_retention_unavailable_v1 unavailable
			WHERE unavailable.source_kind='activity_run' AND unavailable.source_id=run.id)
		FROM activity_timeline_groups run
		LEFT JOIN activity_timeline_group_summaries summary ON summary.group_id=run.id
		WHERE run.end_ms < ?`
	args := []any{endMsBefore}
	if after != nil {
		query += ` AND (run.end_ms, run.rowid) > (?, ?)`
		args = append(args, after.end, after.rowID)
	}
	query += ` ORDER BY run.end_ms, run.rowid LIMIT ?`
	args = append(args, activityRetentionBatchSize)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []timelineRetentionSource
	for rows.Next() {
		row := timelineRetentionSource{receipt: activityRetentionSource{Kind: "activity_run"}}
		var appKey, appName, sessionJSON, title string
		if err := rows.Scan(&row.receipt.ID, &row.start, &row.end, &row.rowID, &appKey, &appName, &sessionJSON, &row.receipt.Excluded, &title, &row.unavailable); err != nil {
			return nil, err
		}
		row.receipt.Excluded = row.receipt.Excluded || activityEvidenceAppExcluded(appKey, appName) || activitymon.IsPrivateBrowsing(title)
		if json.Unmarshal([]byte(sessionJSON), &row.sessions) != nil || len(row.sessions) == 0 {
			row.receipt.Excluded = true
			row.membershipUnknown = true
		}
		for _, id := range row.sessions {
			if strings.TrimSpace(id) == "" {
				row.receipt.Excluded = true
				row.membershipUnknown = true
			}
		}
		sources = append(sources, row)
	}
	return sources, rows.Err()
}

// Propagate privacy and unavailable evidence separately before deleting
// membership. Repeated bounded passes reach a fixed point across shared sessions
// even when a private or unavailable run is in a later page and its neighbors
// are connected only transitively. No pass keeps the connection beyond a page.
func (s *ActivityStore) prepareTimelineRetention(endMsBefore int64) error {
	for {
		changed := false
		var after *timelineRetentionCursor
		for {
			next, count, pageChanged, err := s.prepareTimelineRetentionBatch(endMsBefore, after)
			if err != nil {
				return err
			}
			changed = changed || pageChanged
			if count < activityRetentionBatchSize {
				break
			}
			after = &next
		}
		if !changed {
			return nil
		}
	}
}

func (s *ActivityStore) prepareTimelineRetentionBatch(endMsBefore int64, after *timelineRetentionCursor) (timelineRetentionCursor, int, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return timelineRetentionCursor{}, 0, false, err
	}
	defer tx.Rollback()
	sources, err := loadTimelineRetentionBatch(tx, endMsBefore, after)
	if err != nil {
		return timelineRetentionCursor{}, 0, false, err
	}
	changed, err := preserveTimelineRetentionPrivacy(tx, sources)
	if err != nil {
		return timelineRetentionCursor{}, 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return timelineRetentionCursor{}, 0, false, err
	}
	var next timelineRetentionCursor
	if len(sources) > 0 {
		last := sources[len(sources)-1]
		next = timelineRetentionCursor{end: last.end, rowID: last.rowID}
	}
	return next, len(sources), changed, nil
}

func preserveTimelineRetentionPrivacy(tx *sql.Tx, sources []timelineRetentionSource) (bool, error) {
	changed := false
	execChange := func(query string, args ...any) error {
		result, err := tx.Exec(query, args...)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		changed = changed || count > 0
		return err
	}
	for i := range sources {
		row := &sources[i]
		if row.membershipUnknown {
			if err := execChange(`UPDATE activity_sessions SET triage_status='protected'
				WHERE end_time >= ? AND start_time <= ? AND triage_status!='protected'`, row.start, row.end); err != nil {
				return false, fmt.Errorf("preserve unknown retained run privacy: %w", err)
			}
		}
		for _, id := range row.sessions {
			var unavailable bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM activity_retention_unavailable_v1
				WHERE source_kind='activity_session' AND source_id=?)`, id).Scan(&unavailable); err != nil {
				return false, err
			}
			row.unavailable = row.unavailable || unavailable
			var apps string
			var protected bool
			err := tx.QueryRow(`SELECT app_names, triage_status='protected'
				FROM activity_sessions WHERE id=?`, id).Scan(&apps, &protected)
			switch err {
			case nil:
				row.receipt.Excluded = row.receipt.Excluded || activityRetentionSessionExcluded(apps, protected)
			case sql.ErrNoRows:
				var excluded bool
				err := tx.QueryRow(`SELECT excluded FROM activity_retention_sources_v1
					WHERE source_kind='activity_session' AND source_id=?`, id).Scan(&excluded)
				if err != nil && err != sql.ErrNoRows {
					return false, err
				}
				row.receipt.Excluded = row.receipt.Excluded || excluded
				row.unavailable = row.unavailable || err == sql.ErrNoRows
			default:
				return false, err
			}
		}
		// Missing historical evidence is not a privacy decision. Preserve its
		// opaque availability marker before deleting membership, including on
		// surviving members and existing receipts reached through shared runs.
		if row.unavailable {
			if err := execChange(`INSERT OR IGNORE INTO activity_retention_unavailable_v1
				(source_kind, source_id) VALUES ('activity_run', ?)`, row.receipt.ID); err != nil {
				return false, err
			}
			for _, id := range row.sessions {
				if err := execChange(`INSERT OR IGNORE INTO activity_retention_unavailable_v1
					(source_kind, source_id) VALUES ('activity_session', ?)`, id); err != nil {
					return false, err
				}
			}
		}
		if !row.receipt.Excluded {
			continue
		}
		if err := execChange(`UPDATE activity_timeline_groups SET evidence_excluded=1
			WHERE id=? AND evidence_excluded=0`, row.receipt.ID); err != nil {
			return false, err
		}
		for _, id := range row.sessions {
			if err := execChange(`UPDATE activity_sessions SET triage_status='protected'
				WHERE id=? AND triage_status!='protected'`, id); err != nil {
				return false, fmt.Errorf("preserve retained session privacy: %w", err)
			}
			if err := execChange(`UPDATE activity_retention_sources_v1 SET excluded=1
				WHERE source_kind='activity_session' AND source_id=? AND excluded=0`, id); err != nil {
				return false, err
			}
		}
	}
	return changed, nil
}

func (s *ActivityStore) purgeTimelineRetentionBatch(endMsBefore int64) (int, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	sources, err := loadTimelineRetentionBatch(tx, endMsBefore, nil)
	if err != nil {
		return 0, false, err
	}
	// A foreground writer can change a member between preparation pages.
	// Revalidate in the deletion transaction. If privacy changed, retain all
	// membership and restart preparation so exclusions can cross other pages.
	changed, err := preserveTimelineRetentionPrivacy(tx, sources)
	if err != nil {
		return 0, false, err
	}
	if changed {
		return 0, true, tx.Commit()
	}
	for _, row := range sources {
		if err := recordActivityRetentionSource(tx, row.receipt); err != nil {
			return 0, false, fmt.Errorf("record retained run %s: %w", row.receipt.ID, err)
		}
		if _, err := tx.Exec(`DELETE FROM activity_timeline_group_summaries WHERE group_id=?`, row.receipt.ID); err != nil {
			return 0, false, fmt.Errorf("purge timeline run summaries: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM activity_timeline_groups WHERE id=?`, row.receipt.ID); err != nil {
			return 0, false, fmt.Errorf("purge timeline runs: %w", err)
		}
	}
	return len(sources), false, tx.Commit()
}
