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
	"unicode/utf8"

	"wails-gui/internal/platform/activitymon"
)

// Timeline app runs are the persisted form of the Timeline view's per-app
// groups. Raw activity_sessions/activity_events remain the capture boundary;
// runs are derived from the event stream once (incrementally, at session-save
// time) instead of per query, so their ids stay stable across window/limit/
// keyword changes and the summary cache never orphans.
//
// Identity: a run's id is minted from its first event's autoincrement row id
// ("tg_<rowid>"), which is immutable and chronologically monotone (sessions
// are saved in order and never overlap; event timestamps are unique).

// timelineRunStatus* are the run lifecycle states. Exactly one run — the
// chronologically newest — is 'open'; it keeps absorbing same-app events as
// later sessions are saved.
const (
	timelineRunStatusOpen   = "open"
	timelineRunStatusClosed = "closed"
)

// timelineSummaryStatus* are the summary generation states for one run.
const (
	timelineSummaryStatusNone            = "none"
	timelineSummaryStatusGenerating      = "generating"
	timelineSummaryStatusDone            = "done"
	timelineSummaryStatusSkipped         = "skipped"
	timelineSummaryStatusFailedTransient = "failed_transient"
	timelineSummaryStatusFailedPermanent = "failed_permanent"
)

// timelineGroupActiveGapCapMs caps the inter-event gap counted toward a run's
// active duration, so an idle hour inside one app run does not read as an
// hour of activity.
const timelineGroupActiveGapCapMs int64 = 60_000

// ActivityTimelineRunRow mirrors one activity_timeline_groups row.
type ActivityTimelineRunRow struct {
	ID                   string
	FirstEventRowID      int64
	LastEventRowID       int64
	AppKey               string
	AppName              string
	StartMs              int64
	EndMs                int64
	ActiveMs             int64
	EventCount           int
	SessionIDs           []string
	TriggerKinds         []string
	Status               string
	SummaryStatus        string
	SummaryAttempts      int
	NextRetryAt          int64
	SummarizedEventCount int
	CreatedAt            int64
	UpdatedAt            int64
	EvidenceTokens       []ActivityEvidenceToken
	EvidenceExcluded     bool
}

func timelineRunID(firstEventRowID int64) string {
	return fmt.Sprintf("tg_%d", firstEventRowID)
}

// timelineAppKey/timelineAppName are the string-level identity helpers shared
// by the full-event path (timelineEventAppKey/timelineEventAppName in
// activity_monitor.go) and the lite derivation path below.
func timelineAppKey(bundleID, appName string) string {
	if strings.TrimSpace(bundleID) != "" {
		return strings.TrimSpace(bundleID)
	}
	if strings.TrimSpace(appName) != "" {
		return "app:" + strings.TrimSpace(appName)
	}
	return "unknown"
}

func timelineAppName(bundleID, appName string) string {
	if strings.TrimSpace(appName) != "" {
		return strings.TrimSpace(appName)
	}
	if strings.TrimSpace(bundleID) != "" {
		return strings.TrimSpace(bundleID)
	}
	return "Unknown"
}

// timelineRunEvent is the lite derivation input: just enough of an event row
// to group runs without holding full parsed events in memory (the reconcile
// window is small, but the migration backfill scans the whole table).
type timelineRunEvent struct {
	RowID            int64
	Timestamp        int64
	SessionStartTime int64
	SessionID        string
	TriggerKind      string
	AppKey           string
	AppName          string
	LowSignal        bool
}

// timelineRunEventFields is the partial ActivityEvent JSON parse for the lite
// path — only the fields that influence grouping and low-signal detection.
type timelineRunEventFields struct {
	AppBundleID string `json:"appBundleId"`
	AppName     string `json:"appName"`
	ClickRole   string `json:"clickRole"`
	ClickLabel  string `json:"clickLabel"`
	FocusRole   string `json:"focusRole"`
	FocusLabel  string `json:"focusLabel"`
}

// isLowSignalTimelineEventKind reports whether a single event of this shape
// carries no standalone meaning (pure app switch, unlabeled click/focus). A
// run consisting of exactly one such event is timeline noise: it is folded
// into the surrounding same-app run or dropped.
func isLowSignalTimelineEventKind(kind activitymon.EventKind, clickLabel, clickRole, focusLabel, focusRole string) bool {
	switch kind {
	case activitymon.EventAppSwitch:
		return true
	case activitymon.EventClick:
		return strings.TrimSpace(clickLabel) == "" && strings.TrimSpace(clickRole) == ""
	case activitymon.EventFocus:
		return strings.TrimSpace(focusLabel) == "" && strings.TrimSpace(focusRole) == ""
	default:
		return false
	}
}

// derivedTimelineRun pairs a run row with the derivation-only flag needed by
// compaction (whether a singleton run's sole event is low-signal).
type derivedTimelineRun struct {
	row           ActivityTimelineRunRow
	soleLowSignal bool
	dayActiveMs   int64
	spans         []activityThreadInterval
}

func (d derivedTimelineRun) isLowSignalSingleton() bool {
	return d.row.EventCount == 1 && d.row.ActiveMs <= 1000 && d.soleLowSignal
}

// deriveTimelineRuns groups a chronological event stream into per-app runs.
// Pure function: the exact port of the former query-time derivation in
// GetActivityTimelineGroups (group on app change, cap idle gaps, drop
// low-signal singletons, merge same-app neighbors, floor durations), now run
// once at persist time. Input must be sorted by (Timestamp, SessionStartTime).
// timelineRunDayAllocation observes the original counter's contributions.
// Floors belong to their observation; capped intervals end at the next event.
type timelineRunDayAllocation struct {
	start, end int64
	active     map[string]int64
	spans      map[string][]activityThreadInterval
}

