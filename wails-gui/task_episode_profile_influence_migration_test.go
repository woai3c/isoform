package main

import "testing"

func TestMigrateTaskEpisodeProfileInfluenceUpgradesHistoricalSchema(
	t *testing.T,
) {
	db := newTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE task_episode_hypotheses (
			id TEXT PRIMARY KEY,
			task_episode_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			task_hypothesis TEXT NOT NULL,
			desired_result_hypothesis TEXT NOT NULL,
			task_confidence REAL NOT NULL,
			boundary_confidence REAL NOT NULL,
			desired_result_confidence REAL NOT NULL,
			alternatives_json TEXT NOT NULL,
			evidence_ids_json TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		INSERT INTO task_episode_hypotheses (
			id, task_episode_id, revision, task_hypothesis,
			desired_result_hypothesis, task_confidence,
			boundary_confidence, desired_result_confidence,
			alternatives_json, evidence_ids_json, created_at
		) VALUES (
			'legacy', 'episode', 1, 'Review a change',
			'A decision exists', 0.8, 0.7, 0.7, '[]', '["evidence"]', 10
		)
	`); err != nil {
		t.Fatal(err)
	}

	for pass := 0; pass < 2; pass++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateTaskEpisodeProfileInfluence(tx); err != nil {
			tx.Rollback()
			t.Fatalf("migration pass %d: %v", pass+1, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	var payload string
	if err := db.QueryRow(`
		SELECT profile_influence_json
		FROM task_episode_hypotheses
		WHERE id = 'legacy'
	`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != "{}" {
		t.Fatalf("legacy default=%q, want {}", payload)
	}
}
