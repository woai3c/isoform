package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ComposerSuggestionsBatch is the frontend-facing shape for a cached batch of
// suggestion chips rendered below the empty-state composer.
type ComposerSuggestionsBatch struct {
	ID          string   `json:"id"`
	GeneratedAt int64    `json:"generatedAt"` // unix ms
	Suggestions []string `json:"suggestions"` // up to suggestionCount entries
}

// GetComposerSuggestions returns the most recently cached suggestion batch.
// When no generated batch is cached yet (first launch, or before the
// generator produces its first result), we return a seeded default batch
// so the empty-state composer always has chips to show.
//
// Wails cannot bind methods that take context.Context as their first argument
// (frontend calls pass no ctx, so reflect receives a zero Value and panics);
// we use the app lifecycle ctx internally instead.
func (a *App) GetComposerSuggestions() (perfValue *ComposerSuggestionsBatch, perfErr error) {
	defer logPageBinding("GetComposerSuggestions", time.Now(), &perfValue)
	if a.store == nil {
		return defaultComposerSuggestionsBatch(), nil
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	row, err := a.store.LatestComposerSuggestions(ctx)
	if err != nil {
		return nil, fmt.Errorf("load latest suggestions: %w", err)
	}
	if row == nil {
		return defaultComposerSuggestionsBatch(), nil
	}
	var list []string
	if row.Suggestions != "" {
		if err := json.Unmarshal([]byte(row.Suggestions), &list); err != nil {
			return nil, fmt.Errorf("decode suggestions: %w", err)
		}
	}
	if len(list) == 0 {
		return defaultComposerSuggestionsBatch(), nil
	}
	return &ComposerSuggestionsBatch{
		ID:          row.ID,
		GeneratedAt: row.GeneratedAt,
		Suggestions: list,
	}, nil
}

// RegenerateComposerSuggestions is a DEBUG-only Wails method that forces the
// suggestion generator to produce a fresh batch immediately, bypassing
// the 60-minute floor and input-hash dedup. On success the usual
// `composer-suggestions:updated` event fires, so callers do not need to
// consume the return value beyond error handling.
//
// TODO(debug-only): remove this method and its frontend wiring once the
// feature is validated.
func (a *App) RegenerateComposerSuggestions() error {
	if a.suggestionGenerator == nil {
		return fmt.Errorf("suggestion generator not initialized")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.suggestionGenerator.ForceGenerate(ctx)
}
