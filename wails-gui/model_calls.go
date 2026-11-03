package main

import (
	"context"
	"log"
	"strings"
	"time"

	"yansu-agent/agent"

	"wails-gui/storage"
)

// modelCallRetention is how long served-call records are kept.
const modelCallRetention = 30 * 24 * time.Hour

// recordModelRoute stores a model_route stream event: the gateway told us
// which account and model served a call. Best effort; the chat never waits
// on it.
func (a *App) recordModelRoute(source, requested string, input *agent.TaskInput, event *agent.StreamEvent) {
	if a == nil || a.store == nil || event == nil || event.Type != "model_route" {
		return
	}
	row := storage.ModelCallRow{
		SessionID: event.SessionID,
		RunID:     event.RunID,
		Requested: strings.TrimSpace(requested),
		Provider:  strings.TrimSpace(event.Provider),
		Model:     strings.TrimSpace(event.ServedModel),
		Source:    source,
	}
	if input != nil {
		row.Priority = input.Priority
		if row.Requested == "" {
			row.Requested = strings.TrimSpace(input.Model)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.store.RecordModelCall(ctx, row); err != nil {
		log.Printf("[model-calls] record: %v", err)
	}
}

// pruneModelCalls drops old served-call records. Called from startup.
func (a *App) pruneModelCalls() {
	if a == nil || a.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.store.PruneModelCalls(ctx, time.Now().Add(-modelCallRetention)); err != nil {
		log.Printf("[model-calls] prune: %v", err)
	}
}
