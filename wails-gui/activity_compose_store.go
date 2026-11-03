package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// ActivityRow is one persisted activity bucket (layer 3): a coherent piece of
// work composed from per-app timeline runs. JSON columns are stored as raw
// strings; the App binding parses them into ActivityView. Status is one of
// activityStatus{Active,Idle,Paused,Abandoned,Settled}.
type ActivityRow struct {
	ThreadOutcome       *ActivityThreadOutcome // Additive observed end state, not a lifecycle or authority signal.
	threadOutcomeRecord string
	ID                  string
	DayStartMs          int64
	Status              string
	Title               string
	Description         string
	LatestChange        string
	Category            string
	CategoryColor       string
	Entities            string // JSON array of {type,name}
	RunIDs              string // JSON ordered array of timeline-run ids
	ActiveMsByApp       string // JSON object {appName: ms}
	StartMs             int64
	EndMs               int64
	ContinuesActivityID string
	CreatedAt           int64
	UpdatedAt           int64
	PausedAtMs          int64
	EvidenceTokens      []ActivityEvidenceToken // Loaded from member runs; not duplicated in activities.
}

const (
	activityStatusActive    = "active"
	activityStatusIdle      = "idle"
	activityStatusPaused    = "paused"
	activityStatusAbandoned = "abandoned"
	activityStatusSettled   = "settled"

	activityPauseAfterMs   = int64(6 * time.Hour / time.Millisecond)
	activityAbandonAfterMs = int64(72 * time.Hour / time.Millisecond)
)

const activityColumns = `id, day_start_ms, status, title, description, latest_change,
	category, category_color, entities, run_ids, active_ms_by_app,
	start_ms, end_ms, continues_activity_id, created_at, updated_at, paused_at_ms, thread_outcome`

func scanActivityRows(rows *sql.Rows) ([]ActivityRow, error) {
	return scanActivityRowsContext(context.Background(), rows)
}

func scanActivityRowsContext(ctx context.Context, rows *sql.Rows) ([]ActivityRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []ActivityRow
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var r ActivityRow
		if err := rows.Scan(
			&r.ID, &r.DayStartMs, &r.Status, &r.Title, &r.Description, &r.LatestChange,
			&r.Category, &r.CategoryColor, &r.Entities, &r.RunIDs, &r.ActiveMsByApp,
			&r.StartMs, &r.EndMs, &r.ContinuesActivityID, &r.CreatedAt, &r.UpdatedAt, &r.PausedAtMs,
			&r.threadOutcomeRecord,
		); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		var record activityThreadOutcomeRecord
		if json.Unmarshal([]byte(r.threadOutcomeRecord), &record) == nil && record.Outcome != nil && record.Outcome.Version == threadOutcomeVersion {
			r.ThreadOutcome = redactThreadOutcome(record.Outcome)
		}
		out = append(out, r)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, rows.Err()
}

// InsertActivity persists a new bucket. Caller sets CreatedAt/UpdatedAt.
func (s *ActivityStore) InsertActivity(a ActivityRow) error {
	if a.Status == activityStatusActive || a.Status == activityStatusIdle {
		a.PausedAtMs = 0
	}
	_, err := s.db.Exec(`
		INSERT INTO activities (`+activityColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		a.ID, a.DayStartMs, a.Status, a.Title, a.Description, a.LatestChange,
		a.Category, a.CategoryColor, nonEmptyJSONArray(a.Entities), nonEmptyJSONArray(a.RunIDs),
		nonEmptyJSONObject(a.ActiveMsByApp), a.StartMs, a.EndMs, a.ContinuesActivityID,
		a.CreatedAt, a.UpdatedAt, a.PausedAtMs)
	if err != nil {
		return fmt.Errorf("insert activity %s: %w", a.ID, err)
	}
	return nil
}

