package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Today's chat sources are read by many cards, topics and briefs at once on
// launch. todayChatSourcesCoalescer lets concurrent callers share one
// collection per privacy stamp (singleflight keyed on the stamp), and while
// that collection runs, callers under the same stamp get the last result.
//
// The stamp is what privacy changes move: closed eyes, sensitive apps, pauses,
// privacy withdrawals (forget, delete, account removal all publish one, see
// publishPrivacyWithdrawal), memory writes and the context-use deletion
// sequence. A withdrawal changes the stamp at once, so a result collected
// before it is never served after it; a read that started under the old stamp
// is never handed to a caller under the new one.
type todayChatSourcesCoalescer struct {
	group singleflight.Group
	seq   atomic.Uint64

	mu         sync.Mutex
	last       *todayChatSourcesSnapshot
	refreshing map[string]bool
}

type todayChatSourcesSnapshot struct {
	stamp   string
	seq     uint64
	sources todayChatSources
}

// todayChatSourcesStamp names the privacy inputs the source collection reads.
// It returns "" when a stamp cannot be read, which disables coalescing.
func (a *App) todayChatSourcesStamp(ctx context.Context) string {
	key := a.interestsReadKeyContext(ctx)
	if key == "" {
		return ""
	}
	return fmt.Sprintf("%s|%d", key, a.contextUseEpoch())
}

// collectTodayChatSourcesCoalesced returns the unfiltered Today sources (the
// nil-activities read of collectTodayChatSourcesWithActivities), shared across
// concurrent callers under one privacy stamp. Each caller gets its own copy.
func (a *App) collectTodayChatSourcesCoalesced(ctx context.Context, now time.Time) todayChatSources {
	stamp := a.todayChatSourcesStamp(ctx)
	if stamp == "" {
		return a.collectTodayChatSourcesWithActivities(ctx, now, nil)
	}
	memo := &a.todayChatSourcesMemo
	memo.mu.Lock()
	if last := memo.last; last != nil && last.stamp == stamp && memo.refreshing[stamp] {
		memo.mu.Unlock()
		return cloneTodayChatSources(last.sources)
	}
	memo.mu.Unlock()
	ch := memo.group.DoChan(stamp, func() (any, error) {
		return a.refreshTodayChatSources(context.WithoutCancel(ctx), now, stamp), nil
	})
	select {
	case r := <-ch:
		snap := r.Val.(*todayChatSourcesSnapshot)
		return cloneTodayChatSources(snap.sources)
	case <-ctx.Done():
		return todayChatSources{useEpoch: a.contextUseEpoch(), Now: now, briefProofs: map[string]bool{}}
	}
}

// refreshTodayChatSources runs one collection for stamp and remembers it as
// the last result when it is newer than the one already remembered.
func (a *App) refreshTodayChatSources(ctx context.Context, now time.Time, stamp string) *todayChatSourcesSnapshot {
	memo := &a.todayChatSourcesMemo
	memo.mu.Lock()
	if memo.refreshing == nil {
		memo.refreshing = map[string]bool{}
	}
	memo.refreshing[stamp] = true
	memo.mu.Unlock()
	snap := &todayChatSourcesSnapshot{stamp: stamp, seq: memo.seq.Add(1)}
	snap.sources = a.collectTodayChatSourcesWithActivities(ctx, now, nil)
	memo.mu.Lock()
	delete(memo.refreshing, stamp)
	if memo.last == nil || snap.seq > memo.last.seq {
		memo.last = snap
	}
	memo.mu.Unlock()
	return snap
}

// cloneTodayChatSources copies the slices and maps a caller may append to or
// write, so callers never share mutable state with the remembered result.
// Nil stays nil.
func cloneTodayChatSources(src todayChatSources) todayChatSources {
	out := src
	out.Memories = cloneSliceOf(src.Memories)
	out.Knowledge = cloneSliceOf(src.Knowledge)
	out.Activities = cloneSliceOf(src.Activities)
	out.People = cloneSliceOf(src.People)
	out.Purposes = cloneSliceOf(src.Purposes)
	out.Reminders = cloneMapOf(src.Reminders)
	out.Self = cloneMapOf(src.Self)
	out.Related = cloneMapOf(src.Related)
	out.topicResolved = cloneMapOf(src.topicResolved)
	out.briefProofs = cloneMapOf(src.briefProofs)
	return out
}

func cloneSliceOf[T any](s []T) []T {
	if s == nil {
		return nil
	}
	return append(make([]T, 0, len(s)), s...)
}

func cloneMapOf[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
