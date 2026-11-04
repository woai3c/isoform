package main

import (
	"encoding/json"
	"time"
)

// Only non-sensitive title metadata is projected; narrative and actions still
// pass through the normal brief reader's current privacy checks.
type mainChatBriefDisplay struct {
	Slot     string `json:"slot"`
	AsOf     string `json:"asOf"`
	Timezone string `json:"timezone"`
}

func mainChatBriefDisplayFor(brief MobileBriefV1, state upcomingBriefState, loc *time.Location) mainChatBriefDisplay {
	slot := brief.Slot
	if slot != "morning" && slot != "evening" {
		slot = brief.DisplaySlot
	}
	if slot != "morning" && slot != "evening" {
		slot = "on_request"
	}
	result := mainChatBriefDisplay{Slot: slot, AsOf: brief.AsOf, Timezone: brief.Timezone}
	if slot == "on_request" {
		return result
	}
	// Use the same historical correction and late rule as Tasks. Limit the
	// input to this brief so its run is not lost behind the three-run window.
	runs := state.Runs
	state.Runs = nil
	for _, run := range runs {
		if run.BriefID == brief.BriefID && run.Status == mobileBriefRunOK {
			state.Runs = append(state.Runs, run)
		}
	}
	state.MadeOnRequest = nil
	state.Settings.Enabled = false
	for _, run := range briefScheduleDetailFrom(time.Now(), loc, state, slot).Runs {
		if run.BriefID != brief.BriefID || run.Status != mobileBriefRunOK {
			continue
		}
		if run.Late && run.At != "" {
			result.AsOf = run.At
		} else if run.ScheduledAt != "" {
			result.AsOf = run.ScheduledAt
		}
		break
	}
	return result
}

func (a *App) mainChatBriefDisplayProjector() func(string, string) string {
	var service *mobileBriefService
	serviceChecked := false
	var state upcomingBriefState
	loaded := false
	cache := map[string]*mainChatBriefDisplay{}
	return func(tool, input string) string {
		if tool != mainChatProactiveTool {
			return input
		}
		var card map[string]json.RawMessage
		if json.Unmarshal([]byte(input), &card) != nil {
			return input
		}
		var enrich func(map[string]json.RawMessage)
		enrich = func(item map[string]json.RawMessage) {
			var briefID, kind string
			var evidence []json.RawMessage
			var target mainChatTarget
			_ = json.Unmarshal(item["briefId"], &briefID)
			_ = json.Unmarshal(item["kind"], &kind)
			_ = json.Unmarshal(item["evidence"], &evidence)
			_ = json.Unmarshal(item["target"], &target)
			// A withdrawn ordinary card loses its evidence. A weekly recap's
			// sanitized Today target may retain only its own brief locator.
			allowed := len(evidence) > 0 || (kind == "weekly_recap" && target.Kind == "today" && target.ID == briefID)
			if briefID != "" && allowed && (kind == "brief_ready" || kind == "weekly_recap" || kind == "first_week") {
				if !serviceChecked {
					service, _ = a.ensureMobileBriefService().(*mobileBriefService)
					serviceChecked = true
				}
				if service == nil || service.store == nil {
					return
				}
				display, seen := cache[briefID]
				if !seen {
					if brief, found := service.store.load(briefID); found {
						if !loaded && service.config != nil && service.runs != nil {
							state = upcomingBriefState{Settings: service.config.load(), Runs: service.runs.list()}
							loaded = true
						}
						briefState := state
						briefState.Runs = append([]mobileBriefRun(nil), state.Runs...)
						if brief.Generation != nil && brief.Generation.ScheduledAt != "" {
							if _, err := time.Parse(time.RFC3339Nano, brief.Generation.ScheduledAt); err == nil {
								for i := range briefState.Runs {
									if briefState.Runs[i].BriefID == brief.BriefID {
										briefState.Runs[i].ScheduledAt = brief.Generation.ScheduledAt
									}
								}
							}
						}
						value := mainChatBriefDisplayFor(brief, briefState, service.location)
						display = &value
					}
					cache[briefID] = display
				}
				if display != nil {
					item["briefDisplay"], _ = json.Marshal(display)
				}
			}
			var children []json.RawMessage
			if json.Unmarshal(item["items"], &children) == nil {
				for i, child := range children {
					var object map[string]json.RawMessage
					if json.Unmarshal(child, &object) == nil && object != nil {
						enrich(object)
						children[i], _ = json.Marshal(object)
					}
				}
				item["items"], _ = json.Marshal(children)
			}
		}
		enrich(card)
		encoded, err := json.Marshal(card)
		if err != nil {
			return input
		}
		return string(encoded)
	}
}