func (d *timelineRunDayAllocation) interval(at, ms int64) int64 {
	if d == nil {
		return 0
	}
	return max(int64(0), min(d.end, at)-max(d.start, at-ms))
}
func (d *timelineRunDayAllocation) point(at, ms int64) int64 {
	if d != nil && at >= d.start && at < d.end {
		return ms
	}
	return 0
}

func deriveTimelineRuns(items []timelineRunEvent, dayAllocation ...*timelineRunDayAllocation) []ActivityTimelineRunRow {
	var day *timelineRunDayAllocation
	if len(dayAllocation) > 0 {
		day = dayAllocation[0]
	}
	derived := make([]derivedTimelineRun, 0)
	var current *derivedTimelineRun
	var previous *timelineRunEvent

	for i := range items {
		item := &items[i]
		if current == nil || current.row.AppKey != item.AppKey {
			if current != nil && current.row.EventCount > 0 && current.row.ActiveMs == 0 {
				current.row.ActiveMs = 1000
				current.dayActiveMs += day.point(current.row.EndMs, 1000)
				if day != nil && day.spans != nil {
					current.spans = append(current.spans, activityThreadInterval{start: current.row.EndMs, end: current.row.EndMs + 1000})
				}
			}
			derived = append(derived, derivedTimelineRun{
				row: ActivityTimelineRunRow{
					ID:              timelineRunID(item.RowID),
					FirstEventRowID: item.RowID,
					LastEventRowID:  item.RowID,
					AppKey:          item.AppKey,
					AppName:         item.AppName,
					StartMs:         item.Timestamp,
					EndMs:           item.Timestamp,
					SessionIDs:      []string{},
					TriggerKinds:    []string{},
				},
				soleLowSignal: item.LowSignal,
			})
			current = &derived[len(derived)-1]
			previous = nil
		}

		if previous != nil {
			gap := item.Timestamp - previous.Timestamp
			if gap > 0 {
				if gap > timelineGroupActiveGapCapMs {
					gap = timelineGroupActiveGapCapMs
				}
				current.row.ActiveMs += gap
				current.dayActiveMs += day.interval(item.Timestamp, gap)
				if day != nil && day.spans != nil {
					current.spans = append(current.spans, activityThreadInterval{start: item.Timestamp - gap, end: item.Timestamp})
				}
			}
		}
		current.row.EndMs = item.Timestamp
		current.row.LastEventRowID = item.RowID
		current.row.EventCount++
		current.row.SessionIDs = appendUniqueString(current.row.SessionIDs, item.SessionID)
		current.row.TriggerKinds = appendUniqueString(current.row.TriggerKinds, item.TriggerKind)
		previous = item
	}
	if current != nil && current.row.EventCount > 0 && current.row.ActiveMs == 0 {
		current.row.ActiveMs = 1000
		current.dayActiveMs += day.point(current.row.EndMs, 1000)
		if day != nil && day.spans != nil {
			current.spans = append(current.spans, activityThreadInterval{start: current.row.EndMs, end: current.row.EndMs + 1000})
		}
	}

	compacted := compactDerivedTimelineRuns(derived, day)

	runs := make([]ActivityTimelineRunRow, 0, len(compacted))
	for _, d := range compacted {
		row := d.row
		if row.EventCount > 0 && row.ActiveMs < 1000 {
			d.dayActiveMs += day.point(row.EndMs, 1000-row.ActiveMs)
			if day != nil && day.spans != nil {
				d.spans = append(d.spans, activityThreadInterval{start: row.EndMs, end: row.EndMs + 1000 - row.ActiveMs})
			}
			row.ActiveMs = 1000
		}
		if day != nil {
			day.active[row.ID] = d.dayActiveMs
			if day.spans != nil {
				day.spans[row.ID] = d.spans
			}
		}
		runs = append(runs, row)
	}
	return runs
}

// compactDerivedTimelineRuns drops low-signal singleton runs and merges the
// same-app neighbors they separated, preserving the earlier run's identity.
func compactDerivedTimelineRuns(runs []derivedTimelineRun, dayAllocation ...*timelineRunDayAllocation) []derivedTimelineRun {
	var day *timelineRunDayAllocation
	if len(dayAllocation) > 0 {
		day = dayAllocation[0]
	}
	compacted := make([]derivedTimelineRun, 0, len(runs))
	for _, run := range runs {
		if run.isLowSignalSingleton() {
			continue
		}
		if len(compacted) > 0 && compacted[len(compacted)-1].row.AppKey == run.row.AppKey {
			mergeDerivedTimelineRun(&compacted[len(compacted)-1], run, day)
			continue
		}
		compacted = append(compacted, run)
	}
	return compacted
}

