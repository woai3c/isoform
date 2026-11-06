package storage

import "context"

// SavedProactiveInputs returns only main-chat proactive payloads for the
// one-time title repair; other conversation content is not loaded.
func (s *Store) SavedProactiveInputs(ctx context.Context) ([]StreamItemRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.tool_input FROM stream_items i
		JOIN sessions s ON s.local_id = i.session_local_id
		WHERE s.kind = 'main' AND i.tool_name = 'main_chat_proactive' ORDER BY i.sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StreamItemRow
	for rows.Next() {
		var row StreamItemRow
		if err := rows.Scan(&row.ID, &row.ToolInput); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ReplaceProactiveInputIfUnchanged never overwrites an action or edit that
// changed the card while its source was being checked.
func (s *Store) ReplaceProactiveInputIfUnchanged(ctx context.Context, id, before, after string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE stream_items SET tool_input = ?
		WHERE id = ? AND tool_name = 'main_chat_proactive' AND tool_input = ?
		AND EXISTS (SELECT 1 FROM sessions s WHERE s.local_id = stream_items.session_local_id AND s.kind = 'main')`, after, id, before)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
