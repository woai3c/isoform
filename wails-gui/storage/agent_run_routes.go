package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AgentRunRouteRow records the loop, not the model account, for every run.
// ModelCallRow remains the record of individual model calls within that loop.
type AgentRunRouteRow struct {
	RunID, SessionID, Backend, CandidateBackends, FallbackFrom string
	BackendReason, Model, Status                               string
	ActionsStarted                                             int
	UpdatedAt                                                  int64
}

func (s *Store) RecordAgentRunRoute(ctx context.Context, row AgentRunRouteRow) error {
	if row.RunID == "" || row.Backend == "" {
		return nil
	}
	if row.UpdatedAt == 0 {
		row.UpdatedAt = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_routes
		(run_id, session_id, backend, candidate_backends, fallback_from, backend_reason, model, status, actions_started, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
		 session_id = CASE WHEN excluded.session_id <> '' THEN excluded.session_id ELSE agent_run_routes.session_id END,
		 backend = excluded.backend, candidate_backends = excluded.candidate_backends,
		 fallback_from = excluded.fallback_from, backend_reason = excluded.backend_reason,
		 model = CASE WHEN excluded.model <> '' THEN excluded.model ELSE agent_run_routes.model END,
		 status = CASE WHEN excluded.status <> '' THEN excluded.status ELSE agent_run_routes.status END,
		 actions_started = MAX(agent_run_routes.actions_started, excluded.actions_started), updated_at = excluded.updated_at`,
		row.RunID, row.SessionID, row.Backend, row.CandidateBackends, row.FallbackFrom,
		row.BackendReason, row.Model, row.Status, row.ActionsStarted, row.UpdatedAt)
	return err
}

// InterruptedRunReason ends a route the app left "running" when it stopped.
const InterruptedRunReason = "Isoform closed before this run finished"

// EndInterruptedAgentRunRoutes gives every route still "running" from an
// earlier launch a final status. Nothing runs before startup opens the store,
// so each one was cut off. A run that had started actions has an unknown
// outcome and is blocked; the rest end in error.
func (s *Store) EndInterruptedAgentRunRoutes(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE agent_run_routes SET
		status = CASE WHEN actions_started > 0 THEN 'blocked' ELSE 'error' END,
		backend_reason = CASE WHEN backend_reason = '' THEN ? ELSE backend_reason || '; ' || ? END,
		updated_at = ?
		WHERE status = 'running'`, InterruptedRunReason, InterruptedRunReason, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) AgentRunRoute(ctx context.Context, runID string) (AgentRunRouteRow, error) {
	var row AgentRunRouteRow
	err := s.db.QueryRowContext(ctx, `SELECT run_id, session_id, backend, candidate_backends,
		fallback_from, backend_reason, model, status, actions_started, updated_at FROM agent_run_routes WHERE run_id = ?`, runID).
		Scan(&row.RunID, &row.SessionID, &row.Backend, &row.CandidateBackends, &row.FallbackFrom,
			&row.BackendReason, &row.Model, &row.Status, &row.ActionsStarted, &row.UpdatedAt)
	return row, err
}

// AgentRunRoutes loads only the runs shown in a conversation page. Unknown
// legacy IDs are omitted rather than assigned a guessed backend.
func (s *Store) AgentRunRoutes(ctx context.Context, runIDs []string) ([]AgentRunRouteRow, error) {
	if len(runIDs) > 200 {
		return nil, fmt.Errorf("run history lookup exceeds 200 IDs")
	}
	result := make([]AgentRunRouteRow, 0)
	seen := make(map[string]bool, len(runIDs))
	args := make([]any, 0, len(runIDs))
	marks := make([]string, 0, len(runIDs))
	for _, id := range runIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		args = append(args, id)
		marks = append(marks, "?")
	}
	if len(args) == 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, session_id, backend, candidate_backends,
		fallback_from, backend_reason, model, status, actions_started, updated_at
		FROM agent_run_routes WHERE run_id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row AgentRunRouteRow
		if err := rows.Scan(&row.RunID, &row.SessionID, &row.Backend, &row.CandidateBackends,
			&row.FallbackFrom, &row.BackendReason, &row.Model, &row.Status, &row.ActionsStarted, &row.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
