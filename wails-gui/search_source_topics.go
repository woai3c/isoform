package main

import "context"

// SearchTopicNames is the A1 seam. Its adapter must return only currently
// readable registry names, including followed names during Closed Eyes.
// A1 is absent: production's TopicSource does not implement this yet.
type SearchTopicNames interface {
	SearchTopicNames(context.Context) ([]SearchTopicName, error)
}
type SearchTopicName struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Followed bool   `json:"followed"`
	At       int64  `json:"at"`
}

func (a *App) searchTopics(ctx context.Context, words []string) ([]searchCandidate, error) {
	source, ok := a.topicSource.(SearchTopicNames)
	if !ok {
		return nil, nil
	}
	names, err := source.SearchTopicNames(ctx)
	if err != nil {
		return nil, err
	}
	out := []searchCandidate{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if a.IsClosedEyesModeEnabled() && !name.Followed {
			continue
		}
		if !searchMatches(name.Name, words) {
			continue
		}
		out = append(out, searchCandidate{result: SearchResult{followed: name.Followed, ID: name.ID, Place: "topic", Title: name.Name, At: name.At, Target: SearchTarget{Kind: "topic", ID: name.ID}}})
		if len(out) == searchCandidateLimit {
			break
		}
	}
	return out, ctx.Err()
}