func mergeDerivedTimelineRun(dst *derivedTimelineRun, src derivedTimelineRun, dayAllocation ...*timelineRunDayAllocation) {
	var day *timelineRunDayAllocation
	if len(dayAllocation) > 0 {
		day = dayAllocation[0]
	}
	gap := src.row.StartMs - dst.row.EndMs
	if gap > 0 {
		if gap > timelineGroupActiveGapCapMs {
			gap = timelineGroupActiveGapCapMs
		}
		dst.row.ActiveMs += gap
		dst.dayActiveMs += day.interval(src.row.StartMs, gap)
		if day != nil && day.spans != nil {
			dst.spans = append(dst.spans, activityThreadInterval{start: src.row.StartMs - gap, end: src.row.StartMs})
		}
	}
	dst.row.EndMs = src.row.EndMs
	dst.row.ActiveMs += src.row.ActiveMs
	dst.dayActiveMs += src.dayActiveMs
	dst.spans = append(dst.spans, src.spans...)
	dst.row.EventCount += src.row.EventCount
	dst.row.LastEventRowID = src.row.LastEventRowID
	dst.row.SessionIDs = appendUniqueStrings(dst.row.SessionIDs, src.row.SessionIDs)
	dst.row.TriggerKinds = appendUniqueStrings(dst.row.TriggerKinds, src.row.TriggerKinds)
}

// markTimelineRunStatuses closes every run except the chronologically newest,
// which stays open to absorb the continuing app run.
func markTimelineRunStatuses(runs []ActivityTimelineRunRow) {
	for i := range runs {
		if i == len(runs)-1 {
			runs[i].Status = timelineRunStatusOpen
		} else {
			runs[i].Status = timelineRunStatusClosed
		}
	}
}

// sortTimelineRunEvents applies the canonical event ordering for derivation:
// timestamp, with session start as tiebreak (same as the former query path).
func sortTimelineRunEvents(items []timelineRunEvent) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Timestamp == items[j].Timestamp {
			return items[i].SessionStartTime < items[j].SessionStartTime
		}
		return items[i].Timestamp < items[j].Timestamp
	})
}

// sqlQueryer abstracts *sql.DB / *sql.Tx so the scan and insert helpers below
// serve both the live reconcile path and the one-shot migration backfill.
type sqlQueryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// scanTimelineRunEventsFromRowID streams event rows (joined with their
// session's start time and trigger) from minRowID onward into lite derivation
// inputs, parsing only the JSON fields that matter for grouping.
func scanTimelineRunEventsFromRowID(q sqlQueryer, minRowID int64, throughRowID ...int64) ([]timelineRunEvent, error) {
	query := `SELECT e.id, e.timestamp, e.kind, e.data, e.session_id, s.start_time, s.trigger_kind
        FROM activity_events e JOIN activity_sessions s ON s.id=e.session_id WHERE e.id>=?`
	args := []any{minRowID}
	if len(throughRowID) > 0 {
		query += ` AND e.id<=? AND s.triage_status!='protected'`
		args = append(args, throughRowID[0])
	}
	query += ` ORDER BY e.id`
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query timeline run events: %w", err)
	}
	defer rows.Close()

	var items []timelineRunEvent
	for rows.Next() {
		var (
			rowID, timestamp, sessionStart int64
			kind, data, sessionID, trigger string
		)
		if err := rows.Scan(&rowID, &timestamp, &kind, &data, &sessionID, &sessionStart, &trigger); err != nil {
			return nil, fmt.Errorf("scan timeline run event: %w", err)
		}
		var fields timelineRunEventFields
		if err := json.Unmarshal([]byte(data), &fields); err != nil {
			log.Printf("[activity] timeline run event %d: unmarshal lite fields: %v", rowID, err)
			continue
		}
		items = append(items, timelineRunEvent{
			RowID:            rowID,
			Timestamp:        timestamp,
			SessionStartTime: sessionStart,
			SessionID:        sessionID,
			TriggerKind:      trigger,
			AppKey:           timelineAppKey(fields.AppBundleID, fields.AppName),
			AppName:          timelineAppName(fields.AppBundleID, fields.AppName),
			LowSignal: isLowSignalTimelineEventKind(
				activitymon.EventKind(kind),
				fields.ClickLabel, fields.ClickRole,
				fields.FocusLabel, fields.FocusRole,
			),
		})
	}
	return items, rows.Err()
}

const timelineRunColumns = `id, first_event_row_id, last_event_row_id, app_key, app_name,
	start_ms, end_ms, active_ms, event_count, session_ids, trigger_kinds,
	status, summary_status, summary_attempts, next_retry_at, summarized_event_count,
	created_at, updated_at, evidence_tokens, evidence_excluded`

func scanTimelineRunRows(rows *sql.Rows) ([]ActivityTimelineRunRow, error) {
	return scanTimelineRunRowsContext(context.Background(), rows)
}

func scanTimelineRunRowsContext(ctx context.Context, rows *sql.Rows) ([]ActivityTimelineRunRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var runs []ActivityTimelineRunRow
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var run ActivityTimelineRunRow
		var sessionIDs, triggerKinds, evidenceTokens string
		if err := rows.Scan(&run.ID, &run.FirstEventRowID, &run.LastEventRowID, &run.AppKey, &run.AppName,
			&run.StartMs, &run.EndMs, &run.ActiveMs, &run.EventCount, &sessionIDs, &triggerKinds,
			&run.Status, &run.SummaryStatus, &run.SummaryAttempts, &run.NextRetryAt, &run.SummarizedEventCount,
			&run.CreatedAt, &run.UpdatedAt, &evidenceTokens, &run.EvidenceExcluded); err != nil {
			return nil, fmt.Errorf("scan timeline run: %w", err)
		}
		if err := json.Unmarshal([]byte(sessionIDs), &run.SessionIDs); err != nil {
			log.Printf("[activity] timeline run %s: unmarshal session ids: %v", run.ID, err)
		}
		if err := json.Unmarshal([]byte(triggerKinds), &run.TriggerKinds); err != nil {
			log.Printf("[activity] timeline run %s: unmarshal trigger kinds: %v", run.ID, err)
		}
		if err := json.Unmarshal([]byte(evidenceTokens), &run.EvidenceTokens); err != nil {
			return nil, fmt.Errorf("decode timeline run %s evidence: %w", run.ID, err)
		}
		run.EvidenceTokens = sanitizeActivityEvidenceTokens(run.EvidenceTokens, true)
		if run.EvidenceExcluded {
			run.EvidenceTokens = []ActivityEvidenceToken{}
		}
		runs = append(runs, run)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return runs, rows.Err()
}

