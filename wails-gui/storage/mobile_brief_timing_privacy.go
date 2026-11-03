package storage

import (
	"context"
	"errors"
	"strconv"
)

const mobileBriefTimingPrivacySinceKey = "mobile_brief_timing_privacy_since"

// EnsureMobileBriefTimingPrivacySince records when the adaptive reader first
// has authoritative pause coverage. Older installations may already have
// discarded exclusions; an empty log cannot authorize their old observations.
// Insert-once makes simultaneous schedule reads agree without moving the floor.
func (s *Store) EnsureMobileBriefTimingPrivacySince(ctx context.Context, now int64) (int64, error) {
	if now <= 0 {
		return 0, errors.New("invalid timing privacy initialization time")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO ui_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`, mobileBriefTimingPrivacySinceKey, strconv.FormatInt(now, 10))
	if err != nil {
		return 0, err
	}
	raw, err := s.GetUIState(ctx, mobileBriefTimingPrivacySinceKey)
	if err != nil {
		return 0, err
	}
	since, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || since <= 0 || since > now {
		return 0, errors.New("timing privacy coverage is unavailable")
	}
	return since, nil
}
