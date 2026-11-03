//go:build darwin

package main

import (
	"strings"
	"testing"
)

// The snapshot depends on what else holds the microphone on this Mac, so
// this only checks it is well formed: bundle IDs only with an active
// snapshot, none empty or padded.
func TestOtherMicInputAppsSnapshotIsWellFormed(t *testing.T) {
	snapshot := otherMicInputApps()
	if !snapshot.Active && len(snapshot.BundleIDs) > 0 {
		t.Fatalf("bundle IDs without an active snapshot: %v", snapshot.BundleIDs)
	}
	for _, id := range snapshot.BundleIDs {
		if id == "" || strings.TrimSpace(id) != id {
			t.Fatalf("malformed bundle ID %q in %v", id, snapshot.BundleIDs)
		}
	}
	if snapshot.Active != anotherProcessUsingMicInput() {
		t.Logf("the microphone changed hands between two reads")
	}
	t.Logf("other processes on the microphone: active=%v bundles=%v", snapshot.Active, snapshot.BundleIDs)
}

func TestMicInputSnapshotMeetingApp(t *testing.T) {
	cases := []struct {
		bundles []string
		want    string
	}{
		{[]string{"com.anthropic.claudefordesktop"}, ""},
		{[]string{"com.openai.chat", "us.zoom.xos"}, "Zoom"},
		{[]string{"com.microsoft.teams2"}, "Microsoft Teams"},
		{nil, ""},
	}
	for _, tc := range cases {
		got := micInputSnapshot{Active: len(tc.bundles) > 0, BundleIDs: tc.bundles}.meetingApp()
		if got.MeetingName != tc.want {
			t.Fatalf("%v -> %q, want %q", tc.bundles, got.MeetingName, tc.want)
		}
	}
}
