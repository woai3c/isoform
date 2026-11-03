package main

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Earlier token extraction could retain chat prose as an error. Those error
// tokens have no source provenance, so remove them rather than guessing whether
// they came from a terminal. Keep valid identifiers without reading raw text.
func migrateActivityEvidenceIdentifierPolicy(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id,evidence_tokens,evidence_excluded FROM activity_timeline_groups`)
	if err != nil {
		return err
	}
	type tokenRow struct {
		id, raw  string
		excluded bool
	}
	var runs []tokenRow
	for rows.Next() {
		var item tokenRow
		if err := rows.Scan(&item.id, &item.raw, &item.excluded); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range runs {
		var tokens []ActivityEvidenceToken
		if err := json.Unmarshal([]byte(item.raw), &tokens); err != nil {
			return err
		}
		if err := persistTimelineRunEvidence(tx, ActivityTimelineRunRow{ID: item.id, EvidenceExcluded: item.excluded, EvidenceTokens: sanitizeActivityEvidenceTokens(tokens, false)}); err != nil {
			return err
		}
	}
	rows, err = tx.Query(`SELECT id,activity_id,status,evidence,target,reason,occurred_at FROM activity_outcomes`)
	if err != nil {
		return err
	}
	type outcomeRow struct {
		id, activityID, status, raw, target, reason string
		occurredAt                                  int64
	}
	var outcomes []outcomeRow
	for rows.Next() {
		var item outcomeRow
		if err := rows.Scan(&item.id, &item.activityID, &item.status, &item.raw, &item.target, &item.reason, &item.occurredAt); err != nil {
			rows.Close()
			return err
		}
		outcomes = append(outcomes, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	affected := map[string]bool{}
	for _, item := range outcomes {
		var evidence []ActivityOutcomeEvidence
		if err := json.Unmarshal([]byte(item.raw), &evidence); err != nil {
			return err
		}
		invalid := false
		for i := range evidence {
			before := len(evidence[i].Tokens)
			evidence[i].Tokens = sanitizeActivityEvidenceTokens(evidence[i].Tokens, false)
			invalid = invalid || len(evidence[i].Tokens) < before
		}
		common, err := commonComposerEvidence(tx, item.occurredAt)
		if err != nil {
			return err
		}
		outcome := ActivityOutcome{Status: item.status, Reason: item.reason, Target: item.target, Evidence: evidence}
		if !sanitizeActivityOutcomeEvidence(&outcome, common) {
			invalid = true
		}
		if invalid {
			// A receipt whose matching basis changed must be observed anew.
			if _, err := tx.Exec(`DELETE FROM activity_outcomes WHERE id=?`, item.id); err != nil {
				return err
			}
			affected[item.activityID] = true
			continue
		}
		encoded, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE activity_outcomes SET evidence=?,target=? WHERE id=?`, string(encoded), activityOutcomeSafeTarget(outcome), item.id); err != nil {
			return err
		}
	}
	for id := range affected {
		// Zero receipts otherwise looks like a legacy settled task. Removing
		// the sole invalid receipt must reopen that projection explicitly.
		if _, err := tx.Exec(`UPDATE activities SET status='active',paused_at_ms=0 WHERE id=?`, id); err != nil {
			return err
		}
		var endMs int64
		if err := tx.QueryRow(`SELECT end_ms FROM activities WHERE id=?`, id).Scan(&endMs); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		if err := refreshActivityOutcomeStatusTx(tx, id, endMs); err != nil {
			return err
		}
	}
	// Rebuild now: linked runs are not automatically composed again, and a
	// safe surviving identifier should keep its explanation after migration.
	return recomputeActivityLinkReasons(tx, "", time.Now().UnixMilli())
}

// Reapply the strengthened receipt/code policy for databases that already ran
// the first identifier cleanup. This also repairs explanations erased by it.
func migrateActivityEvidenceReceiptPolicy(tx *sql.Tx) error {
	return migrateActivityEvidenceIdentifierPolicy(tx)
}
