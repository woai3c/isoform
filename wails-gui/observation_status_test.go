package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

func TestDoingNowObservationFrontAppPrivacy(t *testing.T) {
	old := pii.GetUserSensitiveApps()
	pii.SetUserSensitiveApps(map[string]string{"test.private": "Private App"})
	t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
	for _, tc := range []struct {
		name, bundle, title string
		hidden              bool
	}{
		{"Editor", "test.editor", "Secret window title", false},
		{"1Password", "com.1password.desktop", "Secret window title", true},
		{"Private App", "test.private.desktop", "Secret window title", true},
		{"Browser", "com.google.Chrome", "Secret window title - Incognito", true},
		{"loginwindow", "com.apple.loginwindow", "Secret window title", true},
	} {
		for _, kind := range []doingNowKind{doingNowWatching, doingNowWorking} {
			got := observationDoingNow(DoingNow{Kind: kind, Title: "Existing task"}, activitymon.AppInfo{AppName: tc.name, BundleID: tc.bundle, WindowTitle: tc.title}, BackgroundAudioStatus{}, false)
			want := tc.name
			if tc.hidden {
				want = ""
			}
			if got.FrontApp != want || got.FrontAppHidden != tc.hidden {
				t.Fatalf("%s/%s: %+v", tc.name, kind, got)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "Secret window title") {
				t.Fatal("window title escaped")
			}
			if got.Title != "Existing task" {
				t.Fatal("existing task title changed")
			}
		}
	}
	got := observationDoingNow(DoingNow{Kind: doingNowListening}, activitymon.AppInfo{AppName: "Editor"}, BackgroundAudioStatus{}, false)
	if got.FrontApp != "" || got.FrontAppHidden {
		t.Fatalf("listening: %+v", got)
	}
}

