package main

import (
	"context"
	"encoding/json"
	"regexp"
)

// Capture notices predate observation pause rows. Their own moment identifies
// the original source and start; an end is shown only if the watcher recorded it.
func (a *App) mainChatCaptureProjector(ctx context.Context) func(string, string) string {
	loaded := false
	var intervals []engagementCaptureOutageInterval
	cache := map[string]mainChatObservationPause{}
	sources := map[string]string{}
	causes := map[string]string{}
	briefs := map[string]map[string]json.RawMessage{}
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
						var params map[string]string
						if json.Unmarshal([]byte(row.Params), &params) == nil {
							causes[id] = params["cause"]
						}
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
					if causes[id] != "" {
						value["captureCause"], _ = json.Marshal(causes[id])
					}
				}
				if pause.EndAt > 0 {
					value["captureEndAt"], _ = json.Marshal(pause.EndAt)
				}
			}
			if kind == "brief_missed" && id != "" && a.store != nil {
				for key, raw := range a.mainChatBriefMissedFields(ctx, id, briefs) {
					value[key] = raw
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

var mainChatBriefDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// mainChatBriefMissedFields gives a missed-brief line the brief's own day,
// slot, due time and recorded cause, so the line is worded from the brief's
// date rather than the day the notice was written (chat-review v1.0.10 H9).
// The day comes from the brief id ("2026-11-01-evening").
func (a *App) mainChatBriefMissedFields(ctx context.Context, id string, cache map[string]map[string]json.RawMessage) map[string]json.RawMessage {
	if fields, seen := cache[id]; seen {
		return fields
	}
	fields := map[string]json.RawMessage{}
	cache[id] = fields
	row, err := a.store.GetEngagementMoment(ctx, id)
	if err != nil || row == nil || row.Kind != "brief_missed" {
		return fields
	}
	day := mainChatBriefDay.FindString(row.SubjectID)
	var params map[string]string
	if day == "" || json.Unmarshal([]byte(row.Params), &params) != nil {
		return fields
	}
	fields["briefDay"], _ = json.Marshal(day)
	for key, param := range map[string]string{"briefSlot": "slot", "briefTime": "time", "briefCause": "reason"} {
		if params[param] != "" {
			fields[key], _ = json.Marshal(params[param])
		}
	}
	return fields
}
