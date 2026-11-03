package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"wails-gui/storage"
)

type MainChatWaitingState struct {
	SessionID     string           `json:"sessionId"`
	Day           string           `json:"day"`
	Count         int              `json:"count"`
	Conversations int              `json:"conversations"`
	Finished      int              `json:"finished"`
	Routine       int              `json:"routine"`
	Items         []StreamItemData `json:"items"`
	Revision      uint64           `json:"revision"`
	Ready         bool             `json:"ready"`
}

const mainChatWaitingChangedEvent = "main-chat:waiting-changed"

type mainChatWaitingStateHolder struct {
	mu            sync.Mutex
	state         MainChatWaitingState
	generation    uint64
	running       bool
	dirty         bool
	stopped       bool
	started       bool
	cancel        context.CancelFunc
	done          chan struct{}
	workers       sync.WaitGroup
	timer         *time.Timer
	retryTimer    *time.Timer
	retryDelay    time.Duration
	unsubscribe   func()
	beforeCompute func()
}

func (a *App) GetMainChatWaiting() (perfValue MainChatWaitingState) {
	defer logPageBinding("GetMainChatWaiting", time.Now(), &perfValue)
	if a == nil {
		return MainChatWaitingState{Items: []StreamItemData{}}
	}
	a.mainChatWaiting.mu.Lock()
	perfValue = a.mainChatWaiting.state
	perfValue.Items = append([]StreamItemData(nil), perfValue.Items...)
	a.mainChatWaiting.mu.Unlock()
	if perfValue.Items == nil {
		perfValue.Items = []StreamItemData{}
	}
	return perfValue
}

func (a *App) startMainChatWaitingLifecycle(parent context.Context) {
	if a == nil {
		return
	}
	holder := &a.mainChatWaiting
	holder.mu.Lock()
	if holder.started || holder.stopped {
		holder.mu.Unlock()
		return
	}
	holder.started = true
	ctx, cancel := context.WithCancel(parent)
	holder.cancel = cancel
	holder.done = make(chan struct{})
	holder.unsubscribe = subscribePrivacyWithdrawals(func(privacyWithdrawal) {
		a.markMainChatWaitingDirty("privacy-withdrawal")
	})
	done := holder.done
	holder.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.markMainChatWaitingDirty("safety-resync")
			}
		}
	}()
	a.markMainChatWaitingDirty("startup")
}

func (a *App) stopMainChatWaitingLifecycle() {
	if a == nil {
		return
	}
	holder := &a.mainChatWaiting
	holder.mu.Lock()
	holder.stopped = true
	cancel, done, unsubscribe := holder.cancel, holder.done, holder.unsubscribe
	holder.cancel = nil
	holder.unsubscribe = nil
	if holder.timer != nil {
		holder.timer.Stop()
		holder.timer = nil
	}
	if holder.retryTimer != nil {
		holder.retryTimer.Stop()
		holder.retryTimer = nil
	}
	holder.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	holder.workers.Wait()
}

func (a *App) markMainChatWaitingDirty(reason string) {
	if a == nil {
		return
	}
	holder := &a.mainChatWaiting
	holder.mu.Lock()
	if holder.stopped || !holder.started {
		holder.mu.Unlock()
		return
	}
	holder.generation++
	if reason == "privacy-withdrawal" || strings.HasPrefix(reason, "privacy:") {
		holder.state.Count = 0
		holder.state.Items = []StreamItemData{}
		holder.state.Revision++
	}
	if holder.running {
		holder.dirty = true
		holder.mu.Unlock()
		return
	}
	holder.running = true
	holder.workers.Add(1)
	holder.mu.Unlock()
	go a.runMainChatWaitingWorker(reason)
}

