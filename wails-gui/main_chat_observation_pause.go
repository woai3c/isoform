package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"wails-gui/storage"
)

const mainChatObservationPauseTool = "main_chat_observation_pause"

type mainChatObservationPause struct {
	StartAt        int64 `json:"startAt"`
	EndAt          int64 `json:"endAt,omitempty"`
	ResumedUnknown bool  `json:"resumedUnknown,omitempty"`
}

// A restart can end a pause without recording its exact recovery time. Do not
// leave a Resume action on a stale row or present the startup time as recovery.
func (a *App) projectObservationPause(input string) string {
	var pause mainChatObservationPause
	if json.Unmarshal([]byte(input), &pause) != nil || pause.StartAt <= 0 || pause.EndAt > 0 || a.IsClosedEyesModeEnabled() {
		return input
	}
	pause.ResumedUnknown = true
	projected, err := json.Marshal(pause)
	if err != nil {
		return input
	}
	return string(projected)
}

var mainChatObservationPauseMu sync.Mutex

// The pause log supplies the actual start and end. A restart's privacy
// coverage boundary is not used to invent an observing start.
func (a *App) syncMainChatObservationPause(paused bool, at time.Time) {
	if a == nil || a.store == nil || at.IsZero() {
		return
	}
	mainChatObservationPauseMu.Lock()
	defer mainChatObservationPauseMu.Unlock()
	pauses, err := a.observationPauses()
	if err != nil {
		return
	}
	var found observationPause
	for _, pause := range pauses {
		if (paused && pause.Start == at.UnixMilli()) || (!paused && pause.End == at.UnixMilli()) {
			found = pause
			break
		}
	}
	if found.Start <= 0 {
		return
	}
	ctx := context.Background()
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		return
	}
	id := fmt.Sprintf("observation-pause:%d", found.Start)
	payload, err := json.Marshal(mainChatObservationPause{StartAt: found.Start, EndAt: found.End})
	if err != nil {
		return
	}
	row, err := a.store.LoadStreamItem(ctx, id)
	if err != nil {
		return
	}
	if row == nil {
		a.appendMainChatItem(ctx, main.LocalID, storage.StreamItemRow{ID: id, Kind: "system", ToolName: mainChatObservationPauseTool, ToolInput: string(payload), CreatedAt: found.Start}, "observation-pause")
		return
	}
	if row.SessionLocalID != main.LocalID || row.ToolName != mainChatObservationPauseTool || row.ToolInput == string(payload) {
		return
	}
	if a.store.UpdateStreamItemToolInput(ctx, id, string(payload)) != nil {
		return
	}
	a.mainChatRowsChanged(main.LocalID)
	a.emitBrowserEvent(mainChatItemsAppendedEvent, map[string]any{"sessionId": main.LocalID, "itemId": id, "sortOrder": row.SortOrder, "kind": "system", "source": "observation-pause"})
}
