package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/handoff"
)

// Not now on Needs you, shared by the computer and the phone (phone threads
// v1.1.0): the item comes back with the next brief, two hours before it's
// due, or when something new happens to it. A task keeps its Not now on its
// thread; other waiting items keep theirs in ui_state.
const (
	needsPutOffActionPutOff    = "put_off"
	needsPutOffActionBringBack = "bring_back"
	needsPutOffActionKeep      = "keep"
	// needsPutOffActionForget puts it away until something new happens.
	needsPutOffActionForget = "forget"
	needsPutOffStateKey     = "needs_you.put_off"
	// needsPutOffFallback is when an item comes back when no brief is
	// scheduled and it has no due time.
	needsPutOffFallback = 24 * time.Hour
	// needsForgetFor is how long Forget keeps an item away when nothing new
	// happens to it.
	needsForgetFor = 10 * 365 * 24 * time.Hour
)

var (
	needsPutOffMu sync.Mutex
	// needsPutOffNextBrief is when the next brief is made (unix ms).
	needsPutOffNextBrief = func(a *App) int64 { return a.nextBriefItem().NextRunAt }
)

func (a *App) needsPutOffUntil(now time.Time, due int64) int64 {
	return handoff.PutOffReturn(now, needsPutOffNextBrief(a), due, needsPutOffFallback)
}

// todayNeedMark is what "something new happened" compares for a waiting item
// that is not a task.
func todayNeedMark(need TodayNeed) string {
	return strings.Join([]string{need.Kind, need.Sentence, strconv.FormatInt(need.DueAt, 10), need.StopCause}, "|")
}

func (a *App) loadNeedsPutOff() map[string]*handoff.PutOff {
	out := map[string]*handoff.PutOff{}
	if a == nil || a.store == nil {
		return out
	}
	raw, err := a.store.GetUIState(context.Background(), needsPutOffStateKey)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func (a *App) saveNeedsPutOff(state map[string]*handoff.PutOff) error {
	if a == nil || a.store == nil {
		return errors.New("store not ready")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return a.store.SetUIState(context.Background(), needsPutOffStateKey, string(raw))
}

// partitionPutOff splits Needs you into what shows and what is put off.
func (a *App) partitionPutOff(items []TodayNeed, handoffFor map[string]string, entries []handoff.Entry, now time.Time) (shown, away []TodayNeed, nextReturn int64) {
	byID := make(map[string]handoff.Entry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	var chat map[string]*handoff.PutOff
	nowMs := now.UnixMilli()
	shown, away = []TodayNeed{}, []TodayNeed{}
	for _, need := range items {
		id := handoffFor[need.ID]
		if id == "" && need.TargetKind == "handoff" {
			id = need.TargetID
		}
		var putOff *handoff.PutOff
		mark, isAway, until := "", false, int64(0)
		if id != "" {
			entry, ok := byID[id]
			if ok && entry.Thread != nil {
				putOff, mark = entry.Thread.PutOff, handoff.EntryMark(entry)
				isAway = handoff.PutOffAway(entry, now)
				until = entry.Thread.NotNowUntil
			}
		} else {
			if chat == nil {
				chat = a.loadNeedsPutOff()
			}
			putOff, mark = chat[need.ID], todayNeedMark(need)
			isAway = putOff.Away(nowMs, mark)
		}
		if isAway {
			if putOff.Away(nowMs, mark) {
				until = putOff.Until
			}
			need.PutOffUntil = until
			if nextReturn == 0 || until < nextReturn {
				nextReturn = until
			}
			away = append(away, need)
			continue
		}
		need.AsksKeep = putOff.AsksKeep(nowMs, mark)
		shown = append(shown, need)
	}
	return shown, away, nextReturn
}

// putOffHandoff records a task's Not now. Bookkeeping, not news: the task
// keeps its update time and place.
func (a *App) putOffHandoff(id string, now time.Time, due int64) (handoff.Entry, error) {
	entry, err := a.handoffStore.Get(id)
	if err != nil {
		return handoff.Entry{}, err
	}
	if !handoff.IsChipStatus(entry.Status) {
		return handoff.Entry{}, errors.New("not waiting")
	}
	until, mark := a.needsPutOffUntil(now, due), handoff.EntryMark(entry)
	return a.handoffStore.MutateThread(id, func(thread *handoff.Thread) {
		thread.NotNowUntil = 0
		thread.PutOff = thread.PutOff.Next(now, until, mark)
	})
}

// PutOffNeedsYou answers a Needs you item: put_off (Not now), bring_back
// (Bring back now) or keep (Keep, on its second return). id is the task's id
// for a task and the item's id otherwise. Wails binding.
func (a *App) PutOffNeedsYou(id, action string) error {
	switch action {
	case needsPutOffActionPutOff, needsPutOffActionBringBack, needsPutOffActionKeep, needsPutOffActionForget:
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	now := time.Now()
	page, err := a.GetNeedsYou(0)
	if err != nil {
		return err
	}
	var need *TodayNeed
	for _, list := range [][]TodayNeed{page.Items, page.PutOff} {
		for i := range list {
			if list[i].ID == id || (list[i].TargetKind == "handoff" && list[i].TargetID == id) {
				need = &list[i]
			}
		}
	}
	if need == nil {
		return errors.New("that item isn't waiting anymore")
	}
	if a.handoffStore != nil {
		if entry, gerr := a.handoffStore.Get(id); gerr == nil {
			var saved handoff.Entry
			switch action {
			case needsPutOffActionPutOff:
				saved, err = a.putOffHandoff(id, now, need.DueAt)
			case needsPutOffActionForget:
				saved, err = a.handoffStore.MutateThread(id, func(thread *handoff.Thread) {
					thread.NotNowUntil = 0
					thread.PutOff = thread.PutOff.Next(now, now.Add(needsForgetFor).UnixMilli(), handoff.EntryMark(entry))
				})
			default:
				saved, err = a.handoffStore.MutateThread(id, func(thread *handoff.Thread) {
					thread.NotNowUntil = 0
					if action == needsPutOffActionKeep {
						thread.PutOff = nil
					} else if thread.PutOff != nil {
						back := *thread.PutOff
						back.Until = 0
						thread.PutOff = &back
					}
				})
			}
			if err != nil {
				return err
			}
			a.emitBrowserEvent("handoff:updated", map[string]any{"id": id, "status": string(saved.Status)})
			return nil
		}
	}
	needsPutOffMu.Lock()
	defer needsPutOffMu.Unlock()
	state := a.loadNeedsPutOff()
	live := map[string]bool{}
	for _, list := range [][]TodayNeed{page.Items, page.PutOff} {
		for _, item := range list {
			live[item.ID] = true
		}
	}
	for key := range state {
		if !live[key] {
			delete(state, key)
		}
	}
	switch action {
	case needsPutOffActionPutOff:
		state[need.ID] = state[need.ID].Next(now, a.needsPutOffUntil(now, need.DueAt), todayNeedMark(*need))
	case needsPutOffActionBringBack:
		if current := state[need.ID]; current != nil {
			back := *current
			back.Until = 0
			state[need.ID] = &back
		}
	case needsPutOffActionKeep:
		delete(state, need.ID)
	case needsPutOffActionForget:
		state[need.ID] = state[need.ID].Next(now, now.Add(needsForgetFor).UnixMilli(), todayNeedMark(*need))
	}
	if err := a.saveNeedsPutOff(state); err != nil {
		return err
	}
	a.emitBrowserEvent("today:needs-changed")
	return nil
}
