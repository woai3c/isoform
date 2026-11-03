package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/pii"
)

// A verified partial is display-only, just like the persisted last-good view.
// Reuse it under the same database/file/policy gates instead of spending the
// entire cold budget verifying the same contributors on every UI poll. Nil
// views are unverified; invalid distinguishes confirmed withdrawal from a deadline.
func (s *topicsService) stablePartialTodayViews(ctx context.Context, state topicsStateFile) ([]TopicView, bool, bool) {
	invalid := false
	s.registryCacheMu.RLock()
	partial := s.registryPartial
	s.registryCacheMu.RUnlock()
	if partial != nil {
		candidate := state
		candidate.Today = partial
		readable, retry, proofInvalid := s.readableTodaySnapshotWithRetry(ctx, candidate)
		if readable {
			return partial.Views, false, false
		}
		if !proofInvalid {
			return nil, retry, false
		}
		invalid = true
		s.registryCacheMu.Lock()
		if s.registryPartial == partial {
			s.registryPartial = nil
		}
		s.registryCacheMu.Unlock()
	}
	if !s.beginPartialTodayAttempt() {
		return nil, true, invalid
	}
	defer s.endPartialTodayAttempt()
	before, err, retry := s.partialTodaySnapshot(ctx, state)
	if err != nil {
		return nil, retry, invalid || ctx.Err() == nil
	}
	views, verified, retry := s.partialTodayViewsWithSnapshot(ctx, state, before)
	if !verified || ctx.Err() != nil {
		return nil, retry, invalid || ctx.Err() == nil
	}
	before.Views = views
	for _, view := range views {
		if view.Why != nil {
			if at, err := time.Parse(time.RFC3339, view.Why.LastSeenAt); err == nil {
				boundary := at.Add(interests.RefreshWindow)
				if boundary.After(before.At) && boundary.Before(before.Until) {
					before.Until = boundary
				}
			}
		}
		if view.Latest != nil {
			if at, err := time.Parse(time.RFC3339, view.Latest.At); err == nil {
				boundary := at.Add(topicRegistryMovingFor)
				if boundary.After(before.At) && boundary.Before(before.Until) {
					before.Until = boundary
				}
			}
		}
	}
	candidate := state
	candidate.Today = before
	readable, retry, proofInvalid := s.readableTodaySnapshotWithRetry(ctx, candidate)
	invalid = invalid || proofInvalid
	if readable {
		s.registryCacheMu.Lock()
		s.registryPartial = before
		s.registryCacheMu.Unlock()
		return views, false, false
	}
	return nil, retry, invalid || ctx.Err() == nil
}

func (s *topicsService) beginPartialTodayAttempt() bool {
	s.registryCacheMu.Lock()
	defer s.registryCacheMu.Unlock()
	if s.registryPartialRunning || s.now().Before(s.registryPartialNext) {
		return false
	}
	s.registryPartialRunning = true
	s.registryPartialNext = s.now().Add(topicRefreshInterval)
	return true
}

func (s *topicsService) endPartialTodayAttempt() {
	s.registryCacheMu.Lock()
	s.registryPartialRunning = false
	s.registryCacheMu.Unlock()
}

func (s *topicsService) resetPartialTodayAttempts() {
	s.registryCacheMu.Lock()
	s.registryPartialNext = time.Time{}
	s.registryCacheMu.Unlock()
}

func (s *topicsService) partialTodaySnapshot(ctx context.Context, state topicsStateFile) (*topicTodaySnapshot, error, bool) {
	paths, err := s.registryDatabaseFiles(ctx)
	if err != nil {
		return nil, err, false
	}
	stats, err := topicSessionFileStats(ctx, paths)
	if err != nil {
		return nil, err, ctx.Err() != nil
	}
	files, err := s.registrySourceFileStats(ctx)
	if err != nil {
		return nil, err, ctx.Err() != nil
	}
	policy, err := json.Marshal([]any{false, pii.GetUserSensitiveApps(), GetUserVoiceInputApps()})
	if err != nil {
		return nil, err, false
	}
	now := s.now()
	until := now.Add(time.Minute)
	if midnight := time.UnixMilli(startOfDayLocal(now.UnixMilli())).In(time.Local).AddDate(0, 0, 1); midnight.Before(until) {
		until = midnight
	}
	return &topicTodaySnapshot{State: topicTodayStateHash(state), DatabaseFiles: paths, DatabaseStats: stats,
		Sources: files, Interests: proactiveVoiceHash(s.app.interestsSvc().store.load()),
		Policy: sha256.Sum256(policy), At: now, Until: until}, ctx.Err(), ctx.Err() != nil
}

// File provenance follows the bounded original sources, never a scan of all
// historical capture sessions. Missing/over-cap closures still fail the normal
// source gates; collecting extra metadata grants no permission to their text.
func (s *topicsService) partialTodaySessionFiles(ctx context.Context, state topicsStateFile, activities []ActivityRow) (topicSessionFileRevision, error) {
	store := s.app.activity.store
	sessions, runs, semantics := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range activities {
		ids := composeParseStrings(row.RunIDs)
		if len(ids) <= 64 {
			for _, id := range ids {
				runs[id] = true
			}
		}
	}
	for _, item := range s.app.interestsSvc().store.load().Interests {
		if state.Overrides[item.ID].Removed {
			continue
		}
		for _, ref := range item.Evidence {
			if ref.Source == interests.SourceActivity && strings.HasPrefix(ref.Key, "semantic:") && len(semantics) < topicTodaySemanticLimit {
				if id := strings.TrimPrefix(ref.Key, "semantic:"); id != "" {
					semantics[id] = true
				}
			}
		}
	}
	query := func(sql string, ids map[string]bool, semantic bool) error {
		encoded, _ := json.Marshal(sortedTopicKeys(ids))
		rows, err := store.reader().QueryContext(ctx, sql, string(encoded))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			ids := composeParseStrings(raw)
			if semantic {
				var refs semanticSourceRefs
				if json.Unmarshal([]byte(raw), &refs) != nil {
					continue
				}
				ids = refs.SessionIDs
			}
			if len(ids) <= 64 {
				for _, id := range ids {
					sessions[id] = true
				}
			}
		}
		return rows.Err()
	}
	if err := query(`SELECT session_ids FROM activity_timeline_groups WHERE id IN (SELECT value FROM json_each(?))`, runs, false); err != nil {
		return topicSessionFileRevision{}, err
	}
	if err := query(`SELECT source_refs FROM activity_semantic_events WHERE id IN (SELECT value FROM json_each(?))`, semantics, true); err != nil {
		return topicSessionFileRevision{}, err
	}
	encoded, _ := json.Marshal(sortedTopicKeys(sessions))
	rows, err := store.reader().QueryContext(ctx, `SELECT COALESCE(transcript_path,''),COALESCE(audio_path,'') FROM activity_sessions WHERE id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return topicSessionFileRevision{}, err
	}
	paths := map[string]bool{}
	headers := map[string]bool{}
	for rows.Next() {
		var transcript, audio string
		if err := rows.Scan(&transcript, &audio); err != nil {
			rows.Close()
			return topicSessionFileRevision{}, err
		}
		if transcript != "" {
			paths[transcript] = true
			if audio == "" {
				headers[transcript] = true
			}
		}
		if audio != "" {
			paths[meetingWAVMetaPath(store.AudioDir(), audio)] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return topicSessionFileRevision{}, err
	}
	return topicRecordingFileSnapshot(ctx, store.AudioDir(), paths, headers, s.registryRecordingHeadersRead)
}
