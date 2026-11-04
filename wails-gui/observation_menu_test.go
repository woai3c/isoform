package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObservingMenuStatesMatchSidebar(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		data, err := os.ReadFile(filepath.Join("frontend", "src", "i18n", "locales", lang, "app.json"))
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Observing map[string]string `json:"observingStatus"`
		}
		// The catalog includes nested descriptions; decode the state strings only.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw["observingStatus"], &values); err != nil {
			t.Fatal(err)
		}
		catalog.Observing = map[string]string{}
		for _, key := range []string{"label", "notObserving", "paused", "pausedUntil"} {
			var value string
			if err := json.Unmarshal(values[key], &value); err != nil {
				t.Fatal(err)
			}
			catalog.Observing[key] = value
		}
		until := time.Date(2026, 10, 22, 15, 15, 0, 0, time.Local)
		cases := []struct {
			view   DoingNow
			active bool
			key    string
			want   string
		}{
			{DoingNow{Kind: doingNowWatching}, true, "label", ""},
			{DoingNow{Kind: doingNowWorking}, false, "notObserving", ""},
			{DoingNow{Kind: doingNowPaused}, true, "paused", ""},
			{DoingNow{Kind: doingNowPaused, PausedUntil: until.UnixMilli()}, true, "pausedUntil", map[string]string{"en": "3:15 PM", "zh-CN": "15:15"}[lang]},
		}
		for _, tc := range cases {
			want := strings.ReplaceAll(catalog.Observing[tc.key], "{{time}}", tc.want)
			if got := observingMenuText(tc.view, tc.active, lang); got != want {
				t.Errorf("%s %s: got %q, want %q", lang, tc.key, got, want)
			}
		}
	}
}

func TestObservingMenuPauseUsesOneHourAndResumeClearsDeadline(t *testing.T) {
	app := newTimedPauseFixture(t)
	before := time.Now()
	app.toggleMenuBarObservation()
	deadline, err := time.Parse(time.RFC3339Nano, pauseDeadline(t, app))
	if err != nil || !app.IsClosedEyesModeEnabled() {
		t.Fatalf("menu pause did not persist: %v", err)
	}
	if deadline.Before(before.Add(time.Hour)) || deadline.After(time.Now().Add(time.Hour)) {
		t.Fatalf("deadline %v is not one hour from the menu activation", deadline)
	}
	app.toggleMenuBarObservation()
	if app.IsClosedEyesModeEnabled() || pauseDeadline(t, app) != "" {
		t.Fatal("menu resume left the pause active")
	}
}

// Calling the shared helper with an explicit language also guards its portable
// API: common code must never reach into a platform-only menuBarApp variable.
func TestObservingMenuActionNamesDurationAndLanguage(t *testing.T) {
	for _, tc := range []struct{ lang, pause, resume string }{
		{"en", "Pause observing for 1 hour", "Resume observing"},
		{"zh-CN", "暂停观察 1 小时", "恢复观察"},
	} {
		if got := menuBarObservationTitle(false, tc.lang); got != tc.pause {
			t.Fatalf("%s pause = %q", tc.lang, got)
		}
		if got := menuBarObservationTitle(true, tc.lang); got != tc.resume {
			t.Fatalf("%s resume = %q", tc.lang, got)
		}
	}
	if menuBarLanguage(nil) != "en" {
		t.Fatal("uninitialized menus must use English")
	}
}

func TestIndefiniteObservationToggleKeepsClosedEyesUntilResumed(t *testing.T) {
	app := newTimedPauseFixture(t)
	app.toggleClosedEyesModeFromHotkey()
	if !app.IsClosedEyesModeEnabled() || pauseDeadline(t, app) != "" {
		t.Fatal("indefinite toggle must pause without an automatic resume deadline")
	}
	app.toggleClosedEyesModeFromHotkey()
	if app.IsClosedEyesModeEnabled() {
		t.Fatal("indefinite toggle must resume on the next activation")
	}
}

func TestWindowsObservingTimedMenuDoesNotAdvertiseTheGlobalShortcut(t *testing.T) {
	windows, err := os.ReadFile("menubar_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(windows), "closedEyesHotkeySuffix") || strings.Contains(string(windows), "Ctrl+Alt+Shift+E") {
		t.Fatal("the Windows timed menu must not advertise the indefinite global toggle")
	}
}

func TestObservingMenuClockMatchesTheMergedClockFormat(t *testing.T) {
	when := time.Date(2026, 10, 22, 9, 0, 0, 0, time.Local)
	if got := observationMenuClock(when, "en"); got != "9:00 AM" {
		t.Fatal(got)
	}
	if got := observationMenuClock(when, "zh-CN"); got != "9:00" {
		t.Fatal(got)
	}
}
