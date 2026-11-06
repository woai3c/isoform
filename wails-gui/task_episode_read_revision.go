package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

// The rows the all-time repeated-onset set reads change through triggers that
// bump one counter in the same transaction. An insert, an edit or a delete (a
// withdrawal included) therefore moves the key, whichever code made the change.
var taskEpisodeReadTables = []string{"task_episodes", "task_episode_hypotheses", "task_episode_evidence"}

func migrateTaskEpisodeReadRevision(tx *sql.Tx) error {
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS task_episode_read_revision (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			revision INTEGER NOT NULL DEFAULT 0
		);
		INSERT OR IGNORE INTO task_episode_read_revision(id, revision) VALUES (1, 0);`); err != nil {
		return err
	}
	for _, table := range taskEpisodeReadTables {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS task_episode_read_revision_%s_%s
				AFTER %s ON %s BEGIN
				UPDATE task_episode_read_revision SET revision = revision + 1 WHERE id = 1;
				END`, table, strings.ToLower(operation), operation, table)
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
	}
	return nil
}

// taskEpisodeReadRevision reads the counter the triggers maintain.
func (s *ActivityStore) taskEpisodeReadRevision(ctx context.Context) (int64, error) {
	var revision int64
	// The request's context bounds the wait for a read connection: a cancelled lookup
	// returns at once instead of queueing behind a busy pool.
	err := s.reader().QueryRowContext(ctx, `SELECT revision FROM task_episode_read_revision WHERE id = 1`).Scan(&revision)
	return revision, err
}

// taskEpisodeRepeatedCache keeps the last all-time set and the revision it was
// read at. A set read at an older revision is never served.
type taskEpisodeRepeatedCache struct {
	mu       sync.Mutex
	valid    bool
	revision int64
	set      map[string]struct{}
}

func (c *taskEpisodeRepeatedCache) get(revision int64) (map[string]struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || c.revision != revision {
		return nil, false
	}
	return c.set, true
}

func (c *taskEpisodeRepeatedCache) put(revision int64, set map[string]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid, c.revision, c.set = true, revision, set
}