func (a *App) runMainChatWaitingWorker(reason string) {
	holder := &a.mainChatWaiting
	defer holder.workers.Done()
	for {
		holder.mu.Lock()
		generation := holder.generation
		holder.dirty = false
		beforeCompute := holder.beforeCompute
		holder.mu.Unlock()
		if beforeCompute != nil {
			beforeCompute()
		}
		state, nextExpiry, err := a.computeMainChatWaiting()
		if err != nil {
			log.Printf("[main-chat] waiting resync failed: %s", safeLogError(err))
			holder.mu.Lock()
			if holder.stopped {
				holder.running = false
				holder.dirty = false
				holder.mu.Unlock()
				return
			}
			if generation != holder.generation {
				holder.mu.Unlock()
				continue
			}
			if holder.retryTimer == nil {
				delay := holder.retryDelay
				if delay <= 0 {
					delay = 30 * time.Second
				}
				holder.retryTimer = time.AfterFunc(delay, func() {
					holder.mu.Lock()
					holder.retryTimer = nil
					holder.mu.Unlock()
					a.markMainChatWaitingDirty("retry")
				})
			}
			if holder.dirty {
				holder.mu.Unlock()
				continue
			}
			holder.running = false
			holder.mu.Unlock()
			return
		}
		holder.mu.Lock()
		if holder.stopped {
			holder.running = false
			holder.dirty = false
			holder.mu.Unlock()
			return
		}
		if generation != holder.generation {
			holder.mu.Unlock()
			continue
		}
		if holder.retryTimer != nil {
			holder.retryTimer.Stop()
			holder.retryTimer = nil
		}
		previous := holder.state
		state.Ready = true
		state.Revision = previous.Revision + 1
		changed := previous.Count != state.Count ||
			previous.SessionID != state.SessionID ||
			previous.Day != state.Day ||
			previous.Conversations != state.Conversations ||
			previous.Finished != state.Finished ||
			previous.Routine != state.Routine ||
			!sameWaitingItemIDs(previous.Items, state.Items)
		holder.state = state
		if holder.timer != nil {
			holder.timer.Stop()
			holder.timer = nil
		}
		if !nextExpiry.IsZero() {
			delay := time.Until(nextExpiry)
			if delay < 0 {
				delay = 0
			}
			holder.timer = time.AfterFunc(delay, func() {
				a.markMainChatWaitingDirty("expiry")
			})
		}
		runAgain := holder.dirty
		holder.dirty = false
		if !runAgain {
			holder.running = false
		}
		holder.mu.Unlock()

		if changed {
			a.emitBrowserEvent(mainChatWaitingChangedEvent, state)
		}
		if !runAgain {
			return
		}
		reason = "coalesced:" + reason
	}
}

