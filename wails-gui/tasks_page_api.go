package main

import (
	"context"
	"time"

	"wails-gui/internal/engagement"
)

// TaskSuggestionsFindMore is the answer to Find more in Tasks › Up next: the
// ideas after the search, and when the next search is allowed (RFC 3339,
// empty when there is no cooldown).
type TaskSuggestionsFindMore struct {
	Offers        []TaskSuggestionView `json:"offers"`
	NextAllowedAt string               `json:"nextAllowedAt,omitempty"`
}

// FindMoreTaskSuggestions runs the same search, with the same cooldown, as
// Find more on the phone. Wails binding.
func (a *App) FindMoreTaskSuggestions() (TaskSuggestionsFindMore, error) {
	result := TaskSuggestionsFindMore{Offers: []TaskSuggestionView{}}
	view, err := a.findMoreOffers(context.Background())
	if err != nil {
		return result, err
	}
	for _, offer := range view.Offers {
		if offer.Kind == "suggestion" {
			continue
		}
		result.Offers = append(result.Offers, taskSuggestionView(offer))
	}
	result.NextAllowedAt = view.NextAllowedAt
	return result, nil
}

// GetFirstWeekDay is the day of the person's first week with Isoform, counted
// in local calendar days from the first finished setup: 0 is the day setup
// finished. It is -1 before setup finishes, and for installs that never
// enrolled (upgrades). Pages use it for their first-day lines. Wails binding.
func (a *App) GetFirstWeekDay() int {
	if a == nil {
		return -1
	}
	now := time.Now()
	if a.engagement != nil {
		now = a.engagement.now()
	}
	return engagement.FirstWeekDay(a.firstWeekInstall(context.Background()), now)
}