func insertTimelineRun(q sqlQueryer, run ActivityTimelineRunRow, nowMs int64) error {
	sessionIDs, err := json.Marshal(run.SessionIDs)
	if err != nil {
		return fmt.Errorf("marshal timeline run session ids: %w", err)
	}
	triggerKinds, err := json.Marshal(run.TriggerKinds)
	if err != nil {
		return fmt.Errorf("marshal timeline run trigger kinds: %w", err)
	}
	// Use INSERT OR IGNORE to handle the case where ReconcileTimelineRuns
	// only looks back 2 anchors but the derived run's first_event_row_id
	// already exists in the table from a prior reconcile window. Without
	// this, the UNIQUE constraint on first_event_row_id causes a permanent
	// error loop every 2 minutes in the summary worker.
	res, err := q.Exec(`
		INSERT OR IGNORE INTO activity_timeline_groups (
			id, first_event_row_id, last_event_row_id, app_key, app_name,
			start_ms, end_ms, active_ms, event_count, session_ids, trigger_kinds,
			status, summary_status, summary_attempts, next_retry_at, summarized_event_count,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.FirstEventRowID, run.LastEventRowID, run.AppKey, run.AppName,
		run.StartMs, run.EndMs, run.ActiveMs, run.EventCount, string(sessionIDs), string(triggerKinds),
		run.Status, timelineSummaryStatusNone, 0, 0, 0,
		nowMs, nowMs)
	if err != nil {
		return fmt.Errorf("insert timeline run %s: %w", run.ID, err)
	}
	// If the row was ignored (already exists), update the derivation fields
	// so the run stays current without losing its summary state.
	if n, _ := res.RowsAffected(); n == 0 {
		return updateTimelineRunDerivation(q, run, nowMs)
	}
	// Historical migrations create the run table before evidence columns exist.
	// Their derivation rows have nil evidence; only current writes persist it.
	if run.EvidenceTokens != nil || run.EvidenceExcluded {
		return persistTimelineRunEvidence(q, run)
	}
	return nil
}

// updateTimelineRunDerivation refreshes a run's derived fields after a
// reconcile, preserving its summary_* state.
func updateTimelineRunDerivation(q sqlQueryer, run ActivityTimelineRunRow, nowMs int64) error {
	sessionIDs, err := json.Marshal(run.SessionIDs)
	if err != nil {
		return fmt.Errorf("marshal timeline run session ids: %w", err)
	}
	triggerKinds, err := json.Marshal(run.TriggerKinds)
	if err != nil {
		return fmt.Errorf("marshal timeline run trigger kinds: %w", err)
	}
	_, err = q.Exec(`
		UPDATE activity_timeline_groups SET
			last_event_row_id = ?, app_name = ?, start_ms = ?, end_ms = ?, active_ms = ?,
			event_count = ?, session_ids = ?, trigger_kinds = ?, status = ?, updated_at = ?
		WHERE id = ?`,
		run.LastEventRowID, run.AppName, run.StartMs, run.EndMs, run.ActiveMs,
		run.EventCount, string(sessionIDs), string(triggerKinds), run.Status, nowMs,
		run.ID)
	if err != nil {
		return fmt.Errorf("update timeline run %s: %w", run.ID, err)
	}
	return nil
}

// timelineRunDerivationEqual reports whether the derived fields match, so
// no-op reconciles skip the write (and skip signaling "changed").
func timelineRunDerivationEqual(a, b ActivityTimelineRunRow) bool {
	if a.LastEventRowID != b.LastEventRowID || a.AppName != b.AppName ||
		a.StartMs != b.StartMs || a.EndMs != b.EndMs || a.ActiveMs != b.ActiveMs ||
		a.EventCount != b.EventCount || a.Status != b.Status {
		return false
	}
	if a.EvidenceExcluded != b.EvidenceExcluded || !activityEvidenceTokensEqual(a.EvidenceTokens, b.EvidenceTokens) {
		return false
	}
	if len(a.SessionIDs) != len(b.SessionIDs) || len(a.TriggerKinds) != len(b.TriggerKinds) {
		return false
	}
	for i := range a.SessionIDs {
		if a.SessionIDs[i] != b.SessionIDs[i] {
			return false
		}
	}
	for i := range a.TriggerKinds {
		if a.TriggerKinds[i] != b.TriggerKinds[i] {
			return false
		}
	}
	return true
}

// GetLastTimelineRuns returns the newest n runs by first_event_row_id
// descending. Row-id order — not start_ms — is the reconcile's scan order:
// the anchors must be exactly the runs owning the tail of the event-row
// space, so that no third run can hold a first_event_row_id at/after the
// anchor boundary. Out-of-order event timestamps (a delayed session commit
// carrying older-stamped events) make start_ms diverge from row-id order;
// anchoring on start_ms then re-derives a run that collides with a row
// outside the anchor set and freezes the reconcile on the UNIQUE constraint.
func (s *ActivityStore) GetLastTimelineRuns(n int) ([]ActivityTimelineRunRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+timelineRunColumns+`
		FROM activity_timeline_groups
		ORDER BY first_event_row_id DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("query last timeline runs: %w", err)
	}
	defer rows.Close()
	return scanTimelineRunRows(rows)
}

