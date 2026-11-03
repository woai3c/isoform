package storage

// proactiveChildIdentitiesSQL extracts only valid string identities from
// digest children. JSON is parsed at write/backfill time, never while
// checking whether a new moment is already present in an old digest.
const proactiveChildIdentitiesSQL = `
	INSERT INTO stream_item_proactive_children (stream_item_id, session_local_id, moment_id, sort_order)
	SELECT DISTINCT source.id, source.session_local_id,
		json_extract(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId'), source.sort_order
	FROM stream_items AS source,
		json_each(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.items') AS child
	WHERE source.tool_name = 'main_chat_proactive'
		AND json_type(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.items') = 'array'
		AND json_type(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId') = 'text'
		AND json_extract(CASE WHEN child.type = 'object' THEN child.value ELSE '{}' END, '$.momentId') != ''`

const statusEntryIdentitiesSQL = `
	INSERT INTO stream_item_status_entries (stream_item_id, session_local_id, entry_id, sort_order)
	SELECT DISTINCT source.id, source.session_local_id,
		json_extract(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id'), source.sort_order
	FROM stream_items AS source,
		json_each(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.entries') AS entry
	WHERE source.tool_name = 'main_chat_status'
		AND json_type(CASE WHEN json_valid(source.tool_input) THEN source.tool_input ELSE '{}' END, '$.entries') = 'array'
		AND json_type(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id') = 'text'
		AND json_extract(CASE WHEN entry.type = 'object' THEN entry.value ELSE '{}' END, '$.id') != ''`

// migrateMainChatItemIdentities builds digest-child and status-entry indexes
// and keeps them transactionally consistent with every stream writer,
// including bulk replacement, rewind and cascading session deletion.
func (s *Store) migrateMainChatItemIdentities() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Missing tables or maintenance triggers mean writes may have happened
	// without indexed identities. Rebuild once in that case, not on every
	// healthy reopen; ordinary startup must not parse lifetime chat JSON.
	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE
		(type = 'table' AND name IN ('stream_item_proactive_children', 'stream_item_status_entries')) OR
		(type = 'trigger' AND name IN ('stream_items_proactive_children_insert', 'stream_items_proactive_children_update', 'stream_items_status_entries_insert', 'stream_items_status_entries_update'))`).Scan(&existing); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS stream_item_proactive_children (
			stream_item_id   TEXT NOT NULL REFERENCES stream_items(id) ON DELETE CASCADE,
			session_local_id TEXT NOT NULL,
			moment_id        TEXT NOT NULL,
			sort_order       INTEGER NOT NULL,
			PRIMARY KEY (stream_item_id, moment_id)
		);
		CREATE INDEX IF NOT EXISTS idx_proactive_children_identity
			ON stream_item_proactive_children(session_local_id, moment_id, sort_order);
		CREATE TABLE IF NOT EXISTS stream_item_status_entries (
			stream_item_id   TEXT NOT NULL REFERENCES stream_items(id) ON DELETE CASCADE,
			session_local_id TEXT NOT NULL,
			entry_id        TEXT NOT NULL,
			sort_order       INTEGER NOT NULL,
			PRIMARY KEY (stream_item_id, entry_id)
		);
		CREATE INDEX IF NOT EXISTS idx_status_entries_identity
			ON stream_item_status_entries(session_local_id, entry_id, sort_order);
	`); err != nil {
		return err
	}
	if existing != 6 {
		if _, err := tx.Exec(`DELETE FROM stream_item_proactive_children; DELETE FROM stream_item_status_entries;`); err != nil {
			return err
		}
		for _, insert := range []string{proactiveChildIdentitiesSQL, statusEntryIdentitiesSQL} {
			if _, err := tx.Exec(insert); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`
		CREATE TRIGGER IF NOT EXISTS stream_items_proactive_children_insert
		AFTER INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_proactive'
		BEGIN ` + proactiveChildIdentitiesSQL + ` AND source.id = NEW.id; END;
		CREATE TRIGGER IF NOT EXISTS stream_items_proactive_children_update
		AFTER UPDATE OF tool_input, tool_name, session_local_id, sort_order ON stream_items
		WHEN OLD.tool_name = 'main_chat_proactive' OR NEW.tool_name = 'main_chat_proactive'
		BEGIN
			DELETE FROM stream_item_proactive_children WHERE stream_item_id = OLD.id;
			` + proactiveChildIdentitiesSQL + ` AND source.id = NEW.id;
		END;
		CREATE TRIGGER IF NOT EXISTS stream_items_status_entries_insert
		AFTER INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_status'
		BEGIN ` + statusEntryIdentitiesSQL + ` AND source.id = NEW.id; END;
		CREATE TRIGGER IF NOT EXISTS stream_items_status_entries_update
		AFTER UPDATE OF tool_input, tool_name, session_local_id, sort_order ON stream_items
		WHEN OLD.tool_name = 'main_chat_status' OR NEW.tool_name = 'main_chat_status'
		BEGIN
			DELETE FROM stream_item_status_entries WHERE stream_item_id = OLD.id;
			` + statusEntryIdentitiesSQL + ` AND source.id = NEW.id;
		END;
	`); err != nil {
		return err
	}
	return tx.Commit()
}
