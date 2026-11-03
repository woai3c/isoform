package main

import (
	"context"
	"encoding/json"
	"time"
)

// Only a desktop topic send may use this scoped proof read. The registry's
// stored synthesis must predate the retained pause, and every original source
// must still match its fingerprint and current withdrawal policy.
type pausedTopicReadKey struct{}

func pausedTopicReadAllowed(ctx context.Context) bool {
	allowed, _ := ctx.Value(pausedTopicReadKey{}).(bool)
	return allowed
}

func (a *App) loadPausedTodayTopic(ctx context.Context, ref TodayChatContextRef) (todayChatItem, todayChatSources, error) {
	if _, ok := a.topicSource.(*registryTopicSource); !ok {
		return todayChatItem{}, todayChatSources{}, errTodayChatItemUnavailable
	}
	since, err := a.observationPauseCoverageStart()
	if err != nil || since <= 0 {
		return todayChatItem{}, todayChatSources{}, errTodayChatItemUnavailable
	}
	ctx = context.WithValue(ctx, pausedBriefReadKey{}, true)
	sources := a.collectTodayChatSources(ctx, time.Now())
	sources.topicResolved = map[string]topicChatFingerprintInput{}
	topic, _, err := a.chatTopicSnapshotFromSources(ctx, ref.SourceID, sources)
	if err != nil {
		return todayChatItem{}, todayChatSources{}, err
	}
	payload, err := json.Marshal(struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Latest string `json:"latest"`
		Next   string `json:"next"`
	}{topic.Name, topic.Status, topic.Latest, topic.Next})
	if err != nil {
		return todayChatItem{}, todayChatSources{}, err
	}
	// The source collection exists solely for proof: do not pack unrelated
	// records, or activity collected after observation was paused.
	proof := todayChatSources{Now: sources.Now, topicResolved: sources.topicResolved}
	return todayChatItem{Record: todayChatRecord{Kind: "topic", ID: ref.SourceID, Text: string(payload)},
		Label: topic.Name, topicRefs: topic.Refs, pausedTopic: true}, proof, ctx.Err()
}
