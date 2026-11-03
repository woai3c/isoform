package main

import (
	"context"
	"fmt"
	"sort"

	"wails-gui/storage"
)

// Fetch session metadata in one batch rather than once per unique sighting.
// This primes facts only. Each read still runs the existing privacy gates,
// and denied/missing sources never turn into positive permission decisions.
func (a *App) preloadPeopleSessionRows(ctx context.Context, principals []storage.PrincipalRow, cache *peopleReadGateCache) {
	if cache == nil {
		return
	}
	principalIDs := make([]string, 0, len(principals))
	for _, principal := range principals {
		principalIDs = append(principalIDs, principal.ID)
	}
	sightings, err := a.principalSessionSightingsForReadBatch(ctx)
	if err != nil {
		return // Retain the existing per-person error handling.
	}
	if a.peopleLineageProbe != nil {
		for range principalIDs {
			a.peopleLineageProbe()
		}
	}
	ids := map[string]bool{}
	for _, principalID := range principalIDs {
		cache.sightings[principalID] = sightings[principalID]
	}
	for _, rows := range sightings {
		for _, row := range rows {
			ids[row.SessionID] = true
		}
	}
	if a.activity == nil || a.activity.store == nil {
		return
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	store := a.activity.store
	tx, err := store.reader().BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	facts, err := store.batchActivityPrivacyPeopleSessionFactsContext(ctx, tx, keys)
	if err != nil {
		return // A batch failure falls back to the original per-row reads.
	}
	sessionRows := make(map[string]*ActivitySessionRow, len(keys))
	states := make(map[string]activityPrivacySourceState, len(keys))
	loadedIDs := make([]string, 0, len(facts))
	for _, id := range keys {
		fact, found := facts[id]
		if !found || fact.session.TriageStatus == "protected" {
			sessionRows[id] = nil
			continue
		}
		session := fact.session
		sessionRows[id] = &session
		states[id] = fact.state
		loadedIDs = append(loadedIDs, id)
	}
	var sources, updates map[string]activitySourceProvenance
	if len(loadedIDs) > 0 && !a.IsClosedEyesModeEnabled() {
		sources, updates, err = a.preloadPeopleSessionProvenance(ctx, tx, loadedIDs, states)
		if err != nil {
			return // A batch failure falls back to the original per-row reads.
		}
	}
	if err := tx.Commit(); err != nil {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}
	for id, session := range sessionRows {
		cache.sessionRows[id] = session
	}
	for id, source := range updates {
		store.rememberActivitySessionProvenance(id, states[id], source)
	}
	if len(sources) > 0 {
		if cache.sources == nil {
			cache.sources = make(map[string]activitySourceProvenance, len(sources))
		}
		for id, source := range sources {
			cache.sources[id] = source
		}
	}
}

func (a *App) principalSessionSightingsForReadBatch(ctx context.Context) (map[string][]storage.PrincipalSessionSightingRow, error) {
	before, err := a.store.ReadRevision(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.store.ListPrincipalSessionSightingsForActivePrincipals(ctx)
	if err != nil {
		return nil, err
	}
	after, err := a.store.ReadRevision(ctx)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, storage.ErrReadRevisionChanged
	}
	return rows, ctx.Err()
}

func (a *App) preloadPeopleSessionProvenance(ctx context.Context, tx activityPrivacyQueryer, sessionIDs []string, states map[string]activityPrivacySourceState) (map[string]activitySourceProvenance, map[string]activitySourceProvenance, error) {
	if len(sessionIDs) == 0 {
		return nil, nil, nil
	}
	sort.Strings(sessionIDs)
	store := a.activity.store
	var schemaState activityPrivacySourceState
	hasState := false
	for _, id := range sessionIDs {
		if state, ok := states[id]; ok {
			schemaState = state
			hasState = true
			break
		}
	}
	if !hasState {
		return nil, nil, fmt.Errorf("missing activity privacy source state")
	}
	if err := store.validateActivityPrivacySourceSchemaContext(ctx, tx, schemaState); err != nil {
		return nil, nil, err
	}

	sources := make(map[string]activitySourceProvenance, len(states))
	misses := make([]string, 0, len(states))
	for _, id := range sessionIDs {
		state, ok := states[id]
		if !ok {
			return nil, nil, fmt.Errorf("missing activity privacy source state for %s", id)
		}
		if source, found := store.cachedActivitySessionProvenance(id, state); found {
			sources[id] = source
		} else {
			misses = append(misses, id)
		}
	}
	inputs, err := store.batchActivityPrivacyProvenanceInputsWithQueryer(ctx, tx, misses)
	if err != nil {
		return nil, nil, err
	}
	updates := make(map[string]activitySourceProvenance, len(misses))
	for _, id := range misses {
		state := states[id]
		session := ActivitySessionRow{
			ID:           id,
			StartTime:    state.start,
			EndTime:      state.end,
			AppNames:     state.apps,
			TriageStatus: state.status,
			MeetingApp:   state.meeting,
		}
		input := inputs[id]
		source, err := store.decodeActivitySessionProvenanceInputs(session, input.eventData, input.snapshots)
		if err != nil {
			return nil, nil, err
		}
		sources[id] = source
		updates[id] = source
	}
	return sources, updates, nil
}
