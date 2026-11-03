package main

import (
	"context"
	"reflect"

	"wails-gui/storage"
)

type topicPageRevision struct {
	sources topicRegistryRevision
	topics  string
}

func (a *App) captureTopicPageRevision(ctx context.Context) (topicPageRevision, error) {
	svc := a.topicsSvc()
	// Full source hashes are captured outside barriers; the short locked phase
	// checks only metadata, policy and database revisions plus the topic file.
	rev, err := svc.captureRegistryRevision(ctx)
	if err != nil {
		return topicPageRevision{}, err
	}
	short, cancel := context.WithTimeout(ctx, topicRegistrySnapshotBudget)
	defer cancel()
	release, err := svc.lockRegistrySnapshot(short, true)
	if err != nil {
		return topicPageRevision{}, err
	}
	defer release()
	current, err := svc.registryRevisionWithSessionFiles(short, &rev.sessionFiles)
	current.files = rev.files
	if err != nil || !reflect.DeepEqual(current, rev) {
		return topicPageRevision{}, storage.ErrReadRevisionChanged
	}
	return topicPageRevision{sources: rev, topics: proactiveVoiceHash(svc.store.load())}, nil
}

// Recheck and fence only after page computation, never collect under barriers.
func (a *App) validateTopicPageRevision(ctx context.Context, expected topicPageRevision) (func(), error) {
	short, cancel := context.WithTimeout(ctx, topicRegistrySnapshotBudget)
	defer cancel()
	svc := a.topicsSvc()
	release, err := svc.lockRegistrySnapshot(short, true)
	if err != nil {
		return nil, err
	}
	releases := []func(){release}
	success := false
	unlock := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	defer func() {
		if !success {
			unlock()
		}
	}()
	current, err := svc.registryRevisionWithSessionFiles(short, &expected.sources.sessionFiles)
	current.files = expected.sources.files
	if err != nil || !reflect.DeepEqual(current, expected.sources) || proactiveVoiceHash(svc.store.load()) != expected.topics {
		return nil, storage.ErrReadRevisionChanged
	}
	if current.activityStore != nil {
		release, err := storage.LockDatabaseRevision(short, current.activityStore.db, current.activity)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	if current.mainStore != nil {
		release, err := current.mainStore.LockReadRevision(short, current.main)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	if err := svc.validateRegistryFiles(short, expected.sources); err != nil {
		return nil, storage.ErrReadRevisionChanged
	}
	if proactiveVoiceHash(svc.store.load()) != expected.topics {
		return nil, storage.ErrReadRevisionChanged
	}
	success = true
	return unlock, nil
}

func (a *App) topicPageSources(ctx context.Context) (todayChatSources, error) {
	revision, err := a.captureTopicPageRevision(ctx)
	if err != nil {
		return todayChatSources{}, err
	}
	return a.topicPageSourcesForRevision(ctx, revision.sources)
}

// Collection is shared by every row in a page/batch, and by other readers only
// while A3's checked source revision, withdrawal epoch and time window match.
// Final publication must still validate the captured revision and source files.
func (a *App) topicPageSourcesForRevision(ctx context.Context, revision topicRegistryRevision) (todayChatSources, error) {
	svc := a.topicsSvc()
	now := svc.now()
	if revision.closedEyes {
		return todayChatSources{Now: now}, ctx.Err()
	}
	sources, hit, _ := svc.cachedRegistryChatSources(now, revision)
	if !hit {
		rows, err := a.topicRegistryActivities(ctx, now, svc.store.load().Projects)
		if err != nil {
			return todayChatSources{}, err
		}
		sources = svc.registryChatSources(ctx, topicRegistryData{now: now, activities: rows}, revision)
	}
	// Resolution caches belong to one reader, never to the shared collection.
	sources.topicResolved = map[string]topicChatFingerprintInput{}
	sources.briefProofs = map[string]bool{}
	return sources, ctx.Err()
}
