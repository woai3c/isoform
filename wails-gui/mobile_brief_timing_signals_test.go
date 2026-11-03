package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func mobileBriefTimingSignalFixture(t *testing.T, coverage ...bool) (*App, *mobileBriefService, time.Time) {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	app := &App{store: store}
	loc := time.FixedZone("test-west", -7*60*60)
	now := time.Date(2026, 10, 17, 18, 0, 0, 0, loc)
	// Most fixtures model a reader installed before this history was observed.
	if len(coverage) == 0 || coverage[0] {
		if err := store.SetUIState(context.Background(), "mobile_brief_timing_privacy_since", fmt.Sprint(now.AddDate(0, 0, -16).UnixMilli())); err != nil {
			t.Fatal(err)
		}
	}
	return app, &mobileBriefService{app: app, location: loc}, now
}

func putMobileBriefTimingMarks(t *testing.T, app *App, first, last time.Time) {
	t.Helper()
	date := first.Format("2006-01-02")
	raw, err := json.Marshal(contextDayMarks{
		Date: date, FirstActive: first.UnixMilli(), LastActive: last.UnixMilli(),
		FirstInput: first.UnixMilli(), LastInput: last.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetUIState(context.Background(), contextDayMarksKey(date), string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestMobileBriefTimingSignalsReadFourteenLocalDaysWithoutInventingPhonePresence(t *testing.T) {
	app, service, now := mobileBriefTimingSignalFixture(t)
	for offset := -15; offset <= 1; offset++ {
		first := now.AddDate(0, 0, offset).Add(-9 * time.Hour)
		last := first.Add(8 * time.Hour)
		if offset == -2 {
			last = first // A single input cannot become a learned work day.
		}
		putMobileBriefTimingMarks(t, app, first, last)
	}
	got := service.timingInput(now)
	if len(got.Days) != 14 || got.Days[0].Date != "2026-10-03" || got.Days[13].Date != "2026-10-16" || got.Today.Date != "2026-10-17" {
		t.Fatalf("history boundaries = %+v", got)
	}
	for _, day := range got.Days {
		if day.Date == "2026-10-15" {
			if !day.FirstActivity.IsZero() || !day.LastActivity.IsZero() {
				t.Fatalf("single-input sample = %+v", day)
			}
		} else if day.FirstActivity.Hour() != 9 || day.LastActivity.Hour() != 17 {
			t.Fatalf("local clock sample = %+v", day)
		}
		if !day.FirstPhone.IsZero() {
			t.Fatalf("invented foreground phone use = %+v", day)
		}
	}
	if got.Today.FirstActivity.Hour() != 9 || got.Today.LastActivity.Hour() != 17 {
		t.Fatalf("today = %+v", got.Today)
	}
}

func TestMobileBriefTimingSignalsUseFirstActualOpenAndIgnoreFutureReceipts(t *testing.T) {
	app, service, now := mobileBriefTimingSignalFixture(t)
	for index, hoursAgo := range []int{9, 10, -1} {
		moment, err := app.store.CreateEngagementMoment(context.Background(), storage.EngagementMomentRow{Kind: "brief_ready", DedupeKey: fmt.Sprintf("brief-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		channel := "desktop"
		if index == 1 {
			channel = "push"
		}
		if err := app.store.RecordEngagementReceipt(context.Background(), storage.EngagementReceiptRow{
			MomentID: moment.ID, Channel: channel, State: storage.EngagementReceiptOpened, At: now.Add(-time.Duration(hoursAgo) * time.Hour).UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got := service.timingInput(now)
	if !got.Today.FirstOpen.Equal(now.Add(-10*time.Hour)) || !got.Today.FirstPhone.IsZero() {
		t.Fatalf("open signal = %+v", got.Today)
	}
}

func TestMobileBriefTimingSignalsFailClosedAtObservationGates(t *testing.T) {
	for _, gate := range []string{"closed_eyes", "screen_recording", "accessibility", "malformed_exclusions", "historical_pause"} {
		t.Run(gate, func(t *testing.T) {
			app, service, now := mobileBriefTimingSignalFixture(t)
			first, last := now.AddDate(0, 0, -1).Add(-9*time.Hour), now.AddDate(0, 0, -1)
			putMobileBriefTimingMarks(t, app, first, last)
			key, value := permissionUIStateKey(gate), "false"
			switch gate {
			case "closed_eyes":
				app.closedEyes.enabled.Store(true)
			case "malformed_exclusions":
				key, value = "sensitive_apps", "not-json"
			case "historical_pause":
				raw, _ := json.Marshal([]observationPause{{Start: first.UnixMilli(), End: last.UnixMilli()}})
				key, value = observationPausesKey, string(raw)
			}
			if err := app.store.SetUIState(context.Background(), key, value); err != nil {
				t.Fatal(err)
			}
			for _, day := range service.timingInput(now).Days {
				if !day.FirstActivity.IsZero() || !day.LastActivity.IsZero() {
					t.Fatalf("%s leaked timing = %+v", gate, day)
				}
			}
		})
	}
}

func TestMobileBriefTimingCalendarRequiresNativeGrantAndReadableSource(t *testing.T) {
	for _, gate := range []string{"allowed", "os_denied", "calendar_disabled", "calendar_hidden", "provider_hidden", "historical_pause"} {
		t.Run(gate, func(t *testing.T) {
			app, _, now := mobileBriefTimingSignalFixture(t)
			now = now.AddDate(0, 0, -1)
			from := now.Add(-18 * time.Hour)
			first, end := from.Add(9*time.Hour), from.Add(10*time.Hour)
			if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{{
				ID: "private-calendar-title", Title: "Content must never enter timing", Source: "eventkit", Provider: "google",
				StartAt: first.UnixMilli(), EndAt: end.UnixMilli(), Status: "confirmed", MyStatus: "accepted", Availability: "busy",
			}}, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			key, value := "", ""
			switch gate {
			case "calendar_disabled":
				key, value = permissionUIStateKey(calendarPermissionKey), "false"
			case "calendar_hidden":
				key, value = "sensitive_apps", `{"com.apple.iCal":"Calendar"}`
			case "provider_hidden":
				key, value = "sensitive_apps", `{"google":"Google"}`
			case "historical_pause":
				raw, _ := json.Marshal([]observationPause{{Start: first.UnixMilli(), End: end.UnixMilli()}})
				key, value = observationPausesKey, string(raw)
			}
			if key != "" {
				if err := app.store.SetUIState(context.Background(), key, value); err != nil {
					t.Fatal(err)
				}
			}
			privacy, err := app.mcpReadPrivacy(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			day := &mobileBriefTimingDay{Date: now.Format("2006-01-02")}
			collectMobileBriefCalendarTiming(context.Background(), app, privacy, from, from.AddDate(0, 0, 1), gate != "os_denied", map[string]*mobileBriefTimingDay{day.Date: day})
			if gate == "allowed" {
				if !day.FirstMeeting.Equal(first) || !day.LastMeeting.Equal(end) {
					t.Fatalf("authorized timing = %+v", day)
				}
			} else if !day.FirstMeeting.IsZero() || !day.LastMeeting.IsZero() {
				t.Fatalf("%s leaked calendar = %+v", gate, day)
			}
		})
	}
}

func TestMobileBriefTimingSignalsKeepQuietHoursAndSleepWhenSourcesUnavailable(t *testing.T) {
	quiet := engagement.QuietWindow{Start: 21 * time.Hour, End: 7 * time.Hour}
	app := &App{engagement: &engagementService{prefs: engagement.Prefs{Quiet: quiet}}}
	got := (&mobileBriefService{app: app, sleeping: true}).timingInput(time.Now())
	if got.Quiet != quiet || !got.Sleeping || len(got.Days) != 0 {
		t.Fatalf("source-unavailable safety gates = %+v", got)
	}
	defaults := (&mobileBriefService{app: &App{}}).timingInput(time.Now())
	if defaults.Quiet != engagement.DefaultPrefs().Quiet {
		t.Fatalf("default quiet hours = %+v", defaults.Quiet)
	}
	if got := (&mobileBriefService{}).timingInput(time.Now()); len(got.Days) != 0 || got.Quiet != (engagement.QuietWindow{}) {
		t.Fatalf("nil app fixture = %+v", got)
	}
}

func TestMobileBriefTimingSignalsReconcileLostSleepAndWake(t *testing.T) {
	app, service, now := mobileBriefTimingSignalFixture(t)
	app.contextSignals = &contextSignalService{state: contextState{Asleep: true}}
	service.sleeping, service.sleepAt = true, now.Add(-time.Hour)
	latest := activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: now.Add(-time.Minute)}
	service.sleepState = func() activitymon.SystemStateChange { return latest }
	if service.timingInput(now).Sleeping {
		t.Fatal("stale context state or a dropped wake kept adaptive delivery asleep")
	}
	service.sleeping, service.sleepAt = false, time.Time{}
	latest = activitymon.SystemStateChange{Kind: activitymon.SystemStateSleep, At: now.Add(-time.Minute)}
	if !service.timingInput(now).Sleeping {
		t.Fatal("a dropped sleep notification allowed adaptive delivery")
	}
	service.sleeping, service.sleepAt = true, now.Add(-time.Minute)
	latest = activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: now.Add(-time.Hour)}
	if !service.timingInput(now).Sleeping {
		t.Fatal("an old wake overrode a later observed sleep")
	}
	latest = activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: now.Add(time.Minute)}
	if !service.timingInput(now).Sleeping {
		t.Fatal("a future wake overrode observed sleep")
	}
}
