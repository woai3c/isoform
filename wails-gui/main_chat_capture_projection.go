package main

import (
	"context"
	"encoding/json"
)

// Capture notices predate observation pause rows. Their own moment identifies
// the original source and start; an end is shown only if the watcher recorded it.
func (a *App) mainChatCaptureProjector(ctx context.Context) func(string, string) string {
	loaded := false
	var intervals []engagementCaptureOutageInterval
	cache := map[string]mainChatObservationPause{}
	sources := map[string]string{}
	return func(tool, input string) string {
		if tool != mainChatStatusTool && tool != mainChatProactiveTool {
			return input
		}
		var data map[string]json.RawMessage
		if json.Unmarshal([]byte(input), &data) != nil {
			return input
		}
		var enrich func(map[string]json.RawMessage)
		enrich = func(value map[string]json.RawMessage) {
			var kind, id string
			_ = json.Unmarshal(value["kind"], &kind)
			_ = json.Unmarshal(value["momentId"], &id)
			if kind == "capture_paused" && id != "" && a.store != nil {
				pause, seen := cache[id]
				if !seen {
					row, err := a.store.GetEngagementMoment(ctx, id)
					if err == nil && row != nil && row.Kind == "capture_paused" && row.CreatedAt > 0 {
						pause.StartAt = row.CreatedAt
						sources[id] = row.SubjectID
						if !loaded {
							intervals, _ = a.captureOutageIntervals(ctx)
							loaded = true
						}
						for _, interval := range intervals {
							if interval.Source == row.SubjectID && interval.Start == row.CreatedAt {
								pause.EndAt = interval.End
								break
							}
						}
					}
					cache[id] = pause
				}
				if pause.StartAt > 0 {
					value["captureStartAt"], _ = json.Marshal(pause.StartAt)
					value["captureSource"], _ = json.Marshal(sources[id])
				}
				if pause.EndAt > 0 {
					value["captureEndAt"], _ = json.Marshal(pause.EndAt)
				}
			}
			for _, key := range []string{"entries", "items"} {
				var children []map[string]json.RawMessage
				if json.Unmarshal(value[key], &children) != nil {
					continue
				}
				for _, child := range children {
					enrich(child)
				}
				value[key], _ = json.Marshal(children)
			}
		}
		enrich(data)
		projected, err := json.Marshal(data)
		if err != nil {
			return input
		}
		return string(projected)
	}
}
