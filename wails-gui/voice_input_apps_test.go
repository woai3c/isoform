package main

import "testing"

func TestIsVoiceInputApp(t *testing.T) {
	cases := []struct {
		name     string
		bundleID string
		want     bool
	}{
		{"known voice input app", "com.goodsnooze.MacWhisper", true},
		{"empty bundle id", "", false},
		{"unknown app", "com.example.notawhisperapp", false},
		{"meeting app is not voice input", "us.zoom.xos", false},
		{"media app is not voice input", "com.spotify.client", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isVoiceInputApp(c.bundleID); got != c.want {
				t.Errorf("isVoiceInputApp(%q) = %v, want %v", c.bundleID, got, c.want)
			}
		})
	}
}

// TestVoiceInputAppBundleIDs_NoOverlap guards against accidentally adding the
// same bundle ID to both voiceInputAppBundleIDs and mediaPlaybackBundleIDs.
// Overlap would make the OS-level system-tap exclusion silently swallow what
// the per-event filter expects to handle differently.
func TestVoiceInputAppBundleIDs_NoOverlap(t *testing.T) {
	for id := range voiceInputAppBundleIDs {
		if _, ok := mediaPlaybackBundleIDs[id]; ok {
			t.Errorf("%q appears in both voiceInputAppBundleIDs and mediaPlaybackBundleIDs", id)
		}
	}
}

func TestIsVoiceInputApp_HonorsUserList(t *testing.T) {
	// Save + restore so this test doesn't leak state into others.
	original := GetUserVoiceInputApps()
	t.Cleanup(func() { SetUserVoiceInputApps(original) })

	const userBundle = "com.example.usercustomvoiceinput"
	if isVoiceInputApp(userBundle) {
		t.Fatalf("precondition failed: %q already matched before being added", userBundle)
	}

	AddUserVoiceInputApp(userBundle, "Custom Voice Input")
	if !isVoiceInputApp(userBundle) {
		t.Errorf("after AddUserVoiceInputApp(%q): isVoiceInputApp() = false, want true", userBundle)
	}

	RemoveUserVoiceInputApp(userBundle)
	if isVoiceInputApp(userBundle) {
		t.Errorf("after RemoveUserVoiceInputApp(%q): isVoiceInputApp() = true, want false", userBundle)
	}
}

func TestAddUserVoiceInputApp_BuiltInIsNoOp(t *testing.T) {
	original := GetUserVoiceInputApps()
	t.Cleanup(func() { SetUserVoiceInputApps(original) })

	const builtIn = "com.goodsnooze.MacWhisper"
	if _, ok := voiceInputAppBundleIDs[builtIn]; !ok {
		t.Fatalf("precondition failed: %q is not a built-in entry", builtIn)
	}

	AddUserVoiceInputApp(builtIn, "MacWhisper")
	if _, ok := GetUserVoiceInputApps()[builtIn]; ok {
		t.Errorf("AddUserVoiceInputApp accepted a built-in entry %q; built-ins should be no-op", builtIn)
	}
}

func TestAllVoiceInputAppEntries_MarksDefaultAndSortsByName(t *testing.T) {
	original := GetUserVoiceInputApps()
	t.Cleanup(func() { SetUserVoiceInputApps(original) })

	SetUserVoiceInputApps(map[string]string{
		"com.example.zeta":  "Zeta Dictation",
		"com.example.alpha": "Alpha Dictation",
	})

	entries := AllVoiceInputAppEntries()

	// Defaults from built-in list must all be flagged.
	for _, e := range entries {
		if _, isBuiltIn := voiceInputAppBundleIDs[e.BundleID]; isBuiltIn {
			if !e.IsDefault {
				t.Errorf("entry %q is built-in but IsDefault=false", e.BundleID)
			}
		} else if e.IsDefault {
			t.Errorf("entry %q is user-added but IsDefault=true", e.BundleID)
		}
	}

	// Sort order: AppName ascending. Alpha must come before Zeta.
	var alphaIdx, zetaIdx = -1, -1
	for i, e := range entries {
		switch e.BundleID {
		case "com.example.alpha":
			alphaIdx = i
		case "com.example.zeta":
			zetaIdx = i
		}
	}
	if alphaIdx == -1 || zetaIdx == -1 {
		t.Fatalf("expected both user entries in result; got alphaIdx=%d zetaIdx=%d (entries=%v)", alphaIdx, zetaIdx, entries)
	}
	if alphaIdx >= zetaIdx {
		t.Errorf("expected Alpha (idx=%d) to sort before Zeta (idx=%d)", alphaIdx, zetaIdx)
	}
}
