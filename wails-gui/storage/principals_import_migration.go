package storage

import (
	"fmt"
	"strings"
)

// migratePrincipalImportProvenance retains the earlier import provenance while
// leaving manually curated principals unmarked, even when they own an imported
// app handle. The earliest source handle is the canonical external reference;
// any other identifiers stay available through app_handles for deduplication.
func (s *Store) migratePrincipalImportProvenance() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	for _, column := range []string{"source", "external_ref"} {
		if _, err := tx.Exec("ALTER TABLE principals ADD COLUMN " + column + " TEXT NOT NULL DEFAULT ''"); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("add %s: %w", column, err)
		}
	}
	if _, err := tx.Exec(`
		UPDATE principals
		   SET source = created_from
		 WHERE source = '' AND created_from IN ('system_contacts', 'vcard');
		UPDATE principals
		   SET external_ref = COALESCE((
		       SELECT h.handle_id FROM app_handles h
		        WHERE h.principal_id = principals.id
		          AND h.app = CASE principals.source WHEN 'system_contacts' THEN 'contacts' ELSE 'vcard' END
		          AND h.handle_id != ''
		        ORDER BY h.id LIMIT 1
		   ), '')
		 WHERE external_ref = '' AND source IN ('system_contacts', 'vcard');
	`); err != nil {
		return fmt.Errorf("backfill provenance: %w", err)
	}
	return tx.Commit()
}
