package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SaveComposerSuggestions inserts a new composer suggestions batch.
// Returns the row's ID. CreatedAt / GeneratedAt default to now if zero.
func (s *Store) SaveComposerSuggestions(ctx context.Context, r ComposerSuggestionsRow) error {
	if r.ID == "" {
		return errors.New("ComposerSuggestionsRow.ID is required")
	}
	now := time.Now().UnixMilli()
	if r.GeneratedAt == 0 {
		r.GeneratedAt = now
	}
	if r.ExpiresAt == 0 {
		r.ExpiresAt = r.GeneratedAt + 24*60*60*1000
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO composer_suggestions (id, generated_at, suggestions, source_hash, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, r.ID, r.GeneratedAt, r.Suggestions, r.SourceHash, r.ExpiresAt)
	return err
}

// LatestComposerSuggestions returns the most recently generated batch, or nil
// if the table is empty.
func (s *Store) LatestComposerSuggestions(ctx context.Context) (*ComposerSuggestionsRow, error) {
	var r ComposerSuggestionsRow
	err := s.db.QueryRowContext(ctx, `
		SELECT id, generated_at, suggestions, source_hash, expires_at
		FROM composer_suggestions
		ORDER BY generated_at DESC
		LIMIT 1
	`).Scan(&r.ID, &r.GeneratedAt, &r.Suggestions, &r.SourceHash, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// CleanupComposerSuggestions removes expired rows and keeps at most `keepRecent`
// of the most recent rows for debugging. Runs best-effort; callers may ignore errors.
func (s *Store) CleanupComposerSuggestions(ctx context.Context, keepRecent int) error {
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM composer_suggestions WHERE expires_at < ?", now,
	); err != nil {
		return err
	}
	if keepRecent <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM composer_suggestions
		WHERE id NOT IN (
			SELECT id FROM composer_suggestions
			ORDER BY generated_at DESC
			LIMIT ?
		)
	`, keepRecent)
	return err
}
