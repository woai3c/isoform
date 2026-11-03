package storage

import (
	"context"
	"time"
)

// ModelCallRow records which account and model served a call, as the
// gateway reported it. Requested is the model or group the app asked for.
type ModelCallRow struct {
	ID        int64
	At        int64
	SessionID string
	RunID     string
	Requested string
	Provider  string
	Model     string
	Priority  string
	// Source is the caller: chat, cron, agency, ephemeral.
	Source string
}

// RecordModelCall stores one served call. At defaults to now.
func (s *Store) RecordModelCall(ctx context.Context, r ModelCallRow) error {
	if r.At == 0 {
		r.At = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO model_calls (at, session_id, run_id, requested, provider, model, priority, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, r.At, r.SessionID, r.RunID, r.Requested, r.Provider, r.Model, r.Priority, r.Source)
	return err
}

// RecentModelCalls returns the newest calls first, up to limit (default 50).
func (s *Store) RecentModelCalls(ctx context.Context, limit int) ([]ModelCallRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, session_id, run_id, requested, provider, model, priority, source
		FROM model_calls ORDER BY at DESC, id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelCallRow
	for rows.Next() {
		var r ModelCallRow
		if err := rows.Scan(&r.ID, &r.At, &r.SessionID, &r.RunID, &r.Requested, &r.Provider, &r.Model, &r.Priority, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneModelCalls deletes calls older than the given time.
func (s *Store) PruneModelCalls(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM model_calls WHERE at < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
