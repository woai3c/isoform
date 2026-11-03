package storage

import (
	"context"
	"encoding/json"
	"time"
)

// EngagementKindControl preserves why a kind is off and the user's last
// resume boundary. It lives in ui_state alongside the existing pacing rules.
type EngagementKindControl struct {
	Paused    bool  `json:"paused"`
	ResumedAt int64 `json:"resumedAt"`
	// DeliveryMode is quiet_on_today or normal; absent in older records means normal.
	DeliveryMode string `json:"deliveryMode,omitempty"`
}

func (s *Store) GetEngagementKindControl(ctx context.Context, kind string) (EngagementKindControl, error) {
	var control EngagementKindControl
	raw, err := s.GetUIState(ctx, "engagement_kind_control:"+kind)
	if err != nil || raw == "" {
		return control, err
	}
	err = json.Unmarshal([]byte(raw), &control)
	return control, err
}

// SaveEngagementKindControl commits the effective switch and its explanation
// together, so a restart cannot silently undo a pause or a resume.
func (s *Store) SaveEngagementKindControl(ctx context.Context, pref EngagementPrefRow, control EngagementKindControl) error {
	raw, err := json.Marshal(control)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO engagement_prefs (kind, weight, enabled, muted_until, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(kind) DO UPDATE SET weight = excluded.weight,
		enabled = excluded.enabled, muted_until = excluded.muted_until, updated_at = excluded.updated_at`,
		pref.Kind, pref.Weight, pref.Enabled, pref.MutedUntil, pref.UpdatedAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ui_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "engagement_kind_control:"+pref.Kind, string(raw)); err != nil {
		return err
	}
	if control.DeliveryMode == "quiet_on_today" {
		// Release notification pacing holds; explicit reminder times retain
		// their empty reason and must never be brought forward.
		if _, err = tx.ExecContext(ctx, `UPDATE engagement_moments SET hold_until = ?
			WHERE kind = ? AND state = 'scheduled' AND reason IN
			('budget', 'quiet hours', 'passive', 'screen locked', 'digest allowance', 'digest remainder', 'retry', 'no channel', 'partly sent')`, pref.UpdatedAt, pref.Kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EngagementCompletionCandidates includes partial sends and excludes items
// already attributed. Legacy acted button clicks do not prove completion.
// No page limit may silently omit a completion.
func (s *Store) EngagementCompletionCandidates(ctx context.Context, since, until time.Time) ([]EngagementMomentRow, error) {
	return s.queryEngagementMoments(ctx, `WHERE sent_at > 0 AND sent_at >= ? AND sent_at <= ?
		AND kind IN ('work_blocked', 'work_finished', 'decision_due', 'suggestion', 'commitment_slipping', 'goal_check_in', 'first_week')
		AND NOT EXISTS (SELECT 1 FROM engagement_receipts r WHERE r.moment_id = engagement_moments.id
			AND r.state = 'acted' AND r.channel = ? AND r.action = ?)`,
		-1, since.UnixMilli(), until.UnixMilli(), EngagementCompletionChannel, EngagementCompletionAction)
}

// QuietEngagementMomentsBetween reads every quiet delivery in a local-day range.
// Use the quiet receipt time: an earlier partial send can belong to another day.
// Filter before reading so unrelated sends and the default page cannot hide a kind.
func (s *Store) QuietEngagementMomentsBetween(ctx context.Context, since, until time.Time) ([]EngagementMomentRow, error) {
	return s.queryEngagementMoments(ctx, `WHERE state = 'sent' AND EXISTS (
		SELECT 1 FROM engagement_receipts r WHERE r.moment_id = engagement_moments.id
		AND r.channel = 'today_quiet' AND r.state = 'sent' AND r.at >= ? AND r.at < ?
	)`, -1, since.UnixMilli(), until.UnixMilli())
}

// QuietEngagementMomentByKey preserves once-only event identity without pacing.
func (s *Store) QuietEngagementMomentByKey(ctx context.Context, key string) (*EngagementMomentRow, error) {
	rows, err := s.queryEngagementMoments(ctx, `WHERE dedupe_key = ? AND state = 'sent'
		AND instr('+' || channel || '+', '+today_quiet+') > 0`, 1, key)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}
