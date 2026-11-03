package main

import (
	"encoding/json"

	"wails-gui/internal/platform/activitymon"
)

func (c *ActivityTriager) triageGroupAllowsModelRead(group sessionGroup) bool {
	if c.app == nil || c.app.IsClosedEyesModeEnabled() || len(group.Sessions) == 0 {
		return false
	}
	for _, session := range group.Sessions {
		if c.app.activity != nil && c.app.activity.store != nil {
			if !c.app.activitySessionAllowsModelRead(c.app.activity.store, session) {
				return false
			}
			continue
		}
		// In-memory callers still obey the source metadata gate. Persisted
		// activity always takes the full session provenance check above.
		var apps []string
		if session.TriageStatus == "protected" || (session.AppNames != "" && json.Unmarshal([]byte(session.AppNames), &apps) != nil) {
			return false
		}
		for _, app := range append(apps, session.MeetingApp) {
			if activitySourceAppIsSensitive(app, app) {
				return false
			}
		}
	}
	return true
}

func redactTriageModelEvents(events []activitymon.ActivityEvent) []activitymon.ActivityEvent {
	filtered := make([]activitymon.ActivityEvent, 0, len(events))
	for _, event := range events {
		if activitySourceAppIsSensitive(event.AppBundleID, event.AppName) || activitySourceAppIsSensitive("", event.SpeechDictationApp) || activitymon.IsPrivateBrowsing(event.WindowTitle) {
			continue
		}
		event.WindowTitle = redactActivityModelText(event.WindowTitle)
		event.ClickLabel = redactActivityModelText(event.ClickLabel)
		event.FocusLabel = redactActivityModelText(event.FocusLabel)
		// TypedText is a stream of committed fragments. Redact it only after
		// flushTyping joins the fragments, or a matching prefix can be replaced
		// before the remaining secret arrives and leave that suffix exposed.
		event.SpeechText = redactActivityModelText(event.SpeechText)
		event.ClipboardPreview = redactActivityModelText(event.ClipboardPreview)
		filtered = append(filtered, event)
	}
	return filtered
}
