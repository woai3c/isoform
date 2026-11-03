package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
	"wails-gui/internal/pii"
)

// Two batched metadata queries supply every run and session used by the day.
// Source privacy decisions still come from the unchanged base read filter.
type activityThreadDayEvidence struct {
	runs         map[string]ActivityTimelineRunRow
	states       map[string]activityPrivacySourceState
	sources      map[string]activitySourceProvenance
	fingerprints map[string]string
	allowed      map[string]bool
}

func (a *App) activityThreadDayEvidence(ctx context.Context, activities []ActivityRow) (*activityThreadDayEvidence, error) {
	s := a.activity.store
	pauses, err := a.observationPausesContext(ctx)
	if err != nil {
		return nil, err
	}
	policy := activityReadPrivacyPolicy{app: a, pauses: pauses}
	result := &activityThreadDayEvidence{
		runs: map[string]ActivityTimelineRunRow{}, states: map[string]activityPrivacySourceState{},
		sources: map[string]activitySourceProvenance{}, fingerprints: map[string]string{}, allowed: map[string]bool{},
	}
	ids := map[string]bool{}
	for _, row := range activities {
		for _, id := range composeParseStrings(row.RunIDs) {
			ids[id] = true
		}
	}
	runIDs := make([]string, 0, len(ids))
	for id := range ids {
		runIDs = append(runIDs, id)
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT `+timelineRunColumns+` FROM activity_timeline_groups WHERE id IN (SELECT value FROM json_each(?))`, composeJSON(runIDs))
	if err != nil {
		return nil, err
	}
	runs, scanErr := scanTimelineRunRows(rows)
	if err := firstErr(scanErr, rows.Close()); err != nil {
		return nil, err
	}
	sessionSet := map[string]bool{}
	for _, run := range runs {
		result.runs[run.ID] = run
		for _, id := range run.SessionIDs {
			sessionSet[id] = true
		}
	}
	sessionIDs := make([]string, 0, len(sessionSet))
	for id := range sessionSet {
		sessionIDs = append(sessionIDs, id)
	}
	sessions := map[string]ActivitySessionRow{}
	rows, err = s.reader().QueryContext(ctx, `SELECT s.id,s.start_time,s.end_time,COALESCE(s.app_names,''),COALESCE(s.triage_status,''),COALESCE(s.meeting_app,''),COALESCE(r.revision,0),
        (SELECT schema_version FROM pragma_schema_version),COALESCE(s.transcript_path,''),COALESCE(s.audio_path,'')
        FROM activity_sessions s LEFT JOIN activity_evidence_source_revisions r ON r.session_id=s.id
        WHERE s.id IN (SELECT value FROM json_each(?))`, composeJSON(sessionIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		state := activityPrivacySourceState{db: s.db}
		var transcript, audio string
		if err := rows.Scan(&id, &state.start, &state.end, &state.apps, &state.status, &state.meeting, &state.revision, &state.schema, &transcript, &audio); err != nil {
			rows.Close()
			return nil, err
		}
		result.states[id] = state
		sessions[id] = ActivitySessionRow{ID: id, StartTime: state.start, EndTime: state.end, AppNames: state.apps, TriageStatus: state.status, MeetingApp: state.meeting, TranscriptPath: transcript, AudioPath: audio}
		if source, ok := s.cachedActivitySessionProvenance(id, state); ok {
			result.sources[id] = source
		}
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	// Cached facts may also describe a source the base read denied. Reapply
	// policy to both declared metadata and captured facts before using them.
	allowedSession := map[string]bool{}
	for id, session := range sessions {
		source, proven := result.sources[id]
		declared := activitySourceProvenance{StartTime: session.StartTime, EndTime: session.EndTime, Private: session.TriageStatus == "protected"}
		valid := session.AppNames == "" || json.Unmarshal([]byte(session.AppNames), &declared.Apps) == nil
		declared.Apps = append(declared.Apps, session.MeetingApp)
		allowedSession[id] = valid && proven && policy.allows(declared) && policy.allows(source)
	}
	for id, session := range sessions {
		if session.TranscriptPath == "" {
			continue
		}
		dependencies, err := s.transcriptSourceIDs(session, "")
		if err != nil {
			allowedSession[id] = false
			continue
		}
		for _, dependency := range dependencies {
			// Out-of-batch dependencies have no current metadata proof here.
			// Keep the base view, but withhold additive evidence conservatively.
			if !allowedSession[dependency] {
				allowedSession[id] = false
			}
		}
	}
	for _, run := range runs {
		hash := sha256.New()
		fmt.Fprintf(hash, "codes-only-bounded-measurements-v3:%s:%s:%s:%d:%d:%d:%d:%d:%t", run.ID, run.AppKey, run.AppName,
			run.FirstEventRowID, run.LastEventRowID, run.StartMs, run.EndMs, run.EventCount, activityEvidenceAppExcluded(run.AppKey, run.AppName))
		sessions := append([]string(nil), run.SessionIDs...)
		sort.Strings(sessions)
		allowed := !run.EvidenceExcluded && !activityEvidenceAppExcluded(run.AppKey, run.AppName)
		for _, id := range sessions {
			state, ok := result.states[id]
			protected := state.status == "protected"
			fmt.Fprintf(hash, "\x00%s:%d:%t", id, state.revision, protected)
			allowed = allowed && ok && allowedSession[id] && !protected
		}
		result.fingerprints[run.ID] = fmt.Sprintf("%x", hash.Sum(nil))
		result.allowed[run.ID] = allowed
	}
	return result, nil
}

// data_version is comparable only on the same physical SQLite connection.
// Retaining its identity in a cached stamp also prevents identity reuse after
// that connection closes. Reopened or different readers conservatively miss.
type activityThreadProjectionStamp struct {
	connection any
	changes    int64
	version    int64
	context    string
	carryDays  string
}

func (a *App) activityThreadProjectionSignature(ctx context.Context, day int64) (activityThreadProjectionStamp, bool, error) {
	store := a.activity.store
	stamp, fileBacked, err := store.activityThreadReaderStamp(ctx, day)
	if err != nil {
		return activityThreadProjectionStamp{}, false, err
	}

	mainRevision := ""
	if a.store != nil {
		revision, readErr := a.store.ReadRevision(ctx)
		mainRevision, err = fmt.Sprintf("%p:%v", a.store, revision), readErr
		if err != nil {
			return activityThreadProjectionStamp{}, false, err
		}
	}
	pauses, err := a.observationPausesContext(ctx)
	if err != nil {
		return activityThreadProjectionStamp{}, false, err
	}
	policy, err := json.Marshal(struct {
		Closed    bool
		Paused    bool
		Pauses    []observationPause
		Sensitive map[string]string
	}{a.IsClosedEyesModeEnabled(), activityThreadOutcomesPaused(pauses, time.Now()), pauses, pii.GetUserSensitiveApps()})
	if err != nil {
		return activityThreadProjectionStamp{}, false, err
	}
	// File-backed evidence can change without a SQLite write. Recheck its
	// dependency header/sidecar, rather than disabling the entire day cache.
	fileProof := ""
	if fileBacked {
		fileProof, err = store.activityThreadFileProof(ctx, day)
		if err != nil {
			return activityThreadProjectionStamp{}, false, err
		}
	}
	stamp.context = fileProof + fmt.Sprintf("%p:%d:%s:%x", store.reader(), store.readPrivacyRevision.Load(), mainRevision, sha256.Sum256(policy))
	return stamp, store.readPrivacyRevision.Load()%2 == 0, nil
}

// Lease from the reader pool only for the metadata query, then release it
// before source assembly. No read consumes capture's single writer slot.
func (s *ActivityStore) activityThreadReaderStamp(ctx context.Context, day int64) (activityThreadProjectionStamp, bool, error) {
	stamp := activityThreadProjectionStamp{}
	conn, err := s.reader().Conn(ctx)
	if err != nil {
		return stamp, false, err
	}
	defer conn.Close()
	if err := conn.Raw(func(physical any) error {
		stamp.connection = physical
		if physical == nil || !reflect.ValueOf(physical).Comparable() {
			// Unknown non-comparable drivers cannot share cache identities.
			stamp.connection = new(int)
		}
		return nil
	}); err != nil {
		return stamp, false, err
	}
	// total_changes also covers the in-memory reader() fallback, where the
	// reader and writer are the same connection. It is zero on read-only pools.
	var fileBacked bool
	// File headers/sidecars can withdraw access without a database write.
	// Include denied day sources too, so a repaired file is reconsidered.
	err = conn.QueryRowContext(ctx, `SELECT total_changes(),data_version,EXISTS(
        SELECT 1 FROM activities a JOIN json_each(a.run_ids) r
        JOIN activity_timeline_groups g ON g.id=r.value JOIN json_each(g.session_ids) j
        JOIN activity_sessions s ON s.id=j.value
        WHERE (a.day_start_ms=? OR (a.start_ms<? AND a.end_ms>?)) AND (COALESCE(s.transcript_path,'')!='' OR COALESCE(s.audio_path,'')!='')),
        (SELECT json_group_array(DISTINCT day_start_ms) FROM activities WHERE day_start_ms < ? AND end_ms > ? AND start_ms < ?)
        FROM pragma_data_version`, day, activityThreadDayEnd(day), day, day, day, activityThreadDayEnd(day)).Scan(&stamp.changes, &stamp.version, &fileBacked, &stamp.carryDays)
	return stamp, fileBacked, err
}

func (a *App) activityThreadReasonAux(ctx context.Context) (string, error) {
	mainRevision := ""
	var err error
	if a.store != nil {
		revision, readErr := a.store.ReadRevision(ctx)
		mainRevision, err = fmt.Sprintf("%p:%v", a.store, revision), readErr
		if err != nil {
			return "", err
		}
	}
	var count, last int64
	err = a.activity.store.reader().QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(rowid),0) FROM activity_thread_corrections`).Scan(&count, &last)
	return fmt.Sprintf("%s:%d:%d", mainRevision, count, last), err
}

