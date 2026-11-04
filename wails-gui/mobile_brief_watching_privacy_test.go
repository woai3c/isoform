package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestMobileBriefWatchingRechecksStoredBrowserSources(t *testing.T) {
	for _, reason := range []string{"sensitive browser", "historical pause", "closed eyes", "missing provenance", "unknown browser", "unavailable policy"} {
		t.Run(reason, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			previous := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
			pii.SetUserSensitiveApps(nil)
			now := time.Now().UTC().Truncate(time.Second)
			at := now.Add(-10 * time.Minute)
			rows := []BrowserVisitRow{
				{Browser: "Chrome", URL: "https://www.google.com/search", Title: "private research subject - Google Search", VisitedAt: at.UnixMilli()},
				{Browser: "Chrome", URL: "https://example.org/research", Title: "Research", VisitedAt: at.Add(time.Minute).UnixMilli()},
			}
			if _, err := app.activity.store.InsertBrowserVisits(rows); err != nil {
				t.Fatal(err)
			}
			in := mobileBriefInput{AsOf: now, WindowStart: now.Add(-time.Hour), Slot: mobileBriefSlotMorning, Location: time.UTC}
			collectMobileBriefWatching(app, &in)
			brief := buildMobileBriefV1(in)
			if len(brief.Sections.Watching) != 2 {
				t.Fatalf("missing Watching fixture: %+v", brief.Sections.Watching)
			}
			brief.NarrativeStatus = "ok"
			brief.Narrative = []MobileBriefPara{{Text: "private research synthesis", Refs: []string{brief.Evidence[0].Ref}}}
			brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
			t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
			svc := newMobileBriefService(app)
			svc.now = func() time.Time { return now }
			svc.location = time.UTC
			app.mobileBriefSvc = svc
			if err := svc.store.save(brief, now); err != nil {
				t.Fatal(err)
			}
			// Exercise JSON persistence and the Today projection before and after
			// browser retention. Neither may lose the original capture identity.
			for _, prune := range []bool{false, true} {
				if prune {
					if _, err := app.activity.store.PruneBrowserVisits(now.UnixMilli()); err != nil {
						t.Fatal(err)
					}
				}
				view, err := app.GetLatestBrief()
				if err != nil || view.Brief == nil || len(view.Brief.Sections.Watching) != 2 || view.Brief.Audio == nil {
					t.Fatalf("readable Watching lost after prune=%v: %+v, %v", prune, view, err)
				}
			}
			// Today's live refresh runs in the background; let it land first.
			svc.todayRefreshWG.Wait()
			switch reason {
			case "sensitive browser":
				pii.SetUserSensitiveApps(map[string]string{"com.google.Chrome": "Chrome"})
			case "historical pause":
				app.saveObservationPauses([]observationPause{{Start: at.UnixMilli(), End: at.Add(time.Second).UnixMilli()}})
			case "closed eyes":
				app.closedEyes.enabled.Store(true)
			case "missing provenance", "unknown browser":
				for i := range brief.Evidence {
					brief.Evidence[i].WebSource = nil
					if reason == "unknown browser" {
						brief.Evidence[i].WebSource = &MobileBriefWebSource{Browsers: []string{"unknown"}, StartMs: at.UnixMilli(), EndMs: now.UnixMilli()}
					}
				}
				if err := svc.store.save(brief, now); err != nil {
					t.Fatal(err)
				}
			case "unavailable policy":
				if err := app.store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			assertHeld := func(got MobileBriefV1) {
				t.Helper()
				data, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Sections.Watching) != 0 || len(got.Narrative) != 0 || got.Audio != nil || strings.Contains(string(data), "private research") || strings.Contains(string(data), "example.org") {
					t.Fatalf("held web source reached delivery: %s", data)
				}
			}
			view, err := app.GetLatestBrief()
			if err != nil || view.Brief == nil {
				t.Fatalf("missing Today document: %+v, %v", view, err)
			}
			assertHeld(*view.Brief)
			delivered, found := svc.GetMobileBrief(brief.BriefID)
			if !found {
				t.Fatal("missing stored brief")
			}
			assertHeld(delivered)
			stored, found := svc.store.load(brief.BriefID)
			if !found || len(stored.Sections.Watching) != 2 {
				t.Fatal("read gate destroyed the local stored document")
			}
		})
	}
}

func TestMobileBriefWatchingCollectionUsesCurrentReadPolicy(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.google.Chrome": "Chrome"})
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	rows := []BrowserVisitRow{
		{Browser: "Chrome", URL: "https://google.com/search", Title: "protected query - Google Search", VisitedAt: at.UnixMilli()},
		{Browser: "Brave", URL: "https://google.com/search", Title: "paused query - Google Search", VisitedAt: at.Add(time.Minute).UnixMilli()},
		{Browser: "Brave", URL: "https://example.org/private", Title: "Private Browsing", VisitedAt: at.Add(2 * time.Minute).UnixMilli()},
		{Browser: "unknown", URL: "https://example.org/unknown", VisitedAt: at.Add(3 * time.Minute).UnixMilli()},
		{Browser: "Brave", URL: "https://google.com/search", Title: "safe query - Google Search", VisitedAt: at.Add(4 * time.Minute).UnixMilli()},
	}
	if _, err := app.activity.store.InsertBrowserVisits(rows); err != nil {
		t.Fatal(err)
	}
	app.saveObservationPauses([]observationPause{{Start: at.Add(time.Minute).UnixMilli(), End: at.Add(2 * time.Minute).UnixMilli()}})
	in := mobileBriefInput{WindowStart: at.Add(-time.Second), AsOf: at.Add(5 * time.Minute)}
	collectMobileBriefWatching(app, &in)
	if len(in.Watching) != 1 || in.Watching[0].Title != "safe query" || !app.mobileBriefWebSourceAllowsRead(in.Watching[0].WebSource) {
		t.Fatalf("collector did not apply the source policy: %+v", in.Watching)
	}
	app.closedEyes.enabled.Store(true)
	in.Watching = nil
	collectMobileBriefWatching(app, &in)
	if len(in.Watching) != 0 {
		t.Fatal("Closed Eyes collected web evidence")
	}
}
