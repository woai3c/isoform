package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

const (
	taskOnsetObservationHorizon = 3 * time.Minute
	taskOnsetEvidenceTextLimit  = 320
)

func truncateTaskOnsetText(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	redacted, _ := pii.RedactText(value)
	if len(redacted) <= taskOnsetEvidenceTextLimit {
		return redacted
	}
	return truncateAtRuneBoundary(redacted, taskOnsetEvidenceTextLimit) + "..."
}

func summarizeTaskOnsetEvent(event activitymon.ActivityEvent) string {
	app := truncateTaskOnsetText(event.AppName)
	window := truncateTaskOnsetText(event.WindowTitle)
	where := app
	if window != "" {
		if where != "" {
			where += " / "
		}
		where += window
	}
	if where == "" {
		where = "the active application"
	}

	switch event.Kind {
	case activitymon.EventAppSwitch:
		return "Switched to " + where
	case activitymon.EventClick:
		label := truncateTaskOnsetText(event.ClickLabel)
		role := truncateTaskOnsetText(event.ClickRole)
		target := strings.TrimSpace(strings.Join([]string{role, label}, " "))
		if target == "" {
			target = "an interface control"
		}
		return fmt.Sprintf("Clicked %s in %s", target, where)
	case activitymon.EventFocus:
		label := truncateTaskOnsetText(event.FocusLabel)
		role := truncateTaskOnsetText(event.FocusRole)
		target := strings.TrimSpace(strings.Join([]string{role, label}, " "))
		if target == "" {
			target = "a work surface"
		}
		return fmt.Sprintf("Focused %s in %s", target, where)
	case activitymon.EventKeyboard:
		typed := truncateTaskOnsetText(event.TypedText)
		if typed == "" {
			return "Entered text in " + where
		}
		return fmt.Sprintf("Entered text in %s: %q", where, typed)
	case activitymon.EventSpeech:
		speech := truncateTaskOnsetText(event.SpeechText)
		if speech == "" {
			return ""
		}
		source := "Speech"
		switch event.SpeechSource {
		case "microphone":
			source = "User speech"
		case "system_audio":
			source = "Other-party speech"
		}
		return fmt.Sprintf("%s in %s: %q", source, where, speech)
	default:
		return ""
	}
}

func buildTaskOnsetEvidenceFromEvents(
	events []activitymon.ActivityEvent,
	windowStart int64,
	cutoffAt int64,
) []TaskOnsetEvidence {
	sorted := append([]activitymon.ActivityEvent(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp < sorted[j].Timestamp
	})

	result := make([]TaskOnsetEvidence, 0, min(len(sorted), taskOnsetMaxEvidence))
	previousSummary := ""
	for index, event := range sorted {
		if event.Timestamp < windowStart || event.Timestamp > cutoffAt {
			continue
		}
		summary := summarizeTaskOnsetEvent(event)
		if summary == "" || summary == previousSummary {
			continue
		}
		previousSummary = summary
		result = append(result, TaskOnsetEvidence{
			Ref:        fmt.Sprintf("event:%d:%s:%d", event.Timestamp, event.Kind, index),
			Kind:       string(event.Kind),
			SourceKind: "activity_event",
			SourceID:   fmt.Sprintf("%d", index),
			OccurredAt: event.Timestamp,
			AppKey:     normalizeWorkPatternAppKey(event.AppBundleID, event.AppName),
			AppName:    workPatternDisplayAppName(event.AppName),
			Summary:    summary,
		})
		if len(result) == taskOnsetMaxEvidence {
			break
		}
	}
	return result
}

func (c *ActivityTriager) buildTaskOnsetInput(group sessionGroup) (TaskOnsetInput, error) {
	windowStart := group.startTime()
	cutoffAt := windowStart + taskOnsetObservationHorizon.Milliseconds()
	if cutoffAt > group.endTime() {
		cutoffAt = group.endTime()
	}

	var events []activitymon.ActivityEvent
	for _, session := range group.Sessions {
		sessionEvents, err := c.app.activity.store.GetSessionEvents(session.ID)
		if err != nil {
			return TaskOnsetInput{}, fmt.Errorf(
				"load onset events for session %s: %w",
				session.ID,
				err,
			)
		}
		events = append(events, sessionEvents...)
	}

	return normalizeTaskOnsetInput(TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    group.primaryID(),
		WindowStart: windowStart,
		CutoffAt:    cutoffAt,
		Evidence:    buildTaskOnsetEvidenceFromEvents(events, windowStart, cutoffAt),
	})
}
