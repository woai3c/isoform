package main

import (
	"context"
	"errors"
	"wails-gui/internal/pii"
)

// Redact before shortening evidence: cutting through a secret first can hide
// its identifying prefix or label from the detector.
func redactActivityModelText(text string) string {
	redacted, _ := pii.RedactText(text)
	return redacted
}

func (a *App) filterActivityModelSessions(store *ActivityStore, sessions []ActivitySessionRow) []ActivitySessionRow {
	return a.filterActivityModelSessionsContext(context.Background(), store, sessions)
}

func (a *App) filterActivityModelSessionsContext(ctx context.Context, store *ActivityStore, sessions []ActivitySessionRow) []ActivitySessionRow {
	policy, err := a.activityReadPrivacyPolicyContext(ctx)
	if err != nil {
		return nil
	}
	allowed := make([]ActivitySessionRow, 0, len(sessions))
	for _, session := range sessions {
		if ok, err := a.activitySessionAllowsWithPolicy(store, session, policy); err == nil && ok {
			allowed = append(allowed, session)
		}
	}
	return allowed
}

func (a *App) activityModelSessionIDs(store *ActivityStore, startMs, endMs int64, sourceIDs ...map[string]bool) map[string]bool {
	// An explicit allow set belongs to this summary operation and has already
	// checked every source. Reuse it across its app/title/OCR aggregations.
	if len(sourceIDs) > 0 {
		return sourceIDs[0]
	}
	allowed := make(map[string]bool)
	sessions, err := store.modelSessionsOverlapping(startMs, endMs)
	if err != nil {
		return allowed
	}
	for _, session := range a.filterActivityModelSessions(store, sessions) {
		allowed[session.ID] = true
	}
	return allowed
}

func (a *App) activityTimelineRunAllowsModelRead(store *ActivityStore, run ActivityTimelineRunRow) bool {
	return a.activityTimelineRunAllowsModelReadContext(context.Background(), store, run)
}

func (a *App) activityTimelineRunAllowsModelReadContext(ctx context.Context, store *ActivityStore, run ActivityTimelineRunRow) bool {
	allowed, err := a.activityTimelineRunAllowsModelReadResultContext(ctx, store, run)
	return err == nil && allowed
}

func (a *App) activityTimelineRunAllowsModelReadResult(store *ActivityStore, run ActivityTimelineRunRow) (bool, error) {
	return a.activityTimelineRunAllowsModelReadResultContext(context.Background(), store, run)
}

func (a *App) activityTimelineRunAllowsModelReadResultContext(ctx context.Context, store *ActivityStore, run ActivityTimelineRunRow) (bool, error) {
	if store == nil || store.db == nil {
		return false, errors.New("activity store unavailable")
	}
	if activitySourceAppIsSensitive(run.AppKey, run.AppName) || len(run.SessionIDs) == 0 {
		return false, nil
	}
	sessions, err := store.GetSessionsByIDs(run.SessionIDs)
	if err != nil {
		return false, err
	}
	for _, id := range run.SessionIDs {
		session, ok := sessions[id]
		if !ok || session == nil {
			return false, nil
		}
		allowed, err := a.activitySessionAllowsModelReadResultContext(ctx, store, *session)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

// Load every overlapping source before privacy filtering and prompt caps. The
// UI's bounded, fully-contained session query is unsuitable for this purpose.
func (s *ActivityStore) modelSessionsOverlapping(startMs, endMs int64) ([]ActivitySessionRow, error) {
	return s.modelSessionsOverlappingContext(context.Background(), startMs, endMs)
}

func (s *ActivityStore) modelSessionsOverlappingContext(ctx context.Context, startMs, endMs int64) ([]ActivitySessionRow, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store unavailable")
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id FROM activity_sessions s WHERE s.end_time >= ? AND s.start_time < ?`+sqlExcludeProtected+` ORDER BY s.start_time DESC`, startMs, endMs)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	readErr, closeErr := rows.Err(), rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	byID, err := s.GetSessionsByIDsContext(ctx, ids)
	if err != nil {
		return nil, err
	}
	var sessions []ActivitySessionRow
	for _, id := range ids {
		if session := byID[id]; session != nil {
			sessions = append(sessions, *session)
		}
	}
	return sessions, nil
}

func composerBucketContainsSensitiveApp(bucket ActivityRow) bool {
	for app := range composeParseInt64Map(bucket.ActiveMsByApp) {
		if activitySourceAppIsSensitive(app, app) {
			return true
		}
	}
	return false
}

// The compositor and registry share the same bucket/run eligibility rule;
// each caller supplies its own fresh or request-cached provenance reader.
func composerCandidateAllowsModelRead(candidate ActivityRow, allowRun func(string) bool) bool {
	if composerBucketContainsSensitiveApp(candidate) {
		return false
	}
	ids := composeParseStrings(candidate.RunIDs)
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !allowRun(id) {
			return false
		}
	}
	return true
}
func (a *App) filterComposerModelCandidates(store *ActivityStore, candidates []ActivityRow) []ActivityRow {
	return a.filterComposerModelCandidatesContext(context.Background(), store, candidates)
}

func (a *App) filterComposerModelCandidatesContext(ctx context.Context, store *ActivityStore, candidates []ActivityRow) []ActivityRow {
	allowed := make([]ActivityRow, 0, len(candidates))
	for _, candidate := range candidates {
		if composerCandidateAllowsModelRead(candidate, func(id string) bool {
			run, err := store.GetTimelineRunByIDContext(ctx, id)
			return err == nil && run != nil && a.activityTimelineRunAllowsModelReadContext(ctx, store, *run)
		}) {
			allowed = append(allowed, candidate)
		}
	}
	return allowed
}