func (a *App) computeMainChatWaiting() (MainChatWaitingState, time.Time, error) {
	now := time.Now()
	start := mainChatDayStart(now)
	out := MainChatWaitingState{Day: start.Format("2006-01-02"), Items: []StreamItemData{}}
	if a == nil || a.store == nil {
		return out, time.Time{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withMainChatReadMemo(ctx)
	snapshot, err := a.store.ReadMainChatView(ctx, start.UnixMilli())
	if err != nil {
		return out, time.Time{}, withLogStage("read main chat view", err)
	}
	out.SessionID = snapshot.SessionID
	projected := a.projectMainChatRows(ctx, snapshot.Items)
	out.Conversations, out.Finished, out.Routine = mainChatTodayCounts(snapshot.Items, projected, now)
	count, _, items := projectMainChatWaitingRows(snapshot.Items, projected, now, 5)
	out.Count = count
	out.Items = items
	nextExpiry := earliestMainChatWaitingExpiry(snapshot.Items, projected, now)
	release, err := a.projectMainChatView(ctx, out.SessionID, out.Items, now)
	if err != nil {
		return MainChatWaitingState{Items: []StreamItemData{}}, time.Time{}, withLogStage("project topic membership", err)
	}
	defer release()
	for i, j := 0, len(out.Items)-1; i < j; i, j = i+1, j-1 {
		out.Items[i], out.Items[j] = out.Items[j], out.Items[i]
	}
	return out, nextExpiry, withLogStage("waiting budget after projection", ctx.Err())
}

func (a *App) mainChatRowsChanged(sessionID string) {
	if a == nil {
		return
	}
	holder := &a.mainChatWaiting
	holder.mu.Lock()
	isMainSession := !holder.state.Ready || holder.state.SessionID == "" || holder.state.SessionID == sessionID
	holder.mu.Unlock()
	if !isMainSession && sessionID != "" && a.store != nil {
		session, err := a.store.LoadSession(context.Background(), sessionID)
		isMainSession = err == nil && session.Kind == storage.SessionKindMain
	}
	if isMainSession {
		a.markMainChatWaitingDirty("main-chat-write")
	}
}

func projectMainChatWaitingRows(rows []storage.StreamItemRow, projected []StreamItemData, now time.Time, limit int) (int, []string, []StreamItemData) {
	count := 0
	ids := make([]string, 0, min(limit, len(rows)))
	items := make([]StreamItemData, 0, min(limit, len(rows)))
	for i, row := range rows {
		if i >= len(projected) || mainChatRowClassForDisplay(row, projected[i].ToolInput, now) != MainChatRowWaiting {
			continue
		}
		count++
		if limit > 0 && len(items) < limit {
			ids = append(ids, row.ID)
			item := projected[i]
			if row.ToolName == mainChatProactiveTool {
				item.ToolInput = mainChatDigestWaitingProjection(item.ToolInput, now)
			}
			items = append(items, item)
		}
	}
	return count, ids, items
}

func mainChatDigestWaitingProjection(input string, now time.Time) string {
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &payload) != nil {
		return input
	}
	rawChildren, ok := payload["items"]
	if !ok {
		return input
	}
	var children []json.RawMessage
	if json.Unmarshal(rawChildren, &children) != nil {
		return input
	}
	waitingChildren := make([]json.RawMessage, 0, len(children))
	for _, rawChild := range children {
		var child mainChatProactive
		if json.Unmarshal(rawChild, &child) == nil && mainChatProactiveRowClass(child, now) == MainChatRowWaiting {
			waitingChildren = append(waitingChildren, rawChild)
		}
	}
	filtered, err := json.Marshal(waitingChildren)
	if err != nil {
		return input
	}
	payload["items"] = filtered

	decoder := json.NewDecoder(strings.NewReader(input))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return input
	}
	var start, end int
	found := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return input
		}
		key, ok := token.(string)
		if !ok {
			return input
		}
		valueStart := jsonValueStart(input, int(decoder.InputOffset()))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return input
		}
		if key == "items" {
			start, end, found = valueStart, int(decoder.InputOffset()), true
		}
	}
	if !found || start < 0 || end > len(input) || start > end {
		return input
	}
	return input[:start] + string(payload["items"]) + input[end:]
}

func jsonValueStart(input string, offset int) int {
	for offset < len(input) {
		switch input[offset] {
		case ' ', '\t', '\n', '\r', ':':
			offset++
		default:
			return offset
		}
	}
	return offset
}

func earliestMainChatWaitingExpiry(rows []storage.StreamItemRow, projected []StreamItemData, now time.Time) time.Time {
	var earliest time.Time
	for i, row := range rows {
		if i >= len(projected) || row.ToolName != mainChatProactiveTool ||
			mainChatRowClassForDisplay(row, projected[i].ToolInput, now) != MainChatRowWaiting {
			continue
		}
		var card mainChatProactive
		if json.Unmarshal([]byte(projected[i].ToolInput), &card) != nil {
			continue
		}
		var visit func(mainChatProactive)
		visit = func(item mainChatProactive) {
			if item.ExpiresAt > now.UnixMilli() {
				expiry := time.UnixMilli(item.ExpiresAt)
				if earliest.IsZero() || expiry.Before(earliest) {
					earliest = expiry
				}
			}
			for _, child := range item.Items {
				if mainChatProactiveRowClass(child, now) == MainChatRowWaiting {
					visit(child)
				}
			}
		}
		visit(card)
	}
	return earliest
}

func sameWaitingItemIDs(left, right []StreamItemData) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID {
			return false
		}
	}
	return true
}