// GetTimelineRunByID returns one run, or nil when absent.
func (s *ActivityStore) GetTimelineRunByID(id string) (*ActivityTimelineRunRow, error) {
	return s.GetTimelineRunByIDContext(context.Background(), id)
}

func (s *ActivityStore) GetTimelineRunByIDContext(ctx context.Context, id string) (*ActivityTimelineRunRow, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT `+timelineRunColumns+`
		FROM activity_timeline_groups
		WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("query timeline run by id: %w", err)
	}
	defer rows.Close()
	runs, err := scanTimelineRunRows(rows)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

// GetTimelineRuns returns runs in the time range, newest first. Bounds use
// containment semantics matching GetFilteredSessions (0 skips a bound).
func (s *ActivityStore) GetTimelineRuns(limit, offset int, startMs, endMs int64) ([]ActivityTimelineRunRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+timelineRunColumns+`
		FROM activity_timeline_groups
		WHERE (? = 0 OR start_ms >= ?)
		  AND (? = 0 OR end_ms <= ?)
		ORDER BY start_ms DESC
		LIMIT ? OFFSET ?`,
		startMs, startMs, endMs, endMs, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query timeline runs: %w", err)
	}
	defer rows.Close()
	return scanTimelineRunRows(rows)
}

// timelineRunsKeywordCap bounds the keyword path, which fetches the range
// un-paginated and filters by matching sessions in Go.
const timelineRunsKeywordCap = 2000

// GetTimelineRunsInRange returns every run in the range, newest first,
// capped defensively for the keyword-filter path.
func (s *ActivityStore) GetTimelineRunsInRange(startMs, endMs int64) ([]ActivityTimelineRunRow, error) {
	return s.GetTimelineRuns(timelineRunsKeywordCap, 0, startMs, endMs)
}

// GetTimelineRunsForSessions returns the runs containing any of the given
// sessions, newest first, regardless of any date window. Backs the
// session-anchored entry points (Who you know page, memory/knowledge/pipeline
// jumps), where the target session can lie outside the sidebar's current
// filter range.
func (s *ActivityStore) GetTimelineRunsForSessions(sessionIDs []string) ([]ActivityTimelineRunRow, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	sessions, err := s.GetSessionsByIDs(sessionIDs)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	var minStart, maxEnd int64
	for _, session := range sessions {
		if minStart == 0 || session.StartTime < minStart {
			minStart = session.StartTime
		}
		if session.EndTime > maxEnd {
			maxEnd = session.EndTime
		}
	}

	// Overlap query over the sessions' combined span, then exact membership
	// filtering in Go — a run contains a session iff it stitched events from
	// it, which the session_ids list records.
	rows, err := s.reader().Query(`
		SELECT `+timelineRunColumns+`
		FROM activity_timeline_groups
		WHERE end_ms >= ? AND start_ms <= ?
		ORDER BY start_ms DESC`, minStart, maxEnd)
	if err != nil {
		return nil, fmt.Errorf("query timeline runs for sessions: %w", err)
	}
	defer rows.Close()
	candidates, err := scanTimelineRunRows(rows)
	if err != nil {
		return nil, err
	}

	wanted := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		wanted[id] = true
	}
	matches := make([]ActivityTimelineRunRow, 0, len(sessionIDs))
	for _, run := range candidates {
		for _, sessionID := range run.SessionIDs {
			if wanted[sessionID] {
				matches = append(matches, run)
				break
			}
		}
	}
	return matches, nil
}

