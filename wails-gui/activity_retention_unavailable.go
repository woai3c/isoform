package main

import "database/sql"

// Availability is separate from privacy and from a receipt of deletion. A run
// may outlive a member purged before receipts existed; keep only opaque IDs so
// its goal stays content-free after membership expires, even if deletion fails.
func migrateActivityRetentionUnavailable(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS activity_retention_unavailable_v1 (
		source_kind TEXT NOT NULL CHECK (source_kind IN ('activity_session', 'activity_run')),
		source_id TEXT NOT NULL,
		PRIMARY KEY (source_kind, source_id)
	)`)
	return err
}

func (s *ActivityStore) activityRetentionEvidenceUnavailable(kind, id string) (bool, error) {
	var unavailable bool
	err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM activity_retention_unavailable_v1
		WHERE source_kind=? AND source_id=?)`, kind, id).Scan(&unavailable)
	return unavailable, err
}
