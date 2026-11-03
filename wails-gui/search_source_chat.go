package main

import "context"

func (a *App) searchChats(ctx context.Context, words []string) ([]searchCandidate, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	rows, err := a.store.SearchChats(ctx, words)
	if err != nil {
		return nil, err
	}
	out := make([]searchCandidate, 0, len(rows))
	for _, row := range rows {
		target := SearchTarget{Kind: "chat", SessionID: row.SessionID}
		if row.Kind != "" {
			target.Kind = "chat_item"
			target.ItemID = row.ID
		}
		out = append(out, searchCandidate{result: SearchResult{ID: row.ID, Place: "chat", Title: row.Title, At: row.At, TopicID: row.TopicID, Target: target}, body: row.Content})
	}
	return out, ctx.Err()
}
