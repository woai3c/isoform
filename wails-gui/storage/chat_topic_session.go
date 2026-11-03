package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
)

// ResolveTopicChatSession preserves the caller's identity until a recent
// topic run is withdrawn. Each run rotates the session at most once, including
// after restart and when a stale renderer still supplies the prior identity.
// Receipts contain only hashed run identities, never source IDs or text.
func (s *Store) ResolveTopicChatSession(ctx context.Context, tag, sessionID string, staleRuns []string) (string, error) {
	key := "chat_topic_session:" + tag
	if len(staleRuns) == 0 {
		current, err := s.GetUIState(ctx, key)
		if err != nil {
			return "", err
		}
		if current != "" {
			return current, nil
		}
		return sessionID, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	changed := false
	for _, run := range staleRuns {
		if run == "" {
			continue
		}
		digest := sha256.Sum256([]byte(run))
		receipt := key + ":withdrawn:" + hex.EncodeToString(digest[:])
		result, err := tx.ExecContext(ctx, `INSERT INTO ui_state(key,value) VALUES(?,'1') ON CONFLICT(key) DO NOTHING`, receipt)
		if err != nil {
			return "", err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		changed = changed || n > 0
	}
	current := ""
	err = tx.QueryRowContext(ctx, `SELECT value FROM ui_state WHERE key=?`, key).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if changed {
		current = "topic_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO ui_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, current); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if current == "" {
		current = sessionID
	}
	return current, nil
}
