package main

import (
	"testing"
	"time"
)

func TestFindMoreTaskSuggestionsSharesThePhoneSearch(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	svc := bridge.app.topicsSvc()
	now := time.Now()
	svc.now = func() time.Time { return now }
	oldReady, oldDispatch := topicFindsProviderReady, topicFindsDispatch
	t.Cleanup(func() { topicFindsProviderReady, topicFindsDispatch = oldReady, oldDispatch })
	topicFindsProviderReady = func(*App) bool { t.Fatal("cooldown find checked the model"); return false }
	topicFindsDispatch = func(*App, string) error { t.Fatal("cooldown find called the model"); return nil }
	if _, err := svc.store.update(func(st *topicsStateFile) { st.Finds.RanOn = now.In(time.Local).Format("2006-01-02") }); err != nil {
		t.Fatal(err)
	}
	svc.lastStart = now
	result, err := bridge.app.FindMoreTaskSuggestions()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Offers) == 0 {
		t.Fatal("cached offers missing")
	}
	for _, offer := range result.Offers {
		if offer.Kind == "suggestion" {
			t.Fatalf("legacy suggestion returned: %+v", offer)
		}
		if offer.ID == "" || offer.Title == "" || offer.Version == "" {
			t.Fatalf("offer lost fields: %+v", offer)
		}
	}
	until, err := time.Parse(time.RFC3339, result.NextAllowedAt)
	if err != nil || !until.After(now) {
		t.Fatalf("nextAllowedAt = %q, %v", result.NextAllowedAt, err)
	}
}

func TestFindMoreTaskSuggestionsEmptyAndUnavailable(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	bridge.app.closedEyes.enabled.Store(true)
	result, err := bridge.app.FindMoreTaskSuggestions()
	if err != nil {
		t.Fatal(err)
	}
	if result.Offers == nil || len(result.Offers) != 0 {
		t.Fatalf("closed eyes must answer an empty list, got %+v", result)
	}
	bridge.app.store = nil
	if _, err := bridge.app.FindMoreTaskSuggestions(); err == nil {
		t.Fatal("no store must be an error")
	}
}

func TestGetFirstWeekDay(t *testing.T) {
	var nilApp *App
	if day := nilApp.GetFirstWeekDay(); day != -1 {
		t.Fatalf("nil app: %d", day)
	}
	app, rig := newJourneyRig(t)
	if day := app.GetFirstWeekDay(); day != -1 {
		t.Fatalf("before setup: %d", day)
	}
	finishJourneyOnboarding(t, app, rig.clock)
	if day := app.GetFirstWeekDay(); day != 0 {
		t.Fatalf("setup day: %d", day)
	}
	rig.clock = rig.clock.AddDate(0, 0, 2)
	if day := app.GetFirstWeekDay(); day != 2 {
		t.Fatalf("two days later: %d", day)
	}
}
