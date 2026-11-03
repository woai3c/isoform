package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	activityOutcomeReported  = "reported"
	activityOutcomeFixed     = "fixed"
	activityOutcomeDone      = "done"
	activityOutcomeAbandoned = "abandoned"
)

// ActivityOutcome records observed results, never authority to execute work.
// Reason is a stable localization key; evidence stores identifiers, not messages.
type ActivityOutcome struct {
	ID         string                    `json:"id"`
	ActivityID string                    `json:"activityId"`
	RunID      string                    `json:"runId"`
	Status     string                    `json:"status"`
	Reason     string                    `json:"reason"`
	OccurredAt int64                     `json:"occurredAt"`
	Target     string                    `json:"target"`
	Evidence   []ActivityOutcomeEvidence `json:"evidence"`
}

type ActivityOutcomeEvidence struct {
	RunID      string                  `json:"runId,omitempty"`
	SourceKind string                  `json:"sourceKind"`
	SourceID   string                  `json:"sourceId"`
	ObservedAt int64                   `json:"observedAt"`
	Tokens     []ActivityEvidenceToken `json:"tokens"`
}

// Outcome evidence uses identifiers only, even when a direct caller or an
// older database supplies error prose that once passed the token extractor.
func sanitizeActivityOutcomeEvidence(item *ActivityOutcome, commonMaps ...map[string]bool) bool {
	var common map[string]bool
	if len(commonMaps) > 0 {
		common = commonMaps[0]
	}
	hasStrongToken := false
	hasStrongPath := false
	for i := range item.Evidence {
		item.Evidence[i].Tokens = sanitizeActivityEvidenceTokens(item.Evidence[i].Tokens, false)
		for _, token := range item.Evidence[i].Tokens {
			_, strong := activityEvidenceWeight(token, common)
			hasStrongToken = hasStrongToken || strong
			hasStrongPath = hasStrongPath || (strong && token.Kind == "path")
		}
	}
	if item.Status == activityOutcomeFixed {
		for _, evidence := range item.Evidence {
			if evidence.SourceKind != "activity_snapshot" {
				return false
			}
		}
		return hasStrongPath
	}
	return item.Status == activityOutcomeAbandoned || hasStrongToken
}

func activityOutcomeSafeTarget(item ActivityOutcome) string {
	if item.Status == activityOutcomeReported || item.Reason == "user_completed" {
		for _, allowed := range []string{"Claude", "ChatGPT", "Codex", "Slack", "Discord", "Teams", "Telegram", "WhatsApp", "Messages", "Signal", "WeChat", "Gmail", "Mail", "Isoform", "Gemini", "Copilot", "Grok", "Perplexity"} {
			if strings.EqualFold(strings.TrimSpace(item.Target), allowed) {
				return allowed
			}
		}
		return ""
	}
	for _, evidence := range item.Evidence {
		for _, token := range evidence.Tokens {
			if token.Kind == "path" || token.Kind == "repo" || token.Kind == "pr" {
				return token.Value
			}
		}
	}
	return ""
}

