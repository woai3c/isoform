package main

import (
	"testing"
	"time"
)

func TestTodayGetLatestBriefPreservesOngoingMeetingEndAndJoinLink(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	generatedAt := mobileBriefTestDay(7, 0)
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(14, 5)}
	svc, gathers := newTestMobileBriefService(t, clock)
	svc.app, app.mobileBriefSvc = app, svc
	event := prepEvent("ongoing-review", "Design review", mobileBriefTestDay(14, 0), 60, "Zoom", 1)
	storeEvent(t, app.store, event)
	in := mobileBriefInput{
		Slot: mobileBriefSlotMorning, AsOf: generatedAt,
		WindowStart: generatedAt.Add(-time.Hour), Location: time.UTC,
	}
	collectMobileBriefCalendar(app, &in)
	original := buildMobileBriefV1(in)
	if err := svc.store.save(original, generatedAt); err != nil {
		t.Fatal(err)
	}
	meeting := func(brief MobileBriefV1) MobileBriefItem {
		t.Helper()
		for _, item := range brief.Sections.Meetings {
			if item.Title == event.Title {
				return item
			}
		}
		t.Fatalf("ongoing meeting missing from brief: %+v", brief.Sections.Meetings)
		return MobileBriefItem{}
	}
	if got := meeting(original); got.EndAt != formatMobileBriefTime(event.End) {
		t.Fatalf("stored fixture is missing its meeting end: %+v", got)
	}

	// Serving the stored morning brief at 14:05 performs the live projection.
	// The frontend keeps a meeting until EndAt and offers Join for its URL.
	for read := 0; read < 2; read++ {
		view, err := app.GetLatestBrief()
		if err != nil || view.Brief == nil {
			t.Fatalf("served brief = %+v, %v", view, err)
		}
		got := meeting(*view.Brief)
		if got.At != formatMobileBriefTime(event.Start) || got.EndAt != formatMobileBriefTime(event.End) {
			t.Fatalf("live refresh lost the ongoing meeting interval at 14:05: %+v", got)
		}
		if !mobileBriefParseTime(got.At).Before(clock.Now()) || !mobileBriefParseTime(got.EndAt).After(clock.Now()) || got.Status == "done" || got.URL != event.ConferenceURL {
			t.Fatalf("served ongoing meeting no longer supports Join: %+v", got)
		}
		if view.Brief.BriefID != original.BriefID || view.Brief.AsOf != original.AsOf || gathers.Load() != 0 {
			t.Fatal("serving replaced the stored brief with a new generation")
		}
		stored, ok := svc.store.load(original.BriefID)
		if !ok || meeting(stored).EndAt != formatMobileBriefTime(event.End) {
			t.Fatal("live refresh did not preserve the meeting end in the stored brief")
		}
	}
}
