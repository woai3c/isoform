package storage

import "context"

// FindEngagementWorkMoment reconnects a legacy work card to its original
// notification. Identity and the observed transition interval must both match;
// a newer run of the same work must not receive an older card's receipts.
func (s *Store) FindEngagementWorkMoment(ctx context.Context, kind, subjectID, dedupeKey string, since, until int64) (*EngagementMomentRow, error) {
	rows, err := s.queryEngagementMoments(ctx, `WHERE id = (
		SELECT id FROM engagement_moments
		WHERE kind = ? AND subject_id = ? AND dedupe_key = ? AND created_at BETWEEN ? AND ?
		ORDER BY created_at DESC, id DESC LIMIT 1
	)`, 1, kind, subjectID, dedupeKey, since, until)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}
