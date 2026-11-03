package storage

import (
	"context"
	"strings"
	"time"
)

// Pacing is internal delivery evidence, separate from the earliest measured
// chat or channel send. Its action retains the interruption level at delivery.
const engagementPacingChannel = "pacing"

type EngagementPacingRow struct {
	MomentID  string
	Kind      string
	DedupeKey string
	Level     string
	At        int64
}

func recordEngagementPacing(ctx context.Context, db engagementReceiptWriter, id, state, channel, level string, at int64) error {
	if state != EngagementStateSent || at <= 0 || strings.Contains("+"+channel+"+", "+today_quiet+") {
		return nil
	}
	switch level {
	case "passive", "active", "time-sensitive":
	default:
		return nil
	}
	return insertEngagementReceipt(ctx, db, EngagementReceiptRow{
		MomentID: id, Channel: engagementPacingChannel, State: EngagementReceiptSent, Action: level, At: at,
	})
}

// EngagementPacingBetween restores the events that consumed a pacing slot,
// including their actual day and interruption level. A durable chat card may
// precede this event by days and must not move it to an earlier budget. Older
// sent rows with a delivery level fall back to their original sent_at; unpaced
// chat-only moments have no delivery level and do not consume a slot.
func (s *Store) EngagementPacingBetween(ctx context.Context, since, until time.Time) ([]EngagementPacingRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.kind, m.dedupe_key, COALESCE(p.action, m.level), COALESCE(p.at, m.sent_at)
		FROM engagement_moments m
		LEFT JOIN engagement_receipts p ON p.moment_id = m.id AND p.channel = ? AND p.state = ?
		WHERE (p.id IS NOT NULL OR (m.state = ? AND m.level IN ('passive', 'active', 'time-sensitive')))
			AND COALESCE(p.at, m.sent_at) BETWEEN ? AND ?
			AND instr('+' || m.channel || '+', '+today_quiet+') = 0
		ORDER BY COALESCE(p.at, m.sent_at), m.id
	`, engagementPacingChannel, EngagementReceiptSent, EngagementStateSent, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EngagementPacingRow
	for rows.Next() {
		var row EngagementPacingRow
		if err := rows.Scan(&row.MomentID, &row.Kind, &row.DedupeKey, &row.Level, &row.At); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