// UpdateActivity overwrites a bucket by id. Caller sets UpdatedAt.
func (s *ActivityStore) UpdateActivity(a ActivityRow) error {
	if a.Status == activityStatusActive || a.Status == activityStatusIdle {
		a.PausedAtMs = 0
	}
	res, err := s.db.Exec(`
		UPDATE activities SET
			day_start_ms = ?, status = ?,
			title = CASE WHEN user_title != '' THEN user_title ELSE ? END,
			description = CASE WHEN user_description != '' THEN TRIM(user_description) ELSE ? END,
			latest_change = ?,
			category = ?, category_color = ?, entities = ?, run_ids = ?, active_ms_by_app = ?,
			start_ms = ?, end_ms = ?, continues_activity_id = ?, updated_at = ?, paused_at_ms = ?, thread_outcome = ''
		WHERE id = ?`,
		a.DayStartMs, a.Status, a.Title, a.Description, a.LatestChange,
		a.Category, a.CategoryColor, nonEmptyJSONArray(a.Entities), nonEmptyJSONArray(a.RunIDs),
		nonEmptyJSONObject(a.ActiveMsByApp), a.StartMs, a.EndMs, a.ContinuesActivityID,
		a.UpdatedAt, a.PausedAtMs, a.ID)
	if err != nil {
		return fmt.Errorf("update activity %s: %w", a.ID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("activity %s not found", a.ID)
	}
	return nil
}

// DeleteActivity removes a bucket and its run links (member runs survive).
func (s *ActivityStore) DeleteActivity(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM activities WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete activity %s: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM activity_run_links WHERE activity_id = ?`, id); err != nil {
		return fmt.Errorf("delete activity %s links: %w", id, err)
	}
	// The user's corrections quote the moment's text; a follow-up the user
	// added stays in the Purpose Map with its own words.
	if _, err := tx.Exec(`DELETE FROM activity_user_corrections WHERE activity_id = ?`, id); err != nil {
		return fmt.Errorf("delete activity %s corrections: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM activity_followups WHERE activity_id = ?`, id); err != nil {
		return fmt.Errorf("delete activity %s follow-up links: %w", id, err)
	}
	// "Isoform noticed" suggestions quote the moment too.
	if err := s.dropNoticedForMoment(tx, id); err != nil {
		return fmt.Errorf("delete activity %s suggestions: %w", id, err)
	}
	return tx.Commit()
}

// GetActivityByID returns one bucket or (nil, nil).
func (s *ActivityStore) GetActivityByID(id string) (*ActivityRow, error) {
	return s.GetActivityByIDContext(context.Background(), id)
}

func (s *ActivityStore) GetActivityByIDContext(ctx context.Context, id string) (*ActivityRow, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT `+activityColumns+` FROM activities WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("query activity by id: %w", err)
	}
	defer rows.Close()
	out, err := scanActivityRowsContext(ctx, rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// GetOpenActivitiesForDay returns the day's buckets still eligible to receive
// runs (active, idle or paused), newest-active first. This is the router's working
// set ("all of today's open buckets").
func (s *ActivityStore) GetOpenActivitiesForDay(dayStartMs int64) ([]ActivityRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+activityColumns+`
		FROM activities
		WHERE day_start_ms = ? AND status IN (?, ?, ?)
		ORDER BY end_ms DESC`, dayStartMs, activityStatusActive, activityStatusIdle, activityStatusPaused)
	if err != nil {
		return nil, fmt.Errorf("query open activities: %w", err)
	}
	defer rows.Close()
	return scanActivityRows(rows)
}

// GetLatestOpenActivityBefore returns the nearest same-day activity segment
// ending before a run starts. The segment composer only considers this one
// predecessor, keeping grouping local and time-contiguous.
func (s *ActivityStore) GetLatestOpenActivityBefore(dayStartMs, beforeMs int64) (*ActivityRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+activityColumns+`
		FROM activities
		WHERE day_start_ms = ? AND status IN (?, ?, ?) AND end_ms <= ?
		ORDER BY end_ms DESC
		LIMIT 1`, dayStartMs, activityStatusActive, activityStatusIdle, activityStatusPaused, beforeMs)
	if err != nil {
		return nil, fmt.Errorf("query latest open activity: %w", err)
	}
	defer rows.Close()
	out, err := scanActivityRows(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// GetUnsettledActivities returns every resumable activity. It deliberately has
// no time or count window: the candidate scorer applies the evidence windows.
func (s *ActivityStore) GetUnsettledActivities() ([]ActivityRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+activityColumns+`
		FROM activities
		WHERE status IN (?, ?, ?)
		ORDER BY end_ms DESC`, activityStatusActive, activityStatusIdle, activityStatusPaused)
	if err != nil {
		return nil, fmt.Errorf("query unsettled activities: %w", err)
	}
	defer rows.Close()
	return scanActivityRows(rows)
}

