package storage

import (
	"regexp"
	"strings"
)

// Match only a trailing captured timestamp, leaving names with digits in the
// middle intact. Re-running the migration is harmless.
var senderTimestampSuffix = regexp.MustCompile(`(?i)[\s\x{00a0}\x{202f}]+(?:(?:\d{1,4}[/.-]\d{1,2}[/.-]\d{1,4})(?:,?[\s\x{00a0}\x{202f}]+\d{1,2}:\d{2}(?:[\s\x{00a0}\x{202f}]*[ap]m)?)?|(?:(?:today|yesterday|今天|昨天|前天)[\s\x{00a0}\x{202f}]*(?:at[\s\x{00a0}\x{202f}]+)?)?\d{1,2}:\d{2}(?:[\s\x{00a0}\x{202f}]*[ap]m)?)[\s\x{00a0}\x{202f}]*$`)

func (s *Store) migrateSenderTimestampNames() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id, display_name FROM principals`)
	if err != nil {
		return err
	}
	type rename struct{ id, name string }
	var changes []rename
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		clean := strings.TrimSpace(senderTimestampSuffix.ReplaceAllString(name, ""))
		if clean != "" && clean != name {
			changes = append(changes, rename{id, clean})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, change := range changes {
		if _, err := tx.Exec(`UPDATE principals SET display_name = ? WHERE id = ?`, change.name, change.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
