package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

const taskContextManifestStatsVersion = "task-context-manifest-stats-v1"

type taskContextManifestStatsRecord struct {
	TaskEpisodeID            string
	ContextSnapshotID        string
	ManifestSHA256           string
	ManifestEntryCount       int
	IncludedCount            int
	ExcludedCount            int
	FrozenSourceContextCount int
	CreatedAt                int64
}

func deriveTaskContextManifestStats(
	taskEpisodeID string,
	contextSnapshotID string,
	manifest []TaskContextManifestEntry,
	manifestJSON []byte,
	inferredStartAt int64,
	createdAt int64,
) taskContextManifestStatsRecord {
	result := taskContextManifestStatsRecord{
		TaskEpisodeID:      taskEpisodeID,
		ContextSnapshotID:  contextSnapshotID,
		ManifestSHA256:     taskEpisodePayloadHash(manifestJSON),
		ManifestEntryCount: len(manifest),
		CreatedAt:          createdAt,
	}
	for _, entry := range manifest {
		if entry.Included {
			result.IncludedCount++
		} else {
			result.ExcludedCount++
		}
		if entry.Included &&
			entry.SourceKind == taskContextSourceActivitySnapshotKind &&
			entry.ObservedAt > 0 &&
			entry.ObservedAt <= inferredStartAt {
			result.FrozenSourceContextCount++
		}
	}
	return result
}

func insertTaskContextManifestStats(
	tx *sql.Tx,
	stats taskContextManifestStatsRecord,
) error {
	if tx == nil {
		return fmt.Errorf("insert task context manifest stats: transaction is nil")
	}
	if _, err := tx.Exec(`
		INSERT INTO task_context_manifest_stats_v1 (
			task_episode_id, context_snapshot_id, version, manifest_sha256,
			manifest_entry_count, included_count, excluded_count,
			frozen_source_context_count, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stats.TaskEpisodeID,
		stats.ContextSnapshotID,
		taskContextManifestStatsVersion,
		stats.ManifestSHA256,
		stats.ManifestEntryCount,
		stats.IncludedCount,
		stats.ExcludedCount,
		stats.FrozenSourceContextCount,
		stats.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert task context manifest stats: %w", err)
	}
	return nil
}

func migrateTaskContextManifestStats(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("migrate task context manifest stats: transaction is nil")
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS task_context_manifest_stats_v1 (
			task_episode_id            TEXT PRIMARY KEY
				REFERENCES task_episodes(id) ON DELETE RESTRICT,
			context_snapshot_id        TEXT NOT NULL UNIQUE
				REFERENCES task_episode_context_snapshots(id) ON DELETE RESTRICT,
			version                    TEXT NOT NULL,
			manifest_sha256            TEXT NOT NULL,
			manifest_entry_count       INTEGER NOT NULL,
			included_count             INTEGER NOT NULL,
			excluded_count             INTEGER NOT NULL,
			frozen_source_context_count INTEGER NOT NULL,
			created_at                 INTEGER NOT NULL,
			CHECK (version = 'task-context-manifest-stats-v1'),
			CHECK (length(manifest_sha256) = 64),
			CHECK (manifest_entry_count >= 0),
			CHECK (included_count >= 0),
			CHECK (excluded_count >= 0),
			CHECK (included_count + excluded_count = manifest_entry_count),
			CHECK (frozen_source_context_count >= 0),
			CHECK (frozen_source_context_count <= included_count)
		);
		CREATE INDEX IF NOT EXISTS idx_task_context_manifest_stats_created_v1
			ON task_context_manifest_stats_v1(created_at DESC, task_episode_id);
	`); err != nil {
		return fmt.Errorf("create task context manifest stats: %w", err)
	}

	type pendingStats struct {
		taskEpisodeID     string
		contextSnapshotID string
		manifestJSON      string
		inferredStartAt   int64
		createdAt         int64
	}
	rows, err := tx.Query(`
		SELECT episode.id, context.id, context.manifest_json,
			episode.inferred_start_at, context.created_at
		FROM task_episode_context_snapshots context
		JOIN task_episodes episode ON episode.id = context.task_episode_id
		LEFT JOIN task_context_manifest_stats_v1 stats
			ON stats.task_episode_id = episode.id
		WHERE stats.task_episode_id IS NULL
		ORDER BY episode.id ASC`)
	if err != nil {
		return fmt.Errorf("list task context manifests for stats: %w", err)
	}
	pending := make([]pendingStats, 0)
	for rows.Next() {
		var item pendingStats
		if err := rows.Scan(
			&item.taskEpisodeID,
			&item.contextSnapshotID,
			&item.manifestJSON,
			&item.inferredStartAt,
			&item.createdAt,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan task context manifest for stats: %w", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read task context manifests for stats: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close task context manifest stats rows: %w", err)
	}

	for _, item := range pending {
		var manifest []TaskContextManifestEntry
		if err := json.Unmarshal([]byte(item.manifestJSON), &manifest); err != nil {
			return fmt.Errorf(
				"decode task episode %q context manifest for stats: %w",
				item.taskEpisodeID,
				err,
			)
		}
		stats := deriveTaskContextManifestStats(
			item.taskEpisodeID,
			item.contextSnapshotID,
			manifest,
			[]byte(item.manifestJSON),
			item.inferredStartAt,
			item.createdAt,
		)
		if err := insertTaskContextManifestStats(tx, stats); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`
		CREATE TRIGGER IF NOT EXISTS task_context_manifest_stats_v1_no_update
		BEFORE UPDATE ON task_context_manifest_stats_v1
		BEGIN
			SELECT RAISE(ABORT, 'task context manifest stats are immutable');
		END;
		CREATE TRIGGER IF NOT EXISTS task_context_manifest_stats_v1_no_delete
		BEFORE DELETE ON task_context_manifest_stats_v1
		BEGIN
			SELECT RAISE(ABORT, 'task context manifest stats are immutable');
		END;
	`); err != nil {
		return fmt.Errorf("protect task context manifest stats: %w", err)
	}
	return nil
}