// Historical receipts survive unlinking. Only receipts with live, permitted
// run membership contribute to the current activity's status.
// Batch reads supply their shared common-token map; individual lifecycle
// reads retain the receipt-time policy and memoize repeated timestamps.
func activityOutcomes(q sqlQueryer, activityID string, commonMaps ...map[string]bool) ([]ActivityOutcome, error) {
	rows, err := q.Query(`SELECT o.id, o.activity_id, o.run_id, o.status, o.reason,
		o.occurred_at, o.target, o.evidence, COALESCE(g.app_key,''), COALESCE(g.app_name,'')
		FROM activity_outcomes o
		LEFT JOIN activity_run_links l ON l.run_id = o.run_id AND l.activity_id = o.activity_id
		LEFT JOIN activity_timeline_groups g ON g.id = l.run_id
		WHERE o.activity_id = ? AND (o.run_id = '' OR (l.run_id IS NOT NULL AND g.evidence_excluded = 0
			AND NOT EXISTS (SELECT 1 FROM json_each(o.evidence) e WHERE COALESCE(json_extract(e.value,'$.runId'),'') != '' AND NOT EXISTS (SELECT 1 FROM activity_run_links el JOIN activity_timeline_groups eg ON eg.id=el.run_id WHERE el.run_id=json_extract(e.value,'$.runId') AND el.activity_id=o.activity_id AND eg.evidence_excluded=0 AND NOT EXISTS (SELECT 1 FROM json_each(eg.session_ids) es JOIN activity_sessions ss ON ss.id=es.value WHERE ss.triage_status='protected')))
			AND NOT EXISTS (SELECT 1 FROM json_each(g.session_ids) j JOIN activity_sessions s ON s.id = j.value WHERE s.triage_status = 'protected')))
		ORDER BY o.occurred_at, CASE o.status WHEN 'done' THEN 4 WHEN 'fixed' THEN 3 WHEN 'abandoned' THEN 2 ELSE 1 END, o.id`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivityOutcome{}
	for rows.Next() {
		var item ActivityOutcome
		var evidence, appKey, appName string
		if err := rows.Scan(&item.ID, &item.ActivityID, &item.RunID, &item.Status, &item.Reason, &item.OccurredAt, &item.Target, &evidence, &appKey, &appName); err != nil {
			return nil, err
		}
		if item.RunID != "" && activityEvidenceAppExcluded(appKey, appName) {
			continue
		}
		if err := json.Unmarshal([]byte(evidence), &item.Evidence); err != nil {
			return nil, fmt.Errorf("decode activity outcome evidence: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	filtered := make([]ActivityOutcome, 0, len(out))
	commonAt := map[int64]map[string]bool{}
	for _, item := range out {
		var common map[string]bool
		if len(commonMaps) > 0 {
			common = commonMaps[0]
		} else if cached, ok := commonAt[item.OccurredAt]; ok {
			common = cached
		} else {
			var err error
			common, err = commonComposerEvidence(q, item.OccurredAt)
			if err != nil {
				return nil, err
			}
			commonAt[item.OccurredAt] = common
		}
		if !sanitizeActivityOutcomeEvidence(&item, common) {
			continue
		}
		item.Target = activityOutcomeSafeTarget(item)
		allowed := true
		for _, evidence := range item.Evidence {
			if evidence.RunID == "" {
				continue
			}
			var key, name string
			if err := q.QueryRow(`SELECT app_key,app_name FROM activity_timeline_groups WHERE id=?`, evidence.RunID).Scan(&key, &name); err != nil {
				if err == sql.ErrNoRows {
					allowed = false
					break
				}
				return nil, err
			}
			if activityEvidenceAppExcluded(key, name) {
				allowed = false
				break
			}
		}
		if allowed {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *ActivityStore) GetActivityOutcomes(activityID string, commonMaps ...map[string]bool) ([]ActivityOutcome, error) {
	return activityOutcomes(s.reader(), activityID, commonMaps...)
}

func (s *ActivityStore) GetLatestActivityOutcome(activityID string, commonMaps ...map[string]bool) (*ActivityOutcome, error) {
	items, err := s.GetActivityOutcomes(activityID, commonMaps...)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return effectiveActivityOutcome(items), nil
}

// Delivery order is not evidence order: OCR and lifecycle passes can finish
// late. Retain every receipt; terminal observations dominate later handoffs.
func effectiveActivityOutcome(items []ActivityOutcome) *ActivityOutcome {
	var selected *ActivityOutcome
	rank := func(status string) int {
		switch status {
		case activityOutcomeDone:
			return 4
		case activityOutcomeFixed:
			return 3
		case activityOutcomeAbandoned:
			return 2
		default:
			return 1
		}
	}
	for i := range items {
		if selected == nil || rank(items[i].Status) >= rank(selected.Status) {
			selected = &items[i]
		}
	}
	return selected
}

func appendActivityOutcomeTx(tx *sql.Tx, item ActivityOutcome, nowMs int64) error {
	if item.ActivityID == "" || item.OccurredAt <= 0 || item.Reason == "" || len(item.Evidence) == 0 {
		return fmt.Errorf("activity outcome requires identity, reason and evidence")
	}
	item.Evidence = append([]ActivityOutcomeEvidence(nil), item.Evidence...)
	common, err := commonComposerEvidence(tx, item.OccurredAt)
	if err != nil {
		return err
	}
	if !sanitizeActivityOutcomeEvidence(&item, common) {
		return fmt.Errorf("activity outcome requires observed task identifiers")
	}
	item.Target = activityOutcomeSafeTarget(item)
	encoded, err := json.Marshal(item.Evidence)
	if err != nil {
		return err
	}
	var duplicate int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM activity_outcomes WHERE activity_id=? AND run_id=? AND status=? AND reason=? AND occurred_at=? AND evidence=?`, item.ActivityID, item.RunID, item.Status, item.Reason, item.OccurredAt, string(encoded)).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return nil
	}
	item.ID = deterministicTaskEpisodeID("activity-outcome-v1", strings.Join([]string{item.ActivityID, item.RunID, item.Status, item.Reason, fmt.Sprint(item.OccurredAt), string(encoded)}, "\x00"))
	_, err = tx.Exec(`INSERT OR IGNORE INTO activity_outcomes (id,activity_id,run_id,status,reason,occurred_at,target,evidence,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, item.ID, item.ActivityID, item.RunID, item.Status, item.Reason, item.OccurredAt, item.Target, string(encoded), nowMs)
	return err
}

// refreshActivityOutcomeStatusTx also restores the open projection after the
// user rejects the run that supplied a terminal outcome. Reported stays open.
func refreshActivityOutcomeStatusTx(tx *sql.Tx, activityID string, nowMs int64) error {
	items, err := activityOutcomes(tx, activityID)
	if err != nil {
		return err
	}
	var status string
	var endMs, pausedAt int64
	if err := tx.QueryRow(`SELECT status,end_ms,paused_at_ms FROM activities WHERE id=?`, activityID).Scan(&status, &endMs, &pausedAt); err != nil {
		return err
	}
	if latest := effectiveActivityOutcome(items); latest != nil {
		switch latest.Status {
		case activityOutcomeDone, activityOutcomeFixed:
			status, pausedAt = activityStatusSettled, 0
		case activityOutcomeAbandoned:
			status = activityStatusAbandoned
		default:
			if status == activityStatusSettled || status == activityStatusAbandoned {
				status = activityStatusActive
			}
		}
	} else if status == activityStatusSettled || status == activityStatusAbandoned {
		// Legacy settled buckets with no outcome history keep their prior state.
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM activity_outcomes WHERE activity_id=?`, activityID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			status, pausedAt = activityStatusActive, 0
		}
	}
	if status == activityStatusActive || status == activityStatusIdle {
		status, pausedAt = activityStatusActive, 0
		if nowMs-endMs >= activityPauseAfterMs {
			status, pausedAt = activityStatusPaused, endMs+activityPauseAfterMs
		} else if nowMs-endMs > activityIdleAfterMs {
			status = activityStatusIdle
		}
	}
	_, err = tx.Exec(`UPDATE activities SET status=?, paused_at_ms=?, updated_at=? WHERE id=?`, status, pausedAt, nowMs, activityID)
	return err
}
