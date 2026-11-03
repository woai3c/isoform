package main

import "testing"

// Regression test for the "not granted" substring trap — strings.Contains
// "granted" matches "not granted" too, so an early version of
// parseCuaPermissionStatus reported denials as grants. Keep these cases
// covering the wording variants we've seen from cua-driver in the wild
// plus a couple of plausible future ones.
func TestParseCuaPermissionStatus(t *testing.T) {
	cases := []struct {
		name              string
		text              string
		wantAccessibility bool
		wantScreen        bool
		wantOK            bool
	}{
		{
			name:              "both granted (observed in v0.1.4)",
			text:              "✅ Accessibility: granted.\n✅ Screen Recording: granted.",
			wantAccessibility: true,
			wantScreen:        true,
			wantOK:            true,
		},
		{
			name:              "only accessibility granted",
			text:              "✅ Accessibility: granted.\n❌ Screen Recording: not granted.",
			wantAccessibility: true,
			wantScreen:        false,
			wantOK:            false,
		},
		{
			name:              "neither",
			text:              "❌ Accessibility: not-determined.\n❌ Screen Recording: denied.",
			wantAccessibility: false,
			wantScreen:        false,
			wantOK:            false,
		},
		{
			name:              "alternative wording",
			text:              "Accessibility access: not granted.\nScreen Recording access: granted.",
			wantAccessibility: false,
			wantScreen:        true,
			wantOK:            false,
		},
		{
			name:              "denied + not determined",
			text:              "❌ Accessibility: denied.\n❌ Screen Recording: not determined.",
			wantAccessibility: false,
			wantScreen:        false,
			wantOK:            false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCuaPermissionStatus(tc.text)
			if got.Accessibility != tc.wantAccessibility {
				t.Errorf("Accessibility=%v want %v", got.Accessibility, tc.wantAccessibility)
			}
			if got.ScreenRecording != tc.wantScreen {
				t.Errorf("ScreenRecording=%v want %v", got.ScreenRecording, tc.wantScreen)
			}
			if got.OK != tc.wantOK {
				t.Errorf("OK=%v want %v", got.OK, tc.wantOK)
			}
		})
	}
}