// GetFilteredSessionIDs returns the ids of sessions matching the keyword
// within the time range — the SELECT-id-only twin of GetFilteredSessions,
// used to filter persisted runs by their member sessions.
func (s *ActivityStore) GetFilteredSessionIDs(startMs, endMs int64, keyword string) (map[string]bool, error) {
	kw := "%" + keyword + "%"

	// trigram tokenizer requires >= 3 characters; fall back to LIKE for short keywords.
	useFTS := utf8.RuneCountInString(keyword) >= 3
	var ocrClause string
	var ocrParam string
	if useFTS {
		ocrClause = `EXISTS (
			SELECT 1 FROM activity_ocr_frames o
			JOIN activity_ocr_fts f ON f.rowid = o.id
			WHERE o.session_id = s.id AND f.text MATCH ?
		)`
		ocrParam = `"` + keyword + `"`
	} else {
		ocrClause = `EXISTS (
			SELECT 1 FROM activity_ocr_frames o
			WHERE o.session_id = s.id AND o.text LIKE ?
		)`
		ocrParam = kw
	}

	query := `
		SELECT id FROM activity_sessions s
		WHERE (? = 0 OR s.start_time >= ?)
		  AND (? = 0 OR s.end_time <= ?)
		  AND (? = '' OR s.app_names LIKE ? OR EXISTS (
			SELECT 1 FROM activity_events e
			WHERE e.session_id = s.id AND e.data LIKE ?
		  ) OR ` + ocrClause + `)`
	rows, err := s.reader().Query(query,
		startMs, startMs,
		endMs, endMs,
		keyword, kw, kw, ocrParam,
	)
	if err != nil {
		return nil, fmt.Errorf("query filtered session ids: %w", err)
	}
	defer rows.Close()

	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan filtered session id: %w", err)
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// ReconcileTimelineRuns incrementally re-derives app runs from the last two
// persisted runs onward and reconciles the result into the table by
// first_event_row_id: matching runs are updated in place (id and summary
// state preserved), new runs are inserted, and runs that disappeared (a fold
// after an invariant violation) are deleted along with their summaries.
//
// The two-run anchor suffices because adjacent persisted runs never share an
// app key and a persisted run is never a low-signal singleton, so
// re-derivation from that boundary reproduces the anchors and can only
// extend/close the open run or append new ones; the extra run of lookback
// makes the operation self-healing after a crash mid-write. The whole
// operation is idempotent — a second call over the same data is a no-op.
func (s *ActivityStore) ReconcileTimelineRuns() (bool, error) {
	s.timelineRunsMu.Lock()
	defer s.timelineRunsMu.Unlock()

	anchors, err := s.GetLastTimelineRuns(2)
	if err != nil {
		return false, err
	}
	var anchorRowID int64
	if len(anchors) > 0 {
		anchorRowID = anchors[len(anchors)-1].FirstEventRowID
	}

	var maxEventID int64
	if err := s.reader().QueryRow(`SELECT COALESCE(MAX(id), 0) FROM activity_events`).Scan(&maxEventID); err != nil {
		return false, fmt.Errorf("query max event id: %w", err)
	}
	if maxEventID == 0 {
		return false, nil
	}
	// Fast path: nothing new since the open run's tail. (A dropped tail blip
	// keeps maxEventID ahead of the run tail, so this misses then — the
	// re-derivation below is cheap and re-drops it deterministically.)
	if len(anchors) > 0 && maxEventID == anchors[0].LastEventRowID {
		return false, nil
	}

	items, err := scanTimelineRunEventsFromRowID(s.db, anchorRowID)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, nil
	}
	sortTimelineRunEvents(items)
	derived := deriveTimelineRuns(items)
	if len(derived) == 0 {
		// Only unpersistable blips in the window; leave existing rows alone.
		return false, nil
	}
	markTimelineRunStatuses(derived)
	// Preserve bounded identifiers while deriving runs. Raw event/snapshot/OCR
	// collection belongs to the worker; growing runs must not block session save.
	for i := range derived {
		existing, err := s.GetTimelineRunByID(derived[i].ID)
		if err != nil {
			return false, err
		}
		if existing != nil {
			derived[i].EvidenceTokens = existing.EvidenceTokens
			derived[i].EvidenceExcluded = existing.EvidenceExcluded
		}
	}

	existingByFirst := make(map[int64]ActivityTimelineRunRow, len(anchors))
	for _, run := range anchors {
		existingByFirst[run.FirstEventRowID] = run
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin timeline run reconcile: %w", err)
	}
	defer tx.Rollback()

	nowMs := time.Now().UnixMilli()
	changed := false
	derivedFirst := make(map[int64]bool, len(derived))
	for _, run := range derived {
		derivedFirst[run.FirstEventRowID] = true
		if existing, ok := existingByFirst[run.FirstEventRowID]; ok {
			if timelineRunDerivationEqual(existing, run) {
				continue
			}
			if err := updateTimelineRunDerivation(tx, run, nowMs); err != nil {
				return false, err
			}
			changed = true
			continue
		}
		if err := insertTimelineRun(tx, run, nowMs); err != nil {
			if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return false, err
			}
			// A run outside the two-run anchor set already owns this
			// first_event_row_id (legacy rows written while anchors were
			// selected by start_ms could sit past the anchor boundary). Ids
			// are minted from first_event_row_id, so the existing row IS this
			// run — refresh it in place. Failing here instead would abort the
			// whole reconcile transaction on every pass and freeze the run
			// table until the collision happens to derive away.
			if err := updateTimelineRunDerivation(tx, run, nowMs); err != nil {
				return false, fmt.Errorf("update colliding timeline run %s: %w", run.ID, err)
			}
		}
		changed = true
	}
	for first, existing := range existingByFirst {
		if derivedFirst[first] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM activity_timeline_group_summaries WHERE group_id = ?`, existing.ID); err != nil {
			return false, fmt.Errorf("delete summary for folded run %s: %w", existing.ID, err)
		}
		if _, err := tx.Exec(`DELETE FROM activity_timeline_groups WHERE id = ?`, existing.ID); err != nil {
			return false, fmt.Errorf("delete folded run %s: %w", existing.ID, err)
		}
		changed = true
	}

	if !changed {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit timeline run reconcile: %w", err)
	}
	return true, nil
}

// ClaimTimelineRunForSummary atomically transitions a run into 'generating'
// when its current summary_status is one of fromStatuses. Returns false when
// another path already claimed it (the CAS is the worker/on-demand mutex).
func (s *ActivityStore) ClaimTimelineRunForSummary(id string, fromStatuses []string) (bool, error) {
	if len(fromStatuses) == 0 {
		return false, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(fromStatuses)), ",")
	args := make([]any, 0, len(fromStatuses)+2)
	args = append(args, timelineSummaryStatusGenerating, time.Now().UnixMilli(), id)
	for _, status := range fromStatuses {
		args = append(args, status)
	}
	res, err := s.db.Exec(`
		UPDATE activity_timeline_groups
		SET summary_status = ?, updated_at = ?
		WHERE id = ? AND summary_status IN (`+placeholders+`)`, args...)
	if err != nil {
		return false, fmt.Errorf("claim timeline run %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// MarkTimelineRunSummaryDone records a successful generation, remembering how
// many events the summary covered so growth can re-trigger regeneration.
func (s *ActivityStore) MarkTimelineRunSummaryDone(id string, summarizedEventCount int) error {
	_, err := s.db.Exec(`
		UPDATE activity_timeline_groups
		SET summary_status = ?, summarized_event_count = ?, summary_attempts = 0,
			next_retry_at = 0, updated_at = ?
		WHERE id = ?`,
		timelineSummaryStatusDone, summarizedEventCount, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("mark timeline run %s summary done: %w", id, err)
	}
	return nil
}

// MarkTimelineRunSummarySkipped records that generation was intentionally not
// performed (no loadable events — e.g. the member sessions were purged).
func (s *ActivityStore) MarkTimelineRunSummarySkipped(id string, eventCount int) error {
	_, err := s.db.Exec(`
		UPDATE activity_timeline_groups
		SET summary_status = ?, summarized_event_count = ?, updated_at = ?
		WHERE id = ?`,
		timelineSummaryStatusSkipped, eventCount, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("mark timeline run %s summary skipped: %w", id, err)
	}
	return nil
}

// MarkTimelineRunSummaryFailed records a failed generation. Transient
// failures schedule a retry; permanent ones park the run until the user
// explicitly regenerates it.
func (s *ActivityStore) MarkTimelineRunSummaryFailed(id string, permanent bool, attempts int, nextRetryAt int64) error {
	status := timelineSummaryStatusFailedTransient
	if permanent {
		status = timelineSummaryStatusFailedPermanent
		nextRetryAt = 0
	}
	_, err := s.db.Exec(`
		UPDATE activity_timeline_groups
		SET summary_status = ?, summary_attempts = ?, next_retry_at = ?, updated_at = ?
		WHERE id = ?`,
		status, attempts, nextRetryAt, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("mark timeline run %s summary failed: %w", id, err)
	}
	return nil
}

// ResetGeneratingTimelineRuns recovers runs stuck in 'generating' after a
// process kill mid-generation, so the worker picks them up again. Called at
// triager startup, mirroring ResetTriagingToPending.
func (s *ActivityStore) ResetGeneratingTimelineRuns() (int64, error) {
	res, err := s.db.Exec(`
		UPDATE activity_timeline_groups
		SET summary_status = ?
		WHERE summary_status = ?`,
		timelineSummaryStatusNone, timelineSummaryStatusGenerating)
	if err != nil {
		return 0, fmt.Errorf("reset generating timeline runs: %w", err)
	}
	return res.RowsAffected()
}

// GetTimelineRunsDueForSummary returns the runs the background worker should
// summarize now, newest first:
//   - fresh runs within the auto-summarize horizon (older runs are on-demand
//     only),
//   - transiently-failed runs whose retry is due, regardless of the horizon —
//     admission was already granted, so an outage longer than the horizon
//     must not silently void pending retry schedules, and
//   - already-summarized runs that grew since their summary (the reopened
//     open-run case — naturally bounded, closed runs never grow).
//
// Open runs qualify only after quietMs of no new events, so the open run gets
// an early summary without re-burning the model on every save.
func (s *ActivityStore) GetTimelineRunsDueForSummary(nowMs, horizonMs, quietMs int64, limit int) ([]ActivityTimelineRunRow, error) {
	rows, err := s.reader().Query(`
		SELECT `+timelineRunColumns+`
		FROM activity_timeline_groups
		WHERE (
			(summary_status = ? AND next_retry_at <= ? AND end_ms >= ?)
			OR (summary_status = ? AND next_retry_at <= ?)
			OR (summary_status IN (?, ?) AND event_count > summarized_event_count)
		)
		AND (status = ? OR end_ms <= ?)
		ORDER BY end_ms DESC
		LIMIT ?`,
		timelineSummaryStatusNone, nowMs, nowMs-horizonMs,
		timelineSummaryStatusFailedTransient, nowMs,
		timelineSummaryStatusDone, timelineSummaryStatusSkipped,
		timelineRunStatusClosed, nowMs-quietMs,
		limit)
	if err != nil {
		return nil, fmt.Errorf("query timeline runs due for summary: %w", err)
	}
	defer rows.Close()
	return scanTimelineRunRows(rows)
}

// PurgeTimelineRunsOlderThan deletes runs (and their summaries) that ended
// before the retention cutoff. Called from the storage rotator alongside
// session purging.
func (s *ActivityStore) PurgeTimelineRunsOlderThan(endMsBefore int64) error {
	return s.purgeTimelineRunsOlderThan(endMsBefore)
}

func (s *ActivityStore) purgeTimelineRunsOlderThan(endMsBefore int64) error {
	// Reconcile and evidence refresh must not change the run graph between
	// privacy pages. Readers and unrelated activity writes can still acquire
	// the database connection after each bounded transaction commits.
	s.timelineRunsMu.Lock()
	defer s.timelineRunsMu.Unlock()
	if err := s.prepareTimelineRetention(endMsBefore); err != nil {
		return fmt.Errorf("prepare timeline run retention: %w", err)
	}
	for {
		count, privacyChanged, err := s.purgeTimelineRetentionBatch(endMsBefore)
		if err != nil {
			return fmt.Errorf("purge timeline run batch: %w", err)
		}
		if privacyChanged {
			if err := s.prepareTimelineRetention(endMsBefore); err != nil {
				return fmt.Errorf("refresh timeline run retention: %w", err)
			}
			continue
		}
		if count < activityRetentionBatchSize {
			return nil
		}
	}
}

// LoadTimelineRunEvents loads the full member events of one run for the
// detail view and the summary prompt. Membership is exact: an event belongs
// to the run when it sits inside the run's row-id span, comes from one of the
// run's sessions, and matches the run's app key (folded low-signal blips from
// other apps inside the span are excluded by the app-key check).
func (s *ActivityStore) LoadTimelineRunEvents(run ActivityTimelineRunRow) ([]ActivityTimelineEvent, error) {
	return s.loadTimelineRunEventsBetween(run, 0, 0)
}

// Apply membership and observation bounds in SQLite before decoding event data.
func (s *ActivityStore) loadTimelineRunEventsBetween(run ActivityTimelineRunRow, startMs, endMs int64) ([]ActivityTimelineEvent, error) {
	sessions, err := s.GetSessionsByIDs(run.SessionIDs)
	if err != nil {
		return nil, err
	}
	items := make([]ActivityTimelineEvent, 0, run.EventCount)
	for _, sessionID := range run.SessionIDs {
		session, ok := sessions[sessionID]
		if !ok {
			continue
		}
		rows, err := s.reader().Query(`SELECT id,sort_order,data FROM activity_events
			WHERE session_id=? AND id>=? AND id<=? AND (?=0 OR timestamp>=?) AND (?=0 OR timestamp<=?)
			ORDER BY sort_order`, sessionID, run.FirstEventRowID, run.LastEventRowID, startMs, startMs, endMs, endMs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row ActivityEventRow
			var data string
			if err := rows.Scan(&row.ID, &row.SortOrder, &data); err != nil {
				rows.Close()
				return nil, err
			}
			if err := json.Unmarshal([]byte(data), &row.Event); err != nil {
				log.Printf("[activity] failed to unmarshal run event: %v", err)
				continue
			}
			if timelineEventAppKey(row.Event) != run.AppKey {
				continue
			}
			items = append(items, ActivityTimelineEvent{
				ActivityEvent:    row.Event,
				EventID:          fmt.Sprintf("ae_%d", row.ID),
				SessionID:        session.ID,
				SessionStartTime: session.StartTime,
				SessionEndTime:   session.EndTime,
				SessionTrigger:   session.TriggerKind,
				EventIndexInSess: row.SortOrder,
			})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if startMs > 0 && taskLifecycleBrowserDestination(strings.ToLower(run.AppKey), normalizeTaskWorkflowText(run.AppName)) {
		// A recent URL snapshot can precede the bounded draft/send events.
		// Seek each session's latest matching app through the context index;
		// a combined IN query would sort and decode earlier rows again.
		var predecessor *ActivityTimelineEvent
		var predecessorRowID int64
		for _, sessionID := range run.SessionIDs {
			session, ok := sessions[sessionID]
			if !ok {
				continue
			}
			var rowID int64
			var index int
			var data string
			err := s.reader().QueryRow(activityTimelineRunPredecessorSQL, sessionID, run.FirstEventRowID, run.LastEventRowID, run.StartMs, startMs, run.AppKey).Scan(&rowID, &index, &data)
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			if err == nil {
				var event activitymon.ActivityEvent
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					return nil, err
				}
				if predecessor == nil || event.Timestamp > predecessor.Timestamp || (event.Timestamp == predecessor.Timestamp && rowID > predecessorRowID) {
					predecessor = &ActivityTimelineEvent{ActivityEvent: event, EventID: fmt.Sprintf("ae_%d", rowID), SessionID: sessionID, SessionStartTime: session.StartTime, SessionEndTime: session.EndTime, SessionTrigger: session.TriggerKind, EventIndexInSess: index}
					predecessorRowID = rowID
				}
			}
		}
		if predecessor != nil {
			items = append(items, *predecessor)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Timestamp == items[j].Timestamp {
			return items[i].SessionStartTime < items[j].SessionStartTime
		}
		return items[i].Timestamp < items[j].Timestamp
	})
	return items, nil
}

// timelineRunToGroup assembles the Wails-facing group payload from a
// persisted run and its loaded events.
func timelineRunToGroup(run ActivityTimelineRunRow, events []ActivityTimelineEvent) ActivityTimelineGroup {
	primarySessionID := ""
	if len(run.SessionIDs) > 0 {
		primarySessionID = run.SessionIDs[0]
	}
	return ActivityTimelineGroup{
		ID:               run.ID,
		AppKey:           run.AppKey,
		AppName:          run.AppName,
		StartTime:        run.StartMs,
		EndTime:          run.EndMs,
		ActiveMs:         run.ActiveMs,
		EventCount:       run.EventCount,
		SessionIDs:       run.SessionIDs,
		TriggerKinds:     run.TriggerKinds,
		PrimarySessionID: primarySessionID,
		Events:           events,
		SummaryStatus:    run.SummaryStatus,
	}
}

// reconcileTimelineRunsAfterSave folds the just-saved session's events into
// the persisted run table. Runs synchronously on the session-save path —
// before the activity:session-saved emit — so the frontend's refetch sees the
// fresh runs. A successful reconcile that changed the table kicks the summary
// worker so newly closed runs queue immediately instead of waiting for its
// fallback ticker. Failures are logged, never propagated: run persistence
// must not disturb session persistence.
func (a *App) reconcileTimelineRunsAfterSave() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[activity] recovered from panic in timeline run reconcile: %v", r)
		}
	}()
	if a.activity == nil || a.activity.store == nil {
		return
	}
	changed, err := a.activity.store.ReconcileTimelineRuns()
	if err != nil {
		log.Printf("[activity] timeline run reconcile failed: %v", err)
		return
	}
	if !changed {
		return
	}
	a.activity.mu.Lock()
	triager := a.activity.triager
	a.activity.mu.Unlock()
	if triager != nil {
		triager.KickTimelineSummaries()
	}
}
