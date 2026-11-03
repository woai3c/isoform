package storage

import (
	"context"
	"encoding/json"
	"strings"
)

// BriefChatReportRef is a reference to a previously delivered report. Content,
// titles, tool arguments and chat transcripts are deliberately not returned.
type BriefChatReportRef struct {
	ID        string
	Key       string
	CreatedAt int64
}

// BriefChatReportReferences finds exact report keys, even outside the recent
// chat window. Brief generation is read-only and cannot create a main session.
func (s *Store) BriefChatReportReferences(ctx context.Context, sessionID, tool, source, status string, keys []string, asOf int64) ([]BriefChatReportRef, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(keys))
	args := []any{sessionID, tool, asOf, source, status}
	for i, key := range keys {
		placeholders[i] = "?"
		args = append(args, key)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, json_extract(tool_input, '$.key'), created_at
		FROM stream_items
		WHERE session_local_id=? AND tool_name=? AND created_at<=?
		AND CASE WHEN json_valid(tool_input) THEN
			json_extract(tool_input, '$.source')=? AND json_extract(tool_input, '$.status')=?
			AND json_extract(tool_input, '$.key') IN (`+strings.Join(placeholders, ",")+`)
		ELSE 0 END
		ORDER BY created_at DESC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BriefChatReportRef
	for rows.Next() {
		var row BriefChatReportRef
		if err := rows.Scan(&row.ID, &row.Key, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// BriefProactiveReferences finds delivered proactive cards by moment id, for a
// single card and for a card inside a digest. Only the chat item id, the moment
// id and the time cross this boundary; titles and bodies are never returned.
// The most recent delivery of a moment comes first.
func (s *Store) BriefProactiveReferences(ctx context.Context, sessionID, tool string, momentIDs []string, asOf int64) ([]BriefChatReportRef, error) {
	if len(momentIDs) == 0 {
		return nil, nil
	}
	wanted := make(map[string]bool, len(momentIDs))
	conditions := make([]string, len(momentIDs))
	args := []any{sessionID, tool, asOf}
	for i, id := range momentIDs {
		wanted[id] = true
		conditions[i] = "instr(tool_input, ?)>0"
		pattern, err := json.Marshal(map[string]string{"momentId": id})
		if err != nil {
			return nil, err
		}
		// {"momentId":"<id>"} without its braces matches the field wherever it sits.
		args = append(args, string(pattern[1:len(pattern)-1]))
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, tool_input
		FROM stream_items
		WHERE session_local_id=? AND tool_name=? AND created_at<=?
		AND (`+strings.Join(conditions, " OR ")+`)
		ORDER BY created_at DESC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BriefChatReportRef
	for rows.Next() {
		var id, input string
		var createdAt int64
		if err := rows.Scan(&id, &createdAt, &input); err != nil {
			return nil, err
		}
		var card struct {
			MomentID string `json:"momentId"`
			Items    []struct {
				MomentID string `json:"momentId"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(input), &card) != nil {
			continue
		}
		if wanted[card.MomentID] {
			out = append(out, BriefChatReportRef{ID: id, Key: card.MomentID, CreatedAt: createdAt})
		}
		for _, child := range card.Items {
			if wanted[child.MomentID] {
				out = append(out, BriefChatReportRef{ID: id, Key: child.MomentID, CreatedAt: createdAt})
			}
		}
	}
	return out, rows.Err()
}

// BriefEngagementMoments resolves only the relevant situation keys. Other
// moment kinds and old unrelated rows cannot crowd a commitment out of a brief.
func (s *Store) BriefEngagementMoments(ctx context.Context, kind string, keys []string, asOf int64) ([]EngagementMomentRow, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(keys))
	args := []any{kind, asOf, EngagementStatePending, EngagementStateScheduled, EngagementStateSent}
	for i, key := range keys {
		placeholders[i] = "?"
		args = append(args, key)
	}
	return s.queryEngagementMoments(ctx, `WHERE kind=? AND created_at<=? AND state IN (?,?,?) AND dedupe_key IN (`+strings.Join(placeholders, ",")+`)`, -1, args...)
}

// BriefWaitingReplyMoments selects this source before the bound, so unrelated
// engagement kinds cannot crowd out an older reply that still needs attention.
func (s *Store) BriefWaitingReplyMoments(ctx context.Context, asOf int64) ([]EngagementMomentRow, error) {
	return s.queryEngagementMoments(ctx, `WHERE kind='waiting_on_reply' AND created_at<=? AND state IN (?,?,?) AND (expires_at=0 OR expires_at>?)`, 128, asOf, EngagementStatePending, EngagementStateScheduled, EngagementStateSent, asOf)
}
