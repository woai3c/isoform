package main

import (
	"wails-gui/internal/engagement"
)

// The first-week reading is longer than a notification. "Your week" is not:
// its chat line is the catalog doorbell, and the week lives in the brief.
func populateEngagementJourneyCopy(item *mainChatProactive, m engagement.Moment, lang string) {
	if m.Kind != engagement.KindFirstWeek {
		return
	}
	for _, language := range engagement.Languages {
		copy := item.Localized[language]
		copy.Body = engagement.FirstWeekText(m.Params["stage"], m.Params["first_brief"], m.Params["history"], m.Params["name"], language)
		item.Localized[language] = copy
	}
	copy := item.Localized[normalizeUILanguage(lang)]
	item.Body, item.Headline = copy.Body, copy.Headline
}
