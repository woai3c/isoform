package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"
)

// Readers that catch up on a record another app kept (the browser history)
// would otherwise import what happened while observing was paused. The pause
// log remembers when observing was paused, so they can leave that time out.
const (
	observationPausesKey = "observation_pauses"
	// Include the adaptive reader's 14 local days plus today, with room for
	// DST. Its coverage guard also rejects intervals older than this bound.
	observationPausesKept = 16 * 24 * time.Hour
	// Overflow coalesces exclusions conservatively instead of forgetting them.
	observationPausesMax = 1000
)

// observationPause is one stretch of "Pause observing". End is 0 while it
// lasts. Times are Unix milliseconds.
type observationPause struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// observationPauses reads the pause log. An error means the log could not be
// read; a reader must then treat the time as paused rather than guess.
func (a *App) observationPauses() ([]observationPause, error) {
	return a.observationPausesContext(context.Background())
}

func (a *App) observationPausesContext(ctx context.Context) ([]observationPause, error) {
	return a.observationPausesContextWithPeopleCache(ctx, nil)
}

func (a *App) observationPausesContextWithPeopleCache(ctx context.Context, cache *peopleReadGateCache) ([]observationPause, error) {
	if memo := mainChatReadMemoFrom(ctx); memo != nil {
		memo.mu.Lock()
		defer memo.mu.Unlock()
		if !memo.pausesLoaded {
			memo.pauses, memo.pausesErr = a.readObservationPausesContext(ctx, cache)
			memo.pausesLoaded = true
		}
		return memo.pauses, memo.pausesErr
	}
	return a.readObservationPausesContext(ctx, cache)
}

func (a *App) readObservationPausesContext(ctx context.Context, cache *peopleReadGateCache) ([]observationPause, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("the store is not ready")
	}
	if cache != nil && cache.pauseLogSet {
		return append([]observationPause(nil), cache.pauseLog...), cache.pauseLogErr
	}
	raw, err := a.peopleUIStateValue(ctx, observationPausesKey, cache)
	recordMainChatReadMemoLoad("pauses")
	if err != nil {
		return nil, err
	}
	var pauses []observationPause
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &pauses); err != nil {
			if cache != nil {
				cache.pauseLogSet = true
				cache.pauseLogErr = err
			}
			return nil, err
		}
	}
	if cache != nil {
		cache.pauseLogSet = true
		cache.pauseLog = append([]observationPause(nil), pauses...)
	}
	return pauses, nil
}

func (a *App) saveObservationPauses(pauses []observationPause) {
	raw, err := json.Marshal(pauses)
	if err != nil {
		return
	}
	if err := a.store.SetUIState(context.Background(), observationPausesKey, string(raw)); err != nil {
		log.Printf("[observation] save pause log: %v", err)
		return
	}
	// An observation pause changes what a sealed brief may show: re-seal before returning.
	briefPrivacyChanged()
	briefSourceChanged()
}

// recordObservationPause opens a pause when observing is paused and closes
// the open one when it resumes. A log it cannot read is left as it is rather
// than overwritten.
func (a *App) recordObservationPause(paused bool, at time.Time) {
	if a == nil || a.store == nil {
		return
	}
	pauses, err := a.observationPauses()
	if err != nil {
		log.Printf("[observation] read pause log: %v", err)
		return
	}
	a.saveObservationPauses(nextObservationPauses(pauses, paused, at))
}

// closeOpenObservationPause closes a pause left open by a quit while paused;
// observing starts unpaused, so the pause ended at the latest by now.
func (a *App) closeOpenObservationPause(at time.Time) {
	if a == nil || a.store == nil {
		return
	}
	pauses, err := a.observationPauses()
	if err != nil {
		return
	}
	if n := len(pauses); n > 0 && pauses[n-1].End == 0 {
		a.saveObservationPauses(nextObservationPauses(pauses, false, at))
	}
}

// nextObservationPauses applies one change to the log and forgets closed
// pauses older than the retained coverage window.
func nextObservationPauses(pauses []observationPause, paused bool, at time.Time) []observationPause {
	ms := at.UnixMilli()
	open := len(pauses) > 0 && pauses[len(pauses)-1].End == 0
	switch true {
	case paused && !open:
		pauses = append(pauses, observationPause{Start: ms})
	case !paused && open:
		pauses[len(pauses)-1].End = ms
	}
	cutoff := at.Add(-observationPausesKept).UnixMilli()
	kept := pauses[:0]
	for _, p := range pauses {
		if p.End == 0 || p.End >= cutoff {
			kept = append(kept, p)
		}
	}
	if len(kept) > observationPausesMax {
		// A size limit must not turn excluded history into readable history.
		// Merge the oldest excess intervals, including their intervening gaps.
		lastMerged := len(kept) - observationPausesMax
		merged := kept[0]
		for _, p := range kept[1 : lastMerged+1] {
			if p.Start < merged.Start {
				merged.Start = p.Start
			}
			if p.End == 0 || (merged.End != 0 && p.End > merged.End) {
				merged.End = p.End
			}
		}
		kept[lastMerged] = merged
		kept = kept[lastMerged:]
	}
	return kept
}

// pausedAt reports whether observing was paused at the given time.
func pausedAt(pauses []observationPause, at time.Time) bool {
	ms := at.UnixMilli()
	for _, p := range pauses {
		if ms >= p.Start && (p.End == 0 || ms < p.End) {
			return true
		}
	}
	return false
}