func TestDoingNowObservationMicrophoneAndCall(t *testing.T) {
	for _, tc := range []struct {
		name  string
		audio BackgroundAudioStatus
		kind  doingNowKind
		call  bool
		mic   string
	}{
		{"off", BackgroundAudioStatus{}, doingNowIdle, false, "off"},
		{"mic", BackgroundAudioStatus{Running: true, ActiveSources: []string{"microphone"}}, doingNowWatching, true, "listening"},
		{"system only", BackgroundAudioStatus{Running: true, ActiveSources: []string{"system"}}, doingNowWatching, false, "off"},
		{"switch paused", BackgroundAudioStatus{Running: true, MicrophonePaused: true, ActiveSources: []string{"system"}}, doingNowMeeting, true, "paused"},
		{"locked", BackgroundAudioStatus{ScreenLocked: true}, doingNowIdle, false, "paused"},
		{"closed eyes", BackgroundAudioStatus{Running: true, ActiveSources: []string{"microphone"}}, doingNowPaused, false, "paused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := observationDoingNow(DoingNow{Kind: tc.kind}, activitymon.AppInfo{}, tc.audio, tc.call)
			if got.Microphone != tc.mic || got.InCall != tc.call {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestDoingNowObservationPauseDeadlineAndEvent(t *testing.T) {
	app := newTimedPauseFixture(t)
	if err := app.PauseObservationFor(15); err != nil {
		t.Fatal(err)
	}
	deadline, _ := app.observationResumeAt()
	if got := app.GetDoingNow(); got.PausedUntil != deadline.UnixMilli() || got.Kind != doingNowPaused || got.Microphone != "paused" || got.FrontApp != "" {
		t.Fatalf("timed: %+v", got)
	}
	app.eventBroadcaster = NewEventBroadcaster()
	events := app.eventBroadcaster.AddClient(t.Name())
	defer app.eventBroadcaster.RemoveClient(t.Name())
	app.refreshDoingNow()
	select {
	case raw := <-events:
		var event struct {
			Event string     `json:"event"`
			Args  []DoingNow `json:"args"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != doingNowChangedEvent || len(event.Args) != 1 || event.Args[0].PausedUntil != deadline.UnixMilli() {
			t.Fatalf("event: %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing event")
	}
	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := app.GetDoingNow(); got.PausedUntil != 0 || got.Kind != doingNowPaused {
		t.Fatalf("manual: %+v", got)
	}
	if err := app.ResumeObservation(); err != nil {
		t.Fatal(err)
	}
	if got := app.GetDoingNow(); got.PausedUntil != 0 {
		t.Fatalf("resumed: %+v", got)
	}
}

func TestObservationSuggestionsFiltersAndCap(t *testing.T) {
	old := pii.GetUserSensitiveApps()
	pii.SetUserSensitiveApps(map[string]string{"test.private": "Private App", "test.alias": "Private Name"})
	t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
	apps := []AppUsageStat{
		{AppName: "Bank", BundleID: "test.finance", ActiveMs: 1000},
		{AppName: "Messages", BundleID: "com.apple.MobileSMS", ActiveMs: 900},
		{AppName: "Private App", BundleID: "test.private", ActiveMs: 9000},
		{AppName: "Passwords", BundleID: "test.unknown", ActiveMs: 9000},
		{AppName: "Other Name", BundleID: "com.1password.desktop", ActiveMs: 9000},
		{AppName: "Private Name", BundleID: "test.other", ActiveMs: 9000},
		{AppName: "loginwindow", BundleID: "com.apple.loginwindow", ActiveMs: 9000},
		{AppName: "Isoform", BundleID: "com.wails.Isoform", ActiveMs: 9000},
		{AppName: "Ordinary", BundleID: "test.editor", ActiveMs: 9000},
	}
	categories := map[string]string{}
	for _, app := range apps {
		if app.BundleID != "test.editor" {
			categories[strings.ToLower(app.BundleID)] = "finance"
		}
	}
	got := selectPrivateAppSuggestions(apps, categories)
	if len(got) != 2 || got[0].Name != "Bank" || got[1].Category != "messaging" {
		t.Fatalf("filtered: %+v", got)
	}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("test.health.%d", i)
		apps = append(apps, AppUsageStat{AppName: id, BundleID: id, ActiveMs: int64(800 - i)})
		categories[id] = "health"
	}
	got = selectPrivateAppSuggestions(apps, categories)
	if len(got) != 5 || got[4].BundleID != "test.health.2" || got[4].Category != "health" {
		t.Fatalf("capped: %+v", got)
	}
	if got := selectPrivateAppSuggestions(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty: %+v", got)
	}
}

func observationSuggestionFixture(t *testing.T) (*App, time.Time) {
	t.Helper()
	app := newTimedPauseFixture(t)
	app.activity = &activityMonitorState{store: newTestStore(t)}
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.Local)
	for i, seed := range []struct {
		id, name, bundle, title, status string
		private                         bool
	}{
		{"bank", "Bank", "test.finance", "Balance", "pending", false},
		{"messages", "Messages", "com.apple.MobileSMS", "Conversation", "pending", false},
		{"protected", "Protected Bank", "test.protected", "Balance", "protected", false},
		{"incognito", "Private Bank", "test.incognito", "Balance - Incognito", "pending", true},
		{"paused", "Paused Bank", "test.paused", "Balance", "pending", false},
	} {
		at := now.Add(time.Duration(-60+i*10) * time.Minute).UnixMilli()
		events := []activitymon.ActivityEvent{
			{Timestamp: at, Kind: activitymon.EventAppSwitch, AppName: seed.name, AppBundleID: seed.bundle, WindowTitle: seed.title, PrivateWindow: seed.private},
			{Timestamp: at + 60000, Kind: activitymon.EventAppSwitch, AppName: "Editor", AppBundleID: "test.editor"},
		}
		row := ActivitySessionRow{ID: seed.id, StartTime: at, EndTime: at + 120000, AppNames: `[]`, TriageStatus: seed.status, CreatedAt: at}
		if err := app.activity.store.SaveSession(row, events, nil); err != nil {
			t.Fatal(err)
		}
		if seed.id == "paused" {
			raw, _ := json.Marshal([]observationPause{{Start: at, End: at + 120000}})
			if err := app.store.SetUIState(context.Background(), observationPausesKey, string(raw)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return app, now
}

func TestObservationSuggestionsSourceGatesAndDailyCache(t *testing.T) {
	app, now := observationSuggestionFixture(t)
	calls, reads := 0, 0
	enumerate := func() []SystemApp {
		calls++
		return []SystemApp{{BundleID: "test.finance"}, {BundleID: "test.protected"}, {BundleID: "test.incognito"}, {BundleID: "test.paused"}}
	}
	category := func(SystemApp) string { reads++; return "finance" }
	for i := 0; i < 2; i++ {
		got, err := app.privateAppSuggestions(now, enumerate, category)
		if err != nil || len(got) != 2 || got[0].ActiveMs != 60000 || got[1].ActiveMs != 60000 {
			t.Fatalf("%+v, %v", got, err)
		}
	}
	if calls != 1 || reads != 4 {
		t.Fatalf("cache: scans %d reads %d", calls, reads)
	}
	app.observationCategories.dayStart--
	if _, err := app.privateAppSuggestions(now, enumerate, category); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("day rollover did not expire cache")
	}
	old := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
	pii.AddUserSensitiveApp("test.finance", "Bank")
	got, err := app.privateAppSuggestions(now, enumerate, category)
	if err != nil || len(got) != 1 || got[0].Category != "messaging" {
		t.Fatalf("live policy: %+v %v", got, err)
	}
	app.closedEyes.enabled.Store(true)
	got, err = app.privateAppSuggestions(now, enumerate, category)
	if err != nil || got == nil || len(got) != 0 || calls != 2 {
		t.Fatalf("paused: %+v %v", got, err)
	}
}

func TestObservationSuggestionsRecheckAfterEnumeration(t *testing.T) {
	for _, change := range []string{"pause", "private", "protected"} {
		t.Run(change, func(t *testing.T) {
			app, now := observationSuggestionFixture(t)
			old := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
			enumerate := func() []SystemApp {
				switch change {
				case "pause":
					app.closedEyes.enabled.Store(true)
				case "private":
					pii.AddUserSensitiveApp("test.finance", "Bank")
				case "protected":
					if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status = 'protected' WHERE id = 'bank'`); err != nil {
						t.Fatal(err)
					}
				}
				return []SystemApp{{BundleID: "test.finance"}}
			}
			got, err := app.privateAppSuggestions(now, enumerate, func(SystemApp) string { return "finance" })
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range got {
				if row.BundleID == "test.finance" {
					t.Fatalf("stale name returned: %+v", got)
				}
			}
			if change == "pause" && len(got) != 0 {
				t.Fatalf("pause returned names: %+v", got)
			}
		})
	}
}

func TestObservationBindingsAreReadOnlyInBrowserPreview(t *testing.T) {
	for _, method := range []string{"GetCaptureByHour", "GetPrivateAppSuggestions"} {
		if !browserRPCAllowlist[method] {
			t.Fatalf("missing preview read: %s", method)
		}
	}
	if browserRPCAllowlist["AddSensitiveApp"] {
		t.Fatal("private-list writes must remain native-only")
	}
}

func TestDoingNowObservationDoesNotWaitForAudioDrain(t *testing.T) {
	app := &App{}
	app.backgroundAudioMu.Lock()
	defer app.backgroundAudioMu.Unlock()
	done := make(chan DoingNow, 1)
	go func() { done <- app.GetDoingNow() }()
	select {
	case view := <-done:
		if view.Microphone != "off" {
			t.Fatalf("draining microphone: %+v", view)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("status waited behind an audio drain")
	}
}