// GetActivitiesInRange returns buckets overlapping [startMs,endMs], newest
// first. endMs<=0 means no upper bound. Backs the Activities tab.
func (s *ActivityStore) GetActivitiesInRange(startMs, endMs int64) ([]ActivityRow, error) {
	return s.GetActivitiesInRangeContext(context.Background(), startMs, endMs)
}

func (s *ActivityStore) GetActivitiesInRangeContext(ctx context.Context, startMs, endMs int64) ([]ActivityRow, error) {
	upper := endMs
	if upper <= 0 {
		upper = int64(1) << 62
	}
	rows, err := s.reader().QueryContext(ctx, `
		SELECT `+activityColumns+`
		FROM activities
		WHERE end_ms >= ? AND start_ms <= ?
		ORDER BY end_ms DESC`, startMs, upper)
	if err != nil {
		return nil, fmt.Errorf("query activities in range: %w", err)
	}
	defer rows.Close()
	return scanActivityRows(rows)
}

// GetActivitiesBeforeDay returns up to limit buckets from days before
// dayStartMs (newest first), for cross-day continuation linking.
func (s *ActivityStore) GetActivitiesBeforeDay(dayStartMs int64, limit int) ([]ActivityRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+activityColumns+`
		FROM activities
		WHERE day_start_ms < ?
		ORDER BY end_ms DESC
		LIMIT ?`, dayStartMs, limit)
	if err != nil {
		return nil, fmt.Errorf("query prior-day activities: %w", err)
	}
	defer rows.Close()
	return scanActivityRows(rows)
}

