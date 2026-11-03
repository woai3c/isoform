package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Keep only app/time/private facts when raw capture expires. These receipts
// authorize derivative reads, never execution or reconstruction of raw activity.
func migrateActivityRetainedSourcePrivacy(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS activity_retained_source_privacy (
		session_id TEXT PRIMARY KEY,
		provenance TEXT NOT NULL
	)`)
	return err
}

func saveActivitySourcePrivacyReceipt(tx *sql.Tx, source activitySourceProvenance) error {
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO activity_retained_source_privacy(session_id,provenance) VALUES(?,?)
		ON CONFLICT(session_id) DO UPDATE SET provenance=excluded.provenance`, source.SessionID, string(raw))
	return err
}

// Resolve derivative provenance without requiring expired raw capture. Stored
// app names support legacy derivatives predating receipts, but unknown source
// identities and storage failures never authorize reads.
func (s *ActivityStore) activityDependencyProvenance(id string, storedApps []string) (activitySourceProvenance, error) {
	if s == nil || s.db == nil || strings.TrimSpace(id) == "" {
		return activitySourceProvenance{}, errors.New("activity source unavailable")
	}
	source, err := s.activitySessionProvenance(ActivitySessionRow{ID: id})
	if !errors.Is(err, sql.ErrNoRows) {
		return source, err
	}
	source, err = s.retainedActivitySourceProvenance(id)
	if !errors.Is(err, sql.ErrNoRows) {
		return source, err
	}
	apps := uniqueNonEmpty(storedApps, strings.TrimSpace)
	if len(apps) == 0 {
		return source, err
	}
	return activitySourceProvenance{SessionID: id, Apps: apps, Retained: true}, nil
}

func (s *ActivityStore) activityDependencyProvenanceWithInputs(id string, storedApps []string, session *ActivitySessionRow, inputs *activityPrivacyProvenanceInputs) (activitySourceProvenance, error) {
	if session != nil && inputs != nil {
		return s.activitySessionProvenanceWithInputs(*session, inputs)
	}
	return s.activityDependencyProvenance(id, storedApps)
}

func (s *ActivityStore) retainedActivitySourceProvenance(id string) (activitySourceProvenance, error) {
	var source activitySourceProvenance
	var raw string
	if err := s.reader().QueryRow(`SELECT provenance FROM activity_retained_source_privacy WHERE session_id=?`, id).Scan(&raw); err != nil {
		return source, err
	}
	if json.Unmarshal([]byte(raw), &source) != nil || source.SessionID != id || !source.Retained {
		return source, errors.New("activity source unavailable")
	}
	return source, nil
}

func (a *App) activityDependencyAllowsModelRead(id string, storedApps []string) bool {
	return a.activityDependencyAllowsModelReadWithCache(id, storedApps, nil)
}

func (a *App) activityDependencyAllowsModelReadWithCache(id string, storedApps []string, cache *peopleReadGateCache) bool {
	return a.activityDependencyAllowsModelReadWithCacheContext(context.Background(), id, storedApps, cache)
}

func (a *App) activityDependencyAllowsModelReadWithCacheContext(ctx context.Context, id string, storedApps []string, cache *peopleReadGateCache) bool {
	if a == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(ctx) || a.activity == nil || a.activity.store == nil {
		return false
	}
	var session *ActivitySessionRow
	if cache != nil {
		var sessionSeen bool
		session, sessionSeen = cache.sessionRows[id]
		if !sessionSeen || session == nil {
			session = nil
		}
	}
	var source activitySourceProvenance
	var err error
	if cache != nil && session != nil {
		source, err = a.activitySessionProvenanceWithPeopleCache(a.activity.store, *session, nil, cache)
	} else {
		source, err = a.activity.store.activityDependencyProvenanceWithInputs(id, storedApps, session, nil)
	}
	if err != nil {
		return false
	}
	policy, err := a.activityReadPrivacyPolicyContextWithPeopleCache(ctx, cache)
	return err == nil && policy.allows(source)
}
