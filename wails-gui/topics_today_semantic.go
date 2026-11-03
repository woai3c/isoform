package main

import (
	"context"
	"encoding/json"
	"strings"

	"wails-gui/internal/interests"
)

const topicTodaySemanticLimit = 16

// The interest collector also names captured work as semantic:<id>. Verify a
// bounded batch from its original sessions/snapshots, never from its saved text.
// Retained-only or over-budget closures wait for the full background verifier.
func (s *topicsService) partialTodaySemanticSources(ctx context.Context, state topicsStateFile) (map[string]bool, int, error) {
	allowed := map[string]bool{}
	ids, seen := []string{}, map[string]bool{}
	allDone := timeTopicStep("today", "bootstrap_semantic")
	defer func() { allDone("requested", len(ids), "verified", len(allowed)) }()
	for _, item := range s.app.interestsSvc().store.load().Interests {
		if state.Overrides[item.ID].Removed {
			continue
		}
		for _, ref := range item.Evidence {
			if ctx.Err() != nil {
				return nil, len(ids), ctx.Err()
			}
			if ref.Source == interests.SourceActivity && strings.HasPrefix(ref.Key, "semantic:") {
				id := strings.TrimPrefix(ref.Key, "semantic:")
				if id != "" && !seen[id] {
					ids, seen[id] = append(ids, id), true
				}
			}
			if len(ids) == topicTodaySemanticLimit {
				break
			}
		}
		if len(ids) == topicTodaySemanticLimit {
			break
		}
	}
	if len(ids) == 0 {
		return allowed, 0, ctx.Err()
	}
	policy, err := s.app.activityReadPrivacyPolicyContext(ctx)
	if err != nil {
		return nil, len(ids), err
	}
	encoded, _ := json.Marshal(ids)
	queryDone := timeTopicStep("today", "bootstrap_semantic_query")
	rows, err := s.app.activity.store.reader().QueryContext(ctx, `SELECT id,apps,source_refs,start_ms,end_ms FROM activity_semantic_events WHERE id IN (SELECT value FROM json_each(?)) LIMIT ?`, string(encoded), topicTodaySemanticLimit)
	queryDone("requested", len(ids))
	if err != nil {
		return nil, len(ids), err
	}
	rowsDone := timeTopicStep("today", "bootstrap_semantic_rows")
	var candidates []SemanticEventRow
	defer func() { rowsDone("requested", len(ids), "rows", len(candidates)) }()
	for rows.Next() {
		var row SemanticEventRow
		if err := rows.Scan(&row.ID, &row.Apps, &row.SourceRefs, &row.StartMs, &row.EndMs); err != nil {
			rows.Close()
			return nil, len(ids), err
		}
		candidates = append(candidates, row)
	}
	err = rows.Err()
	rows.Close()
	rowsDone("requested", len(ids), "rows", len(candidates))
	if err != nil {
		return nil, len(ids), err
	}
	checksDone := timeTopicStep("today", "bootstrap_semantic_checks")
	checked := 0
	defer func() { checksDone("rows", len(candidates), "checked", checked, "verified", len(allowed)) }()
	for _, row := range candidates {
		ok, err := s.partialTodaySemanticSource(ctx, row, policy)
		checked++
		if err != nil {
			return nil, len(ids), err
		}
		if ok {
			allowed["semantic:"+row.ID] = true
		}
	}
	checksDone("rows", len(candidates), "checked", checked, "verified", len(allowed))
	return allowed, len(ids), ctx.Err()
}

func (s *topicsService) partialTodaySemanticSource(ctx context.Context, row SemanticEventRow, policy activityReadPrivacyPolicy) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	var refs semanticSourceRefs
	source := activitySourceProvenance{StartTime: row.StartMs, EndTime: row.EndMs}
	if json.Unmarshal([]byte(row.Apps), &source.Apps) != nil || json.Unmarshal([]byte(row.SourceRefs), &refs) != nil || len(refs.SessionIDs) == 0 || len(refs.SessionIDs) > 64 || len(refs.SnapshotIDs) > 64 || !policy.allows(source) {
		return false, nil
	}
	store := s.app.activity.store
	sessions, err := store.GetSessionsByIDsContext(ctx, refs.SessionIDs)
	if err != nil {
		return false, err
	}
	for _, id := range refs.SessionIDs {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		session := sessions[id]
		if session == nil {
			return false, nil
		}
		if ok, err := s.app.activitySessionAllowsWithPolicy(store, *session, policy); err != nil || !ok {
			return false, err
		}
	}
	if len(refs.SnapshotIDs) == 0 {
		return true, ctx.Err()
	}
	encoded, _ := json.Marshal(refs.SnapshotIDs)
	rows, err := store.reader().QueryContext(ctx, `SELECT id,session_id FROM activity_snapshots WHERE id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	checked := map[int64]bool{}
	for rows.Next() {
		var id int64
		var sessionID string
		if err := rows.Scan(&id, &sessionID); err != nil {
			return false, err
		}
		checked[id] = sessions[sessionID] != nil
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, id := range refs.SnapshotIDs {
		if !checked[id] {
			return false, nil
		}
	}
	return true, ctx.Err()
}
