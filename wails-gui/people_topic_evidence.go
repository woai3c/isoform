package main

import (
	"context"
	"encoding/json"
	"strings"
)

// The user's observed activity supplies the self side of membership. Require
// the person and the named topic in that same readable source, rather than
// taking either identity from the registry's project-wide union of names.
func peopleTopicActivityMatches(row ActivityRow, topicName string, names map[string]bool) bool {
	var entities []Entity
	if json.Unmarshal([]byte(row.Entities), &entities) != nil {
		return false
	}
	person := false
	topic := topicNameWithin(row.Title, topicName)
	for _, entity := range entities {
		switch entity.Type {
		case entityPerson:
			person = person || names[threadNorm(entity.Name)]
		case entityProject, entityRepo, entityChannel:
			topic = topic || topicNameWithin(entity.Name, topicName)
		}
	}
	return person && topic
}

func (a *App) peopleTopicSourceShared(ctx context.Context, topic peopleWorkTopic, names map[string]bool) bool {
	if a.activity == nil || a.activity.store == nil || len(topic.ActivityIDs) == 0 || ctx.Err() != nil {
		return false
	}
	store := a.activity.store
	for start := 0; start < len(topic.ActivityIDs); start += 400 {
		end := min(start+400, len(topic.ActivityIDs))
		ids := topic.ActivityIDs[start:end]
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		query := `SELECT ` + activityColumns + ` FROM activities WHERE id IN (` + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + `)`
		rows, err := store.reader().QueryContext(ctx, query, args...)
		if err != nil {
			return false
		}
		activities, err := scanActivityRowsContext(ctx, rows)
		rows.Close()
		if err != nil {
			return false
		}
		var candidates []ActivityRow
		for _, row := range activities {
			if peopleTopicActivityMatches(row, topic.Name, names) {
				candidates = append(candidates, row)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		readable, err := a.filterTopicRegistryActivities(ctx, candidates)
		if err != nil {
			return false
		}
		if len(readable) > 0 {
			return true
		}
	}
	return false
}
