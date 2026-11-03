package storage

import "context"

// DatabaseFiles identifies the persistent database and its write-ahead log for
// conservative file revision checks. Call before acquiring a database fence.
func (s *Store) DatabaseFiles(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []string
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			return nil, err
		}
		if name == "main" && path != "" {
			files = append(files, path, path+"-wal")
		}
	}
	return files, rows.Err()
}
