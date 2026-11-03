package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"wails-gui/storage"
	agent "yansu-agent/agent"
)

type cronRunRouteContextKey struct{}

// cronRunRouteRecorder is bound to one scheduler execution, not its shared log
// session. Persisting each route event preserves attribution if the app exits
// during an assistant attempt or that attempt returns no final result.
type cronRunRouteRecorder struct {
	mu      sync.Mutex
	row     storage.CronRunRow
	blocked bool
	persist func(storage.CronRunRow) error
	// Action counts protect crash recovery from replaying an acted occurrence.
	// Unlike display attribution, a failed receipt must keep dispatch closed.
	actionPersistErr error
}

func withCronRunRoute(ctx context.Context, recorder *cronRunRouteRecorder) context.Context {
	return context.WithValue(ctx, cronRunRouteContextKey{}, recorder)
}

func recordCronRunRoute(ctx context.Context, event *agent.StreamEvent) error {
	recorder, _ := ctx.Value(cronRunRouteContextKey{}).(*cronRunRouteRecorder)
	if recorder == nil || event == nil {
		return nil
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.actionPersistErr != nil {
		return recorder.actionPersistErr
	}
	previous := recorder.row
	if event.Backend != "" {
		if recorder.row.Backend != event.Backend {
			// The previous assistant's model must never label its fallback.
			recorder.row.Model = ""
		}
		recorder.row.Backend = event.Backend
	}
	if event.CandidateBackends != nil {
		encoded, _ := json.Marshal(event.CandidateBackends)
		recorder.row.CandidateBackends = string(encoded)
	}
	if event.FallbackFrom != nil {
		encoded, _ := json.Marshal(event.FallbackFrom)
		recorder.row.FallbackFrom = string(encoded)
	}
	if event.BackendReason != "" {
		recorder.row.BackendReason = event.BackendReason
	}
	if event.ServedModel != "" {
		recorder.row.Model = event.ServedModel
	}
	if event.ActionsStarted > recorder.row.ActionsStarted {
		recorder.row.ActionsStarted = event.ActionsStarted
	}
	recorder.blocked = recorder.blocked || event.Blocked
	if previous != recorder.row && recorder.persist != nil {
		if err := recorder.persist(recorder.row); err != nil {
			// Startup recovery treats a persisted zero count as safe to retry.
			// When a write also carries a new action receipt, it must succeed
			// before dispatch even if it includes ordinary route metadata.
			if recorder.row.ActionsStarted > previous.ActionsStarted {
				recorder.actionPersistErr = err
				return err
			}
			// Attribution is best effort. Keep the in-memory route for final
			// history and retry persistence when another metadata field changes.
			log.Printf("[cron] failed to persist attribution for run %s: %v", recorder.row.ID, err)
		}
	}
	return nil
}

func recordCronRunLogPosition(ctx context.Context, tag string, sortStart int) {
	recorder, _ := ctx.Value(cronRunRouteContextKey{}).(*cronRunRouteRecorder)
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.row.SessionTag = tag
	recorder.row.ItemSortStart = sortStart
}

func (r *cronRunRouteRecorder) snapshot() (storage.CronRunRow, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.row, r.blocked
}

func applyCronRunRoute(dst *storage.CronRunRow, src storage.CronRunRow) {
	dst.CandidateBackends = src.CandidateBackends
	dst.Backend = src.Backend
	dst.FallbackFrom = src.FallbackFrom
	dst.BackendReason = src.BackendReason
	dst.Model = src.Model
	dst.ActionsStarted = src.ActionsStarted
}

func cronRunRouteFields(row storage.CronRunRow) map[string]any {
	return map[string]any{
		"candidate_backends": row.CandidateBackends,
		"backend":            row.Backend,
		"fallback_from":      row.FallbackFrom,
		"backend_reason":     row.BackendReason,
		"model":              row.Model,
		"actions_started":    row.ActionsStarted,
	}
}

func cronRouteBackends(raw string) []string {
	var backends []string
	_ = json.Unmarshal([]byte(raw), &backends)
	return backends
}
