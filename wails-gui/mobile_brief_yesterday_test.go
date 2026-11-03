package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/pii"
)

func TestMobileBriefYesterdayActivityRechecksSources(t *testing.T) {
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	app, _, _ := yourWeekFrozenWindowFixture(t, []time.Weekday{time.Monday}, "2026-10-19", intPointer(1), 1, nil)
	asOf := time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)
	snap := app.mobileBriefYesterdayActivity(context.Background(), asOf, time.UTC)
	if snap == nil || snap.StartDate != "2026-10-19" || snap.CutoffMs != time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("yesterday snapshot = %+v", snap)
	}
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	// Today review v1.1.6 reopens the exact calendar occurrence on delivery.
	storeEvent(t, app.store, calendar.Event{ID: "calendar-fixture", Title: "A meeting", Start: asOf.Add(time.Hour), End: asOf.Add(2 * time.Hour)})
	readable := app.yourWeekReadable(context.Background(), *snap, start, asOf)
	if len(readable.Topics) != 1 || readable.Topics[0].ID != "t-halcyon" || readable.Topics[0].Minutes != 6*60 {
		t.Fatalf("yesterday topics = %+v", readable.Topics)
	}
	brief := MobileBriefV1{AsOf: formatMobileBriefTime(asOf), Timezone: "UTC", NarrativeStatus: "ok",
		Evidence:  []MobileBriefRef{{Ref: "e1", SourceKind: "calendar", SourceID: "calendar-fixture", Label: "A meeting"}},
		Narrative: []MobileBriefPara{{Text: "The project took six hours yesterday.", Refs: []string{"e1"}}}}
	binding := &mobileBriefModelBinding{}
	if topics, ok := app.bindMobileBriefYesterday(context.Background(), brief, binding, snap); !ok || len(topics) != 1 {
		t.Fatalf("could not bind yesterday topics: %+v", topics)
	}
	if !app.mobileBriefYesterdayBindingReadable(context.Background(), brief, binding) {
		t.Fatal("unchanged source binding was rejected")
	}
	app.closedEyes.enabled.Store(true)
	pausedCtx := context.WithValue(context.Background(), pausedBriefReadKey{}, true)
	if !app.mobileBriefYesterdayBindingReadable(pausedCtx, brief, binding) {
		t.Fatal("paused selected-brief proof could not recheck yesterday's activity")
	}
	if app.mobileBriefYesterdayBindingReadable(context.Background(), brief, binding) {
		t.Fatal("ordinary paused model read exposed yesterday's activity")
	}
	app.closedEyes.enabled.Store(false)
	wrongDate := brief
	wrongDate.AsOf = formatMobileBriefTime(asOf.AddDate(0, 0, 1))
	if app.mobileBriefYesterdayBindingReadable(context.Background(), wrongDate, binding) {
		t.Fatal("snapshot was accepted for a different morning")
	}
	brief.modelBinding = binding
	if delivered, withheld := app.mobileBriefDelivery(brief); len(delivered.Narrative) != 1 || withheld.Reason != "" {
		t.Fatalf("unchanged synthesis withheld: %+v, %+v", delivered.Narrative, withheld)
	}
	app.closedEyes.enabled.Store(true)
	if delivered, withheld := app.mobileBriefDeliveryContext(pausedCtx, brief); len(delivered.Narrative) != 1 || withheld.Reason != "" {
		t.Fatalf("paused selected-brief synthesis withheld: %+v, %+v", delivered.Narrative, withheld)
	}
	if delivered, _ := app.mobileBriefDelivery(brief); len(delivered.Narrative) != 0 {
		t.Fatal("ordinary paused brief delivery exposed activity-derived synthesis")
	}
	app.closedEyes.enabled.Store(false)
	pii.SetUserSensitiveApps(map[string]string{"com.example.finder": "Finder"})
	readable = app.yourWeekReadable(context.Background(), *snap, start, asOf)
	if len(readable.Topics) != 0 {
		t.Fatalf("withdrawn activity remained in yesterday topics: %+v", readable.Topics)
	}
	if app.mobileBriefYesterdayBindingReadable(context.Background(), brief, binding) {
		t.Fatal("withdrawn activity remained valid for model synthesis")
	}
	app.closedEyes.enabled.Store(true)
	if app.mobileBriefYesterdayBindingReadable(pausedCtx, brief, binding) {
		t.Fatal("withdrawn activity remained valid for a paused brief")
	}
	app.closedEyes.enabled.Store(false)
	if delivered, withheld := app.mobileBriefDelivery(brief); len(delivered.Narrative) != 0 || withheld.SourceKind != "activity" {
		t.Fatalf("withdrawn synthesis delivered: %+v, %+v", delivered.Narrative, withheld)
	}
}