// SettleActivitiesBeforeDay is retained for older callers. Background settling
// uses SettleActivities so crossing midnight does not end a resumable activity.
func (s *ActivityStore) SettleActivitiesBeforeDay(dayStartMs, nowMs int64) (int, error) {
	res, err := s.db.Exec(`
		UPDATE activities SET status = ?, updated_at = ?
		WHERE day_start_ms < ? AND status != ?`,
		activityStatusSettled, nowMs, dayStartMs, activityStatusSettled)
	if err != nil {
		return 0, fmt.Errorf("settle prior-day activities: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func activityStatusResumable(status string) bool {
	switch status {
	case activityStatusActive, activityStatusIdle, activityStatusPaused:
		return true
	default:
		return false
	}
}

// SettleActivities advances quiet activities without inferring a completion or
// changing their evidence. A linked run may have grown while its model summary
// is pending, so that run's latest evidence also postpones the timeout. The
// effective pause time is evidence + 6h, making catch-up after downtime stable.
func (s *ActivityStore) SettleActivities(nowMs int64, touched ...map[string]bool) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin activity settling: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`
		SELECT a.id, a.status, a.paused_at_ms, a.day_start_ms, a.start_ms, a.end_ms,
			MAX(a.end_ms, COALESCE((
				SELECT MAX(g.end_ms) FROM activity_run_links l
				JOIN activity_timeline_groups g ON g.id = l.run_id
				WHERE l.activity_id = a.id
			), 0))
		FROM activities a
		WHERE a.status IN (?, ?, ?)`, activityStatusActive, activityStatusIdle, activityStatusPaused)
	if err != nil {
		return 0, fmt.Errorf("query activity settling: %w", err)
	}
	type transition struct {
		id              string
		status          string
		pausedAt        int64
		evidenceMs      int64
		day, start, end int64
	}
	var changes []transition
	for rows.Next() {
		var id, status string
		var pausedAt, evidenceMs, day, start, end int64
		if err := rows.Scan(&id, &status, &pausedAt, &day, &start, &end, &evidenceMs); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan activity settling: %w", err)
		}
		nextStatus, nextPausedAt := status, pausedAt
		pauseAt := evidenceMs + activityPauseAfterMs
		// Preserve an explicitly recorded later pause, unless new evidence has
		// resumed the activity since that pause began.
		if status == activityStatusPaused && pausedAt > pauseAt {
			pauseAt = pausedAt
		}
		if nowMs >= pauseAt+activityAbandonAfterMs {
			nextStatus, nextPausedAt = activityStatusAbandoned, pauseAt
		} else if nowMs >= pauseAt {
			nextStatus, nextPausedAt = activityStatusPaused, pauseAt
		} else if status == activityStatusPaused {
			// New linked evidence can arrive before its summary/model pass.
			// Resume the bucket without inventing an outcome from quiet time.
			nextStatus, nextPausedAt = activityStatusActive, 0
			if nowMs-evidenceMs > activityIdleAfterMs {
				nextStatus = activityStatusIdle
			}
		}
		if nextStatus != status || nextPausedAt != pausedAt {
			changes = append(changes, transition{id: id, status: nextStatus, pausedAt: nextPausedAt, evidenceMs: evidenceMs, day: day, start: start, end: end})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate activity settling: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close activity settling rows: %w", err)
	}
	for _, change := range changes {
		if change.status == activityStatusAbandoned {
			if err := appendActivityOutcomeTx(tx, ActivityOutcome{
				ActivityID: change.id, Status: activityOutcomeAbandoned, Reason: "quiet_timeout",
				OccurredAt: change.pausedAt + activityAbandonAfterMs,
				Evidence: []ActivityOutcomeEvidence{{SourceKind: "activity_quiet_sweep", SourceID: change.id,
					ObservedAt: change.evidenceMs, Tokens: []ActivityEvidenceToken{}}},
			}, nowMs); err != nil {
				return 0, fmt.Errorf("record abandoned activity %s: %w", change.id, err)
			}
		}
		if _, err := tx.Exec(`UPDATE activities SET status = ?, paused_at_ms = ?, updated_at = ? WHERE id = ?`,
			change.status, change.pausedAt, nowMs, change.id); err != nil {
			return 0, fmt.Errorf("settle activity %s: %w", change.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit activity settling: %w", err)
	}
	if len(touched) > 0 {
		for _, change := range changes {
			activityAddRowDates(touched[0], ActivityRow{DayStartMs: change.day, StartMs: change.start, EndMs: change.end})
		}
	}
	return len(changes), nil
}

// settleActivities runs from the existing startup/periodic composer pass even
// when no model runner or ready runs are available.
func (c *ActivityTriager) settleActivities(store *ActivityStore, nowMs int64) {
	dates := map[string]bool{}
	n, err := store.SettleActivities(nowMs, dates)
	if err != nil {
		log.Printf("[activity-composer] settle activities: %v", err)
		return
	}
	if n > 0 && c.app != nil {
		c.app.emitActivityDateUpdate(dates)
	}
}

// UpsertActivityRunLink records that a run has been routed into a bucket at the
// given summarized-event count (so later growth re-triggers a refresh).
func (s *ActivityStore) UpsertActivityRunLink(runID, activityID string, composedEventCount int, nowMs int64) error {
	_, err := s.db.Exec(`
		INSERT INTO activity_run_links (run_id, activity_id, composed_event_count, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			activity_id = excluded.activity_id,
			composed_event_count = excluded.composed_event_count`,
		runID, activityID, composedEventCount, nowMs)
	if err != nil {
		return fmt.Errorf("upsert activity run link %s: %w", runID, err)
	}
	return nil
}

// RepointActivityRunLinks moves every run link from one bucket to another (used
// when the composer merges two buckets).
func (s *ActivityStore) RepointActivityRunLinks(fromActivityID, toActivityID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE activity_run_links SET activity_id = ? WHERE activity_id = ?`, toActivityID, fromActivityID); err != nil {
		return fmt.Errorf("repoint activity run links %s->%s: %w", fromActivityID, toActivityID, err)
	}
	if _, err := tx.Exec(`UPDATE activity_outcomes SET activity_id = ? WHERE activity_id = ?`, toActivityID, fromActivityID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO activity_run_corrections (id,activity_id,run_id,reason,created_at)
		SELECT id || ':merged:' || ?, ?, run_id, reason, created_at FROM activity_run_corrections WHERE activity_id=?`, toActivityID, toActivityID, fromActivityID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetComposerReadyRuns returns summarized runs that still need composing —
// never routed (no link) OR grown since they were last composed.
//
// Ordering is TODAY-FIRST then oldest-first: runs ending on/after todayStartMs
// come first (so the day the user is actually looking at populates promptly
// instead of waiting out a multi-day backfill), and within each priority band
// runs are chronological (start_ms ASC) so online bucketing builds correctly.
// sinceMs bounds the backfill to runs ending at/after it (0 = no bound), so
// first launch never fans out LLM calls over the entire run history.
func (s *ActivityStore) GetComposerReadyRuns(limit int, sinceMs, todayStartMs int64) ([]ActivityTimelineRunRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+timelineRunColumnsQualified("g")+`
		FROM activity_timeline_groups g
		LEFT JOIN activity_run_links l ON l.run_id = g.id
		WHERE g.summary_status = 'done'
		  AND (? = 0 OR g.end_ms >= ?)
		  AND (l.run_id IS NULL OR l.composed_event_count < g.summarized_event_count)
		ORDER BY CASE WHEN g.end_ms >= ? THEN 0 ELSE 1 END ASC, g.start_ms ASC
		LIMIT ?`, sinceMs, sinceMs, todayStartMs, limit)
	if err != nil {
		return nil, fmt.Errorf("query composer-ready runs: %w", err)
	}
	defer rows.Close()
	return scanTimelineRunRows(rows)
}

// GetActivityAppUsage returns, per activity id, its apps with bundle id, name,
// and summed active time — derived from the run links + run table so the
// front-end can resolve native app icons (keyed by bundle id) and per-app
// dwell time. Ranked by active time desc within each activity.
func (s *ActivityStore) GetActivityAppUsage(activityIDs []string) (map[string][]ActivityAppUsage, error) {
	out := map[string][]ActivityAppUsage{}
	if len(activityIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(activityIDs))
	args := make([]interface{}, len(activityIDs))
	for i, id := range activityIDs {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := s.reader().Query(`
		SELECT l.activity_id, g.id, g.app_key, g.app_name, g.active_ms, g.session_ids, g.evidence_excluded
		FROM activity_run_links l
		JOIN activity_timeline_groups g ON g.id = l.run_id
		WHERE l.activity_id IN (`+strings.Join(ph, ",")+`)
		ORDER BY g.start_ms, g.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query activity app usage: %w", err)
	}
	type member struct {
		activityID string
		run        ActivityTimelineRunRow
	}
	var members []member
	for rows.Next() {
		var item member
		var sessions string
		if err := rows.Scan(&item.activityID, &item.run.ID, &item.run.AppKey, &item.run.AppName, &item.run.ActiveMs, &sessions, &item.run.EvidenceExcluded); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan activity app usage: %w", err)
		}
		if err := json.Unmarshal([]byte(sessions), &item.run.SessionIDs); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode activity app usage sessions: %w", err)
		}
		members = append(members, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	byActivity := map[string]map[string]ActivityAppUsage{}
	for _, item := range members {
		allowed, err := s.timelineRunEvidenceAllowed(item.run)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if byActivity[item.activityID] == nil {
			byActivity[item.activityID] = map[string]ActivityAppUsage{}
		}
		u := byActivity[item.activityID][item.run.AppKey]
		u.AppKey, u.AppName = item.run.AppKey, item.run.AppName
		u.ActiveMs += item.run.ActiveMs
		byActivity[item.activityID][item.run.AppKey] = u
	}
	for id, apps := range byActivity {
		for _, usage := range apps {
			out[id] = append(out[id], usage)
		}
		sort.Slice(out[id], func(i, j int) bool {
			if out[id][i].ActiveMs != out[id][j].ActiveMs {
				return out[id][i].ActiveMs > out[id][j].ActiveMs
			}
			return out[id][i].AppKey < out[id][j].AppKey
		})
	}
	return out, nil
}

func (s *ActivityStore) activityRunActiveMsByApp(runIDs []string, current *ActivityTimelineRunRow) (map[string]int64, error) {
	byApp := map[string]int64{}
	for _, id := range runIDs {
		var run *ActivityTimelineRunRow
		if current != nil && current.ID == id {
			run = current
		} else {
			var err error
			run, err = s.GetTimelineRunByID(id)
			if err != nil {
				return nil, err
			}
		}
		if run == nil {
			continue
		}
		allowed, err := s.timelineRunEvidenceAllowed(*run)
		if err != nil {
			return nil, err
		}
		if allowed {
			byApp[run.AppName] += run.ActiveMs
		}
	}
	return byApp, nil
}

// timelineRunColumnsQualified returns timelineRunColumns with each bare column
// prefixed by the given table alias, for the composer JOIN.
func timelineRunColumnsQualified(alias string) string {
	cols := strings.Split(timelineRunColumns, ",")
	for i, c := range cols {
		cols[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}
