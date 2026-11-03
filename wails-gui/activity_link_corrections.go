package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// ActivityLinkReason retains the observed basis of a run's assignment. It is
// not a statement of user intent; a correction takes precedence over it.
type ActivityLinkReason struct {
	RunID      string `json:"runId"`
	ReasonKind string `json:"reasonKind"`
	Token      string `json:"token,omitempty"`
}

func (s *ActivityStore) activityRunRejected(activityID, runID string) (bool, error) {
	var count int
	err := s.reader().QueryRow(`SELECT COUNT(*) FROM activity_run_corrections WHERE activity_id = ? AND run_id = ?`, activityID, runID).Scan(&count)
	return count > 0, err
}

func (s *ActivityStore) activityRunLinkReason(activityID, runID string, commonMaps ...map[string]bool) (*ActivityLinkReason, error) {
	var reason ActivityLinkReason
	var kind string
	var observedAt int64
	reason.RunID = runID
	err := s.reader().QueryRow(`SELECT l.link_reason, l.link_token_kind, l.link_token_value, g.end_ms
        FROM activity_run_links l JOIN activity_timeline_groups g ON g.id=l.run_id
        WHERE l.activity_id = ? AND l.run_id = ?`, activityID, runID).Scan(&reason.ReasonKind, &kind, &reason.Token, &observedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if reason.ReasonKind == "" {
		reason.ReasonKind = "related_context"
	}
	if kind != "" {
		var common map[string]bool
		if len(commonMaps) > 0 {
			common = commonMaps[0]
		} else {
			common, err = commonComposerEvidence(s.db, observedAt)
			if err != nil {
				return nil, err
			}
		}
		tokens := sanitizeActivityEvidenceTokens([]ActivityEvidenceToken{{Kind: kind, Value: reason.Token}}, true)
		strong := false
		if len(tokens) == 1 {
			_, strong = activityEvidenceWeight(tokens[0], common)
			reason.Token = tokens[0].Value
		}
		if !strong {
			reason.ReasonKind, reason.Token = "related_context", ""
		}
	}
	return &reason, nil
}

func activityLinkMembers(q sqlQueryer, activityID string) ([]ActivityTimelineRunRow, error) {
	rows, err := q.Query(`SELECT `+timelineRunColumns+` FROM activity_timeline_groups
        WHERE id IN (SELECT run_id FROM activity_run_links WHERE activity_id=?)
        ORDER BY start_ms,end_ms,id`, activityID)
	if err != nil {
		return nil, err
	}
	members, err := scanTimelineRunRows(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	eligible := make([]ActivityTimelineRunRow, 0, len(members))
	for _, member := range members {
		allowed, err := timelineRunEvidenceAllowed(q, member)
		if err != nil {
			return nil, err
		}
		if allowed {
			eligible = append(eligible, member)
		}
	}
	return eligible, nil
}

// Only persisted, eligible observations can supply an explanation. Common
// identifiers are weak here just as they are for the composer's admission.
func activityLinkEvidenceReason(run ActivityTimelineRunRow, previous []ActivityTimelineRunRow, common map[string]bool) (string, string, string) {
	if len(previous) == 0 {
		return "task_started", "", ""
	}
	for _, wanted := range []string{"path", "pr", "repo", "error", "figure"} {
		for _, token := range run.EvidenceTokens {
			if token.Kind != wanted {
				continue
			}
			if _, strong := activityEvidenceWeight(token, common); !strong {
				continue
			}
			for _, member := range previous {
				for _, older := range member.EvidenceTokens {
					if _, strong := activityEvidenceWeight(older, common); !strong {
						continue
					}
					if older.LastSeenMs > run.EndMs || run.EndMs-older.LastSeenMs > activityStrongLinkMs || !activityEvidenceMatches(token, older) {
						continue
					}
					reasons := map[string]string{"path": "shared_path", "pr": "shared_pr", "repo": "shared_repo", "error": "same_error", "figure": "same_figure"}
					return reasons[token.Kind], token.Kind, token.Value
				}
			}
		}
	}
	return "related_context", "", ""
}

func writeActivityLinkReason(q sqlQueryer, activityID string, run ActivityTimelineRunRow, previous []ActivityTimelineRunRow, common map[string]bool) error {
	reason, kind, value := activityLinkEvidenceReason(run, previous, common)
	_, err := q.Exec(`UPDATE activity_run_links SET link_reason=?,link_token_kind=?,link_token_value=?
        WHERE activity_id=? AND run_id=?`, reason, kind, value, activityID, run.ID)
	return err
}

func (s *ActivityStore) recordActivityLinkReason(activityID string, run ActivityTimelineRunRow) error {
	members, err := activityLinkMembers(s.db, activityID)
	if err != nil {
		return err
	}
	common, err := commonComposerEvidence(s.db, run.EndMs)
	if err != nil {
		return err
	}
	for i, member := range members {
		if member.ID == run.ID {
			return writeActivityLinkReason(s.db, activityID, member, members[:i], common)
		}
	}
	return nil
}

// Rebuild surviving explanations without capture reads. An empty activityID
// applies the same policy to every linked task, for data-policy migrations.
func recomputeActivityLinkReasons(q sqlQueryer, activityID string, nowMs int64) error {
	ids := []string{activityID}
	if activityID == "" {
		rows, err := q.Query(`SELECT DISTINCT activity_id FROM activity_run_links WHERE activity_id!='' ORDER BY activity_id`)
		if err != nil {
			return err
		}
		ids = nil
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for _, id := range ids {
		members, err := activityLinkMembers(q, id)
		if err != nil {
			return err
		}
		// Clear disallowed members too; no stale token explanation survives.
		if _, err := q.Exec(`UPDATE activity_run_links SET link_reason='related_context',link_token_kind='',link_token_value='' WHERE activity_id=?`, id); err != nil {
			return err
		}
		if len(members) == 0 {
			continue
		}
		referenceMs := members[len(members)-1].EndMs
		if nowMs > 0 {
			referenceMs = min(referenceMs, nowMs)
		}
		common, err := commonComposerEvidence(q, referenceMs)
		if err != nil {
			return err
		}
		for i, member := range members {
			if err := writeActivityLinkReason(q, id, member, members[:i], common); err != nil {
				return err
			}
		}
	}
	return nil
}

// UnlinkActivityRun records an explicit correction and detaches only this run.
// The run and outcome history survive. A future summary revision may route it
// elsewhere, but cannot undo this activity/run exclusion.
func (s *ActivityStore) UnlinkActivityRun(activityID, runID string, nowMs int64) error {
	if strings.TrimSpace(activityID) == "" || strings.TrimSpace(runID) == "" {
		return fmt.Errorf("activity and run are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var linkedID string
	err = tx.QueryRow(`SELECT activity_id FROM activity_run_links WHERE run_id = ?`, runID).Scan(&linkedID)
	if err != nil || linkedID != activityID {
		var corrected int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM activity_run_corrections WHERE activity_id = ? AND run_id = ?`, activityID, runID).Scan(&corrected); err != nil {
			return err
		}
		if corrected > 0 {
			return nil
		}
		return fmt.Errorf("run is not linked to activity")
	}
	var rawIDs string
	if err := tx.QueryRow(`SELECT run_ids FROM activities WHERE id = ?`, activityID).Scan(&rawIDs); err != nil {
		return err
	}
	correctionID := deterministicTaskEpisodeID("activity-link-correction", activityID+"\x00"+runID)
	if _, err := tx.Exec(`INSERT INTO activity_run_corrections (id, activity_id, run_id, reason, created_at) VALUES (?, ?, ?, 'not_related', ?) ON CONFLICT(activity_id, run_id) DO NOTHING`, correctionID, activityID, runID, nowMs); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE activity_run_links SET activity_id = '', link_reason = '', link_token_kind = '', link_token_value = '', composed_event_count = COALESCE((SELECT summarized_event_count FROM activity_timeline_groups WHERE id = ?), composed_event_count) WHERE run_id = ?`, runID, runID); err != nil {
		return err
	}
	ids := []string{}
	byApp := map[string]int64{}
	var start, end int64
	var firstSummary, lastSummary ActivityTimelineGroupSummary
	var summaryStart, summaryEnd int64
	var fallbackTitle string
	var fallbackStart int64
	entities := []Entity{}
	for _, id := range composeParseStrings(rawIDs) {
		if id == runID {
			continue
		}
		var member ActivityTimelineRunRow
		var sessions string
		member.ID = id
		if err := tx.QueryRow(`SELECT app_key, app_name, start_ms, end_ms, active_ms, session_ids, evidence_excluded FROM activity_timeline_groups WHERE id = ?`, id).Scan(&member.AppKey, &member.AppName, &member.StartMs, &member.EndMs, &member.ActiveMs, &sessions, &member.EvidenceExcluded); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		if err := json.Unmarshal([]byte(sessions), &member.SessionIDs); err != nil {
			return err
		}
		ids = append(ids, id)
		if start == 0 || member.StartMs < start {
			start = member.StartMs
		}
		end = max(end, member.EndMs)
		allowed, err := timelineRunEvidenceAllowed(tx, member)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		byApp[member.AppName] += member.ActiveMs
		if fallbackStart == 0 || member.StartMs < fallbackStart {
			fallbackTitle, fallbackStart = member.AppName, member.StartMs
		}
		var summary ActivityTimelineGroupSummary
		var rawEntities string
		err = tx.QueryRow(`SELECT title, description, activity_type, entities FROM activity_timeline_group_summaries WHERE group_id = ?`, id).Scan(&summary.Title, &summary.Description, &summary.ActivityType, &rawEntities)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if err := json.Unmarshal([]byte(rawEntities), &summary.Entities); err != nil {
				return err
			}
			entities = composeUnionEntities(entities, typedEntitiesForRunSummary(member, summary))
			if summaryStart == 0 || member.StartMs < summaryStart {
				firstSummary, summaryStart = summary, member.StartMs
			}
			if member.EndMs >= summaryEnd {
				lastSummary, summaryEnd = summary, member.EndMs
			}
		}
	}
	if len(ids) == 0 {
		// A relation correction is not a request to delete the task. Retain its
		// identity and time span so the card stays findable, but remove all
		// run-derived detail and keep it out of live work and join candidates.
		_, err = tx.Exec(`UPDATE activities SET run_ids='[]',active_ms_by_app='{}',description='',latest_change='',entities='[]',status=?,paused_at_ms=?,updated_at=? WHERE id=?`, activityStatusPaused, nowMs, nowMs, activityID)
	} else {
		// Drop the composed prose and identity hints that may describe the
		// rejected run. Rebuild only from summaries of the remaining evidence.
		description := firstSummary.Description
		if len(ids) > 1 {
			description = composeSegmentDescription(firstSummary, lastSummary)
		}
		category := composeFirstNonEmpty(activityCategoryForRunSummary(lastSummary), "sustain")
		_, err = tx.Exec(`UPDATE activities SET run_ids = ?, active_ms_by_app = ?, start_ms = ?, end_ms = ?, updated_at = ?, paused_at_ms = 0,
			title = ?, description = ?, latest_change = ?, entities = ?, category = ?, category_color = ? WHERE id = ?`, composeJSON(ids), composeJSON(byApp), start, end, nowMs,
			composeFirstNonEmpty(firstSummary.Title, fallbackTitle), description, lastSummary.Title, composeJSON(entities), category, semanticCategoryDotColor(category), activityID)
		if err == nil {
			err = refreshActivityOutcomeStatusTx(tx, activityID, nowMs)
		}
		if err == nil {
			err = recomputeActivityLinkReasons(tx, activityID, nowMs)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ActivityStore) activityMergeCorrected(fromID, toID string) (bool, error) {
	var count int
	err := s.reader().QueryRow(`SELECT COUNT(*) FROM activity_run_corrections c JOIN activity_run_links l ON l.run_id = c.run_id
		WHERE (c.activity_id = ? AND l.activity_id = ?) OR (c.activity_id = ? AND l.activity_id = ?)`, fromID, toID, toID, fromID).Scan(&count)
	return count > 0, err
}
