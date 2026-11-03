package main

import (
	"context"
	"sync"
	"time"
)

// Completed card summaries share the thread projection's live privacy proof.
// Only exact, past calendar days are retained; today's live cards always read
// fresh. The cache is process-local and bounded like the renderer's read LRU.
var activityDayCards = struct {
	sync.Mutex
	entries map[activityThreadCacheKey]activityDayCardsEntry
}{entries: map[activityThreadCacheKey]activityDayCardsEntry{}}

type activityDayCardsEntry struct {
	signature activityThreadProjectionStamp
	value     []ActivityView
}

func (a *App) GetActivitiesInRange(startMs, endMs int64) (perfValue []ActivityView, perfErr error) {
	defer logPageBinding("GetActivitiesInRange", time.Now(), &perfValue)
	day := startOfDayLocal(startMs)
	if a == nil || a.activity == nil || a.activity.store == nil {
		return nil, nil
	}
	if startMs != day || endMs != activityThreadDayEnd(day) || day >= startOfDayLocal(time.Now().UnixMilli()) {
		return a.buildActivitiesInRange(startMs, endMs)
	}
	ctx := context.Background()
	signature, cacheable, proofErr := a.activityThreadProjectionSignature(ctx, day)
	key := activityThreadCacheKey{store: a.activity.store, day: day}
	activityDayCards.Lock()
	entry, found := activityDayCards.entries[key]
	activityDayCards.Unlock()
	if proofErr == nil && cacheable && found && entry.signature == signature {
		return entry.value, nil
	}
	value, err := a.buildActivitiesInRange(startMs, endMs)
	if err != nil {
		return nil, err
	}
	// The generic range API is inclusive at both edges. A cached calendar day
	// owns only overlapping moments in [midnight, next midnight), matching
	// its source proof and the renderer's day interval.
	inside := value[:0]
	for _, view := range value {
		if view.EndMs > startMs && view.StartMs < endMs {
			inside = append(inside, view)
		}
	}
	clear(value[len(inside):])
	value = inside
	after, stillCacheable, afterErr := a.activityThreadProjectionSignature(ctx, day)
	if proofErr == nil && afterErr == nil && cacheable && stillCacheable && signature == after {
		activityDayCards.Lock()
		if len(activityDayCards.entries) >= 64 {
			for old := range activityDayCards.entries {
				delete(activityDayCards.entries, old)
				break
			}
		}
		activityDayCards.entries[key] = activityDayCardsEntry{signature: signature, value: value}
		activityDayCards.Unlock()
	}
	return value, nil
}
