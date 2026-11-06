package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/pii"
)

// Filters built concurrently under one key share the collected hashes and
// the goal purposes. Every filter must reach the same decision for the same
// reference, and the shared memo must be race-free (R24 part 2).
func TestInterestsReadFilterSharedCollectionsAreRaceFree(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	now := time.Now()
	searchText := strings.ToLower(topicText("silent speech emg wearable"))
	if _, err := app.activity.store.InsertBrowserVisits([]BrowserVisitRow{{
		URL:       "https://www.google.com/search?q=silent+speech+emg+wearable",
		Title:     "silent speech emg wearable - Google Search",
		VisitedAt: now.Add(-time.Hour).UnixMilli(),
		Browser:   "Chrome",
	}}); err != nil {
		t.Fatal(err)
	}
	searchRef := interests.Ref{Key: "search:" + interestHash(searchText), Source: interests.SourceSearch}
	unknownRef := interests.Ref{Key: "search:" + interestHash("a query never typed"), Source: interests.SourceSearch}

	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if !app.interestsReadFilter()(interests.Interest{Evidence: []interests.Ref{searchRef}}) {
					errs <- "a derivable search reference was held under concurrent filters"
					return
				}
				if app.interestsReadFilter()(interests.Interest{Evidence: []interests.Ref{unknownRef}}) {
					errs <- "an underivable search reference authorized replay under concurrent filters"
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}