// Hash both allowed and denied file dependency declarations so withdrawal and
// repair invalidate a completed day, even when no database row changes.
func (s *ActivityStore) activityThreadFileProof(ctx context.Context, day int64) (string, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT DISTINCT s.id, COALESCE(s.transcript_path,''), COALESCE(s.audio_path,'')
		FROM activities a JOIN json_each(a.run_ids) r
		JOIN activity_timeline_groups g ON g.id=r.value JOIN json_each(g.session_ids) j
		JOIN activity_sessions s ON s.id=j.value
		WHERE (a.day_start_ms=? OR (a.start_ms<? AND a.end_ms>?)) AND (COALESCE(s.transcript_path,'')!='' OR COALESCE(s.audio_path,'')!='') ORDER BY s.id`, day, activityThreadDayEnd(day), day)
	if err != nil {
		return "", err
	}
	var sessions []ActivitySessionRow
	for rows.Next() {
		var session ActivitySessionRow
		if err := rows.Scan(&session.ID, &session.TranscriptPath, &session.AudioPath); err != nil {
			rows.Close()
			return "", err
		}
		sessions = append(sessions, session)
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, session := range sessions {
		ids, readErr := s.transcriptSourceIDs(session, "")
		fmt.Fprintf(hash, "%q:%q:%q:%q:%v\n", session.ID, session.TranscriptPath, session.AudioPath, ids, readErr)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
