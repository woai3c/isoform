package plugins

import (
	"testing"

	"wails-gui/internal/engagement"
)

func TestBuiltinsInventoryAndStoredSemantics(t *testing.T) {
	registry := NewRegistry()
	byID := map[string]Builtin{}
	for _, builtin := range Builtins() {
		registry.Register(builtin.Manifest)
		byID[builtin.ID] = builtin
		if !builtin.Essential && builtin.Setter == "" {
			t.Errorf("%s has no existing writer", builtin.ID)
		}
	}
	for _, kind := range engagement.AllKinds() {
		if _, ok := byID["notify-"+string(kind)]; !ok {
			t.Errorf("notification kind %s is not mapped", kind)
		}
	}
	for _, id := range []string{"calendar-peek", "meeting-recording", "topic-finds", "auto-speak", "browser-bridge", "activity-monitor", "triage-events", "calendar-sync", "mail-sync", "files-activity", "text-channel"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("design switch %s is not mapped", id)
		}
	}
	if _, ok := byID["daily-news"]; ok {
		t.Fatal("retired Daily News switch is still registered")
	}
	for _, test := range []struct {
		id, raw string
		want    bool
	}{
		{"calendar-peek", "", true},
		{"calendar-peek", "false", false},
		{"calendar-peek", "FALSE", true},
		{"calendar-peek", " false ", true},
		{"triage-memory", "0", true},
		{"files-activity", "", false},
		{"files-activity", "false", false},
		{"files-activity", "0", true},
		{"work-pattern-profile", "", false},
		{"work-pattern-profile", " TRUE ", true},
		{"work-pattern-profile", "1", false},
	} {
		if got := byID[test.id].StoredOn(test.raw); got != test.want {
			t.Errorf("%s.StoredOn(%q) = %v, want %v", test.id, test.raw, got, test.want)
		}
	}
}

func TestBuiltinCatalogueOnlyLocksEssentials(t *testing.T) {
	want := map[string]bool{"authority": true, "cron-api": true, "notification-pacing": true}
	for _, builtin := range Builtins() {
		if builtin.Essential != want[builtin.ID] {
			t.Errorf("%s essential = %v", builtin.ID, builtin.Essential)
		}
		for _, dependency := range builtin.Requires {
			if builtin.ID == "auto-speak-automation" || builtin.ID == "auto-speak-notifications" {
				t.Errorf("independent speech category %s depends on %s", builtin.ID, dependency)
			}
		}
	}
}

func TestBuiltinPlatformFiltersMatchPermissionAvailability(t *testing.T) {
	for _, platform := range []string{"darwin", "windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			rows := map[string]Builtin{}
			for _, row := range BuiltinsForPlatform(platform) {
				rows[row.ID] = row
			}
			for _, id := range []string{"calendar-sync", "mail-sync", "reminders-sync", "files-activity", "clipboard"} {
				if _, present := rows[id]; present != (platform == "darwin") {
					t.Errorf("%s available on %s = %v", id, platform, present)
				}
			}
			if _, present := rows["background-microphone"]; present != (platform == "darwin" || platform == "windows") {
				t.Errorf("microphone available on %s = %v", platform, present)
			}
			for _, id := range []string{"authority", "cron-api", "notification-pacing", "triage-memory", "start-at-login"} {
				if _, present := rows[id]; !present {
					t.Errorf("platform-neutral %s missing on %s", id, platform)
				}
			}
		})
	}
}
