package main

import (
	"context"
	"database/sql"
	"strings"
	"sync"
)

// activityDependencyReadGate answers activityDependencyAllowsModelRead for the
// many sessions behind one read. The pause policy is read once, and the
// privacy state of the prefetched sessions in one query (plus one for
// retained receipts), instead of a query pair per session. It keeps nothing
// past its read, so a privacy change applies on the next read.
type activityDependencyReadGate struct {
	a   *App
	ctx context.Context

	mu        sync.Mutex
	policy    activityReadPrivacyPolicy
	policyErr error
	policySet bool
	loaded    map[string]bool
	states    map[string]activityPrivacySourceState
	retained  map[string]string
	verdicts  map[string]bool
}

func (a *App) newActivityDependencyReadGate(ctx context.Context) *activityDependencyReadGate {
	return &activityDependencyReadGate{
		a:        a,
		ctx:      ctx,
		loaded:   map[string]bool{},
		states:   map[string]activityPrivacySourceState{},
		retained: map[string]string{},
		verdicts: map[string]bool{},
	}
}

func (g *activityDependencyReadGate) store() *ActivityStore {
	if g.a == nil || g.a.activity == nil {
		return nil
	}
	return g.a.activity.store
}

// prefetch reads the privacy state of the ids not read yet. If the batch
// fails, those ids keep the per-session check.
func (g *activityDependencyReadGate) prefetch(ids []string) {
	store := g.store()
	if store == nil || store.db == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || g.loaded[id] || seen[id] {
			continue
		}
		seen[id] = true
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return
	}
	tx, err := store.reader().BeginTx(g.ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	states, err := store.batchActivityPrivacySourceStatesContext(g.ctx, tx, missing)
	if err != nil {
		return
	}
	var absent []string
	for _, id := range missing {
		if _, found := states[id]; !found {
			absent = append(absent, id)
		}
	}
	retained, err := store.retainedActivitySourceProvenancesContext(g.ctx, tx, absent)
	if err != nil || tx.Commit() != nil {
		return
	}
	for id, state := range states {
		g.states[id] = state
	}
	for id, raw := range retained {
		g.retained[id] = raw
	}
	for _, id := range missing {
		g.loaded[id] = true
	}
}

// allows is activityDependencyAllowsModelReadWithCacheContext for one session.
func (g *activityDependencyReadGate) allows(id string, storedApps []string) bool {
	a := g.a
	if a == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(g.ctx) || g.store() == nil {
		return false
	}
	key := id + "\x00" + strings.Join(storedApps, "\x00")
	g.mu.Lock()
	loaded := g.loaded[id]
	verdict, decided := g.verdicts[key]
	g.mu.Unlock()
	if decided {
		return verdict
	}
	if !loaded {
		return a.activityDependencyAllowsModelReadWithCacheContext(g.ctx, id, storedApps, nil)
	}
	source, err := g.source(id, storedApps)
	allowed := false
	if err == nil {
		policy, policyErr := g.readPolicy()
		allowed = policyErr == nil && policy.allows(source)
	}
	g.mu.Lock()
	g.verdicts[key] = allowed
	g.mu.Unlock()
	return allowed
}

// source follows activityDependencyProvenance on the prefetched state: the
// live session, else its retained receipt, else the stored app names.
func (g *activityDependencyReadGate) source(id string, storedApps []string) (activitySourceProvenance, error) {
	store := g.store()
	g.mu.Lock()
	state, live := g.states[id]
	raw, kept := g.retained[id]
	g.mu.Unlock()
	if live {
		if err := store.validateActivityPrivacySourceSchemaContext(g.ctx, store.reader(), state); err != nil {
			return activitySourceProvenance{}, err
		}
		if source, found := store.cachedActivitySessionProvenance(id, state); found {
			return source, nil
		}
		return store.activitySessionProvenanceContext(g.ctx, ActivitySessionRow{ID: id})
	}
	if kept {
		return decodeRetainedActivitySourceProvenance(id, raw)
	}
	apps := uniqueNonEmpty(storedApps, strings.TrimSpace)
	if len(apps) == 0 {
		return activitySourceProvenance{}, sql.ErrNoRows
	}
	return activitySourceProvenance{SessionID: id, Apps: apps, Retained: true}, nil
}

func (g *activityDependencyReadGate) readPolicy() (activityReadPrivacyPolicy, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.policySet {
		g.policy, g.policyErr = g.a.activityReadPrivacyPolicyContext(g.ctx)
		g.policySet = true
	}
	return g.policy, g.policyErr
}

// sessionRows is GetSessionsByIDsContext (protected sessions left out) for
// prefetched ids, with the fields the derived-source gates read.
func (g *activityDependencyReadGate) sessionRows(ids []string) (map[string]*ActivitySessionRow, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]*ActivitySessionRow, len(ids))
	for _, id := range ids {
		if !g.loaded[id] {
			return nil, false
		}
		state, live := g.states[id]
		if !live || state.status == "protected" {
			continue
		}
		out[id] = &ActivitySessionRow{ID: id, StartTime: state.start, EndTime: state.end, AppNames: state.apps, TriageStatus: state.status, MeetingApp: state.meeting}
	}
	return out, true
}
