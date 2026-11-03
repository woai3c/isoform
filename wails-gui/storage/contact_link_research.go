package storage

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Reading an existing dossier never starts research. The source row would
// otherwise cascade-delete with MergePrincipals and make unlink lossy.
func snapshotContactResearchTx(ctx context.Context, tx *sql.Tx, id string) (*PrincipalResearchRow, error) {
	var r PrincipalResearchRow
	var sources string
	err := tx.QueryRowContext(ctx, `SELECT principal_id,status,summary,markdown,sources,model,error,researched_at,created_at,updated_at FROM principal_research WHERE principal_id=?`, id).Scan(&r.PrincipalID, &r.Status, &r.Summary, &r.Markdown, &sources, &r.Model, &r.Error, &r.ResearchedAt, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Sources = decodeResearchSources(sources)
	return &r, nil
}

func restoreContactResearchTx(ctx context.Context, tx *sql.Tx, r *PrincipalResearchRow) error {
	if r == nil {
		return nil
	}
	restored := *r
	// An old running status cannot revive an asynchronous job. Preserve its
	// last completed dossier, or return to idle without scheduling work.
	if restored.Status == PrincipalResearchStatusResearching {
		restored.Status = PrincipalResearchStatusIdle
		if restored.Markdown != "" {
			restored.Status = PrincipalResearchStatusReady
		}
		restored.Error = ""
	}
	sources, err := json.Marshal(restored.Sources)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO principal_research(principal_id,status,summary,markdown,sources,model,error,researched_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, restored.PrincipalID, restored.Status, restored.Summary, restored.Markdown, string(sources), restored.Model, restored.Error, restored.ResearchedAt, restored.CreatedAt, restored.UpdatedAt)
	return err
}
