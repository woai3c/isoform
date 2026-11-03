package main

import (
	"context"
	"slices"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// Source facts can change while retrieval or SQLite admission waits. The final
// read is under the file/policy barriers; database revisions then carry that
// read through the atomic history commit. A concurrent unrelated database write
// conservatively asks the user to retry instead of admitting an uncertain draft.
func (a *App) admitTodayChatTurn(ctx context.Context, tag string, row storage.StreamItemRow, ref TodayChatContextRef, related map[string]bool, panelAsk ...bool) (todayChatContextResult, int, error) {
	var empty todayChatContextResult
	for _, lock := range []struct {
		try     func() bool
		release func()
	}{
		{a.closedEyes.mu.TryLock, a.closedEyes.mu.Unlock},
		{memoryMutationMu.TryLock, memoryMutationMu.Unlock},
		{memoryFileLocks.publication.TryLock, memoryFileLocks.publication.Unlock},
		{pii.TryLockSensitiveAppsForPublication, pii.UnlockSensitiveAppsForPublication},
	} {
		if err := lockTodayChatPublication(ctx, lock.try); err != nil {
			return empty, -1, err
		}
		defer lock.release()
	}
	if ref.SourceKind == todayChatPausedKind {
		// A paused draft reads and attaches no source; only its turn is admitted.
		revision, err := a.store.ReadRevision(ctx)
		if err != nil {
			return empty, -1, err
		}
		result := buildTodayChatContextSections(todayChatPausedItem(), todayChatSources{Now: time.Now()})
		order, err := a.store.AppendUserStreamItemAtomicIfCurrent(ctx, tag, row, revision, func() error { return nil })
		return result, order, err
	}
	releaseServices, err := a.lockTodayChatFileServices(ctx, ref)
	if err != nil {
		return empty, -1, err
	}
	defer releaseServices()
	files, err := a.readTodayChatAdmissionFiles(ctx, ref)
	if err != nil {
		return empty, -1, err
	}
	revision, err := a.store.ReadRevision(ctx)
	if err != nil {
		return empty, -1, err
	}
	var activityRevision storage.ReadRevision
	if a.activity != nil && a.activity.store != nil {
		activityRevision, err = storage.DatabaseRevision(ctx, a.activity.store.db)
		if err != nil {
			return empty, -1, err
		}
	}
	// Do not repeat the potentially blocking indexed lookup. Its identities are
	// intersected with this fresh privacy-filtered collection and selected item.
	item, sources, err := a.loadTodayChatContextItem(ctx, ref)
	if err != nil {
		return empty, -1, err
	}
	sources.Related = related
	result := buildTodayChatContextSections(item, sources)
	if item.topicID != "" {
		row.TopicID = item.topicID
	}
	if ref.SourceKind == "topic" || item.topicID != "" {
		row.AutomationContext, err = a.topicChatAdmissionMetadataFromSources(ctx, append(item.topicRefs, result.topicPackedRefs...), sources)
		if err != nil {
			return empty, -1, err
		}
		result.topicMetadata = row.AutomationContext
	}
	if !todayChatGeneratedFactsMatch(ref, result) || todayChatCarriesStaleWords(row.Content, item, ref) {
		return empty, -1, errTodayChatItemUnavailable
	}
	if len(panelAsk) > 0 && panelAsk[0] && panelAskCanSave(ref) {
		row.AutomationContext, err = a.panelAskMetadata(ctx, row.AutomationContext, ref, item, result, sources)
		if err != nil {
			return empty, -1, err
		}
		if result.topicMetadata != "" {
			result.topicMetadata = row.AutomationContext
		}
	}
	if a.activity != nil && a.activity.store != nil {
		release, err := storage.LockDatabaseRevision(ctx, a.activity.store.db, activityRevision)
		if err != nil {
			return empty, -1, err
		}
		defer release()
	}
	order, err := a.store.AppendUserStreamItemAtomicIfCurrent(ctx, tag, row, revision, func() error {
		return a.validateTodayChatAdmissionFiles(ctx, ref, files)
	})
	return result, order, err
}

func todayChatGeneratedFactsMatch(ref TodayChatContextRef, result todayChatContextResult) bool {
	return (ref.GeneratedTitle == "" || ref.GeneratedTitle == result.Title) &&
		(len(ref.GeneratedPeople) == 0 || slices.Equal(ref.GeneratedPeople, result.People)) &&
		(ref.GeneratedFingerprint == "" || ref.GeneratedFingerprint == result.PrefillFingerprint)
}

// TryLock avoids waiting beyond Stop/deadline and, for the file barrier, avoids
// blocking nested per-file readers behind a pending exclusive lock.
func lockTodayChatPublication(ctx context.Context, try func() bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if try() {
			return nil
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
