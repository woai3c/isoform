package main

import (
	"context"
)

func collectAndReadTopicViewsForTest(app *App) ([]TopicView, error) {
	if _, err := app.topicsSvc().collectRegistryViewsLocked(context.Background(), true); err != nil {
		return nil, err
	}
	return app.todayTopicViews()
}

func collectAndReadTopicSnapshotViewsForTest(app *App) ([]TopicView, error) {
	if _, err := app.topicsSvc().collectRegistryViewsLocked(context.Background(), true); err != nil {
		return nil, err
	}
	views, _, _ := app.topicsSvc().snapshotRead()
	return views, nil
}

func topicViewsRepresentInterest(app *App, views []TopicView, id string) bool {
	for _, view := range views {
		if view.ID == id {
			return true
		}
	}
	cache := app.topicsSvc().loadRegistryCache()
	if cache == nil {
		return false
	}
	canonical := cache.result.canonical[id]
	for _, view := range views {
		if view.ID == canonical && canonical != "" {
			return true
		}
	}
	return false
}
