package main

import (
	"context"
	"encoding/json"

	"wails-gui/internal/engagement"
)

// mainChatBriefMissedProjector drops a "didn't run" status line once its
// brief ran on a later try (today-review v1.1.19 R42). The line's moment
// names the slot's brief; a stored brief with that id means the slot ran.
func (a *App) mainChatBriefMissedProjector(ctx context.Context) func(string, string) string {
	ran := map[string]bool{}
	made := func(momentID string) bool {
		if done, seen := ran[momentID]; seen {
			return done
		}
		done := false
		if a.store != nil {
			if row, err := a.store.GetEngagementMoment(ctx, momentID); err == nil && row != nil && row.Kind == string(engagement.KindBriefMissed) && row.SubjectID != "" {
				if service, ok := a.ensureMobileBriefService().(*mobileBriefService); ok && service != nil {
					_, done = service.store.load(row.SubjectID)
				}
			}
		}
		ran[momentID] = done
		return done
	}
	return func(tool, input string) string {
		if tool != mainChatStatusTool {
			return input
		}
		var status mainChatStatus
		if json.Unmarshal([]byte(input), &status) != nil {
			return input
		}
		entries := status.Entries[:0:0]
		for _, entry := range status.Entries {
			if entry.Kind == string(engagement.KindBriefMissed) && entry.MomentID != "" && made(entry.MomentID) {
				continue
			}
			entries = append(entries, entry)
		}
		if len(entries) == len(status.Entries) {
			return input
		}
		var data map[string]json.RawMessage
		if json.Unmarshal([]byte(input), &data) != nil {
			return input
		}
		var raw []json.RawMessage
		if json.Unmarshal(data["entries"], &raw) != nil || len(raw) != len(status.Entries) {
			return input
		}
		kept := raw[:0:0]
		for i, entry := range status.Entries {
			if entry.Kind == string(engagement.KindBriefMissed) && entry.MomentID != "" && made(entry.MomentID) {
				continue
			}
			kept = append(kept, raw[i])
		}
		data["entries"], _ = json.Marshal(kept)
		projected, err := json.Marshal(data)
		if err != nil {
			return input
		}
		return string(projected)
	}
}
