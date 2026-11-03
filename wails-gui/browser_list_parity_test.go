//go:build darwin

package main

import (
	"testing"

	"wails-gui/internal/browsers"
	"wails-gui/internal/meetingdetect"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/platform/activitymon/appparser"
)

// TestOneBrowserList keeps call detection, the activity monitor (and its
// tab-address reader) and the page parser on the same browsers: a browser
// that is one in one place and not in another starts no calls there.
func TestOneBrowserList(t *testing.T) {
	parsers := map[string]bool{}
	for _, entry := range appparser.Catalog() {
		if entry.Name == "browser" {
			for _, id := range entry.Bundles {
				parsers[id] = true
			}
		}
	}
	for _, id := range browsers.MacBundleIDs() {
		if !meetingdetect.IsBrowserBundle(id) {
			t.Errorf("%s: call detection does not take it as a browser", id)
		}
		if !activitymon.IsBrowser(id) {
			t.Errorf("%s: the activity monitor does not take it as a browser", id)
		}
		if !parsers[id] {
			t.Errorf("%s: the page parser does not take it", id)
		}
	}
	if len(parsers) != len(browsers.MacBundleIDs()) {
		t.Errorf("the page parser lists %d browsers, the list has %d", len(parsers), len(browsers.MacBundleIDs()))
	}
	for _, id := range []string{"com.operasoftware.Opera", "com.operasoftware.OperaGX", "com.google.Chrome.canary", "com.brave.Browser.nightly", "com.microsoft.edgemac.Dev"} {
		if browsers.FamilyOf(id) != browsers.Chromium {
			t.Errorf("%s: read with Chrome's AppleScript dictionary", id)
		}
	}
	if browsers.FamilyOf("org.mozilla.nightly") != browsers.Firefox || browsers.FamilyOf("com.apple.Safari") != browsers.Safari {
		t.Error("Firefox and Safari families")
	}
	if meetingdetect.IsBrowserBundle("us.zoom.xos") || activitymon.IsBrowser("us.zoom.xos") {
		t.Error("Zoom is no browser")
	}
}
