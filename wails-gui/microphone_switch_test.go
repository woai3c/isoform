package main

import (
	"runtime"
	"testing"
)

func TestBackgroundListeningSourceWithoutMicrophone(t *testing.T) {
	cases := map[backgroundListeningSource]backgroundListeningSource{
		backgroundListeningSourceBoth:       backgroundListeningSourceSystem,
		backgroundListeningSourceSystem:     backgroundListeningSourceSystem,
		backgroundListeningSourceMicrophone: backgroundListeningSourceNone,
	}
	for in, want := range cases {
		if got := backgroundListeningSourceWithoutMicrophone(in); got != want {
			t.Fatalf("without microphone %q = %q, want %q", in, got, want)
		}
	}
	if got := resolveBackgroundListeningSource(yansuConfig{}); got != defaultBackgroundListeningSource() {
		t.Fatalf("the microphone is on by default: %q", got)
	}
	paused := resolveBackgroundListeningSource(yansuConfig{BackgroundMicrophonePaused: true})
	if paused == backgroundListeningSourceMicrophone || paused == backgroundListeningSourceBoth {
		t.Fatalf("a paused microphone is still a source: %q", paused)
	}
}

func TestMicrophoneSwitchPausesOnlyTheMicrophoneSource(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the microphone switch is a source of its own only on macOS")
	}
	t.Setenv("HOME", t.TempDir())
	a := &App{}
	if err := updateConfig(func(cfg *yansuConfig) {
		cfg.BackgroundListeningConfigured = true
		cfg.BackgroundListeningDisabled = false
	}); err != nil {
		t.Fatal(err)
	}

	item := func() PermissionItem {
		for _, item := range a.GetPlatformPermissions().Items {
			if item.Key == "microphone" {
				return item
			}
		}
		t.Fatal("no microphone item")
		return PermissionItem{}
	}
	if got := item(); !got.Toggleable || !got.Enabled {
		t.Fatalf("microphone item %+v", got)
	}

	if err := a.SetPermissionEnabled("microphone", false); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if got := item(); got.Enabled {
		t.Fatal("the switch did not stick")
	}
	if !a.GetBackgroundListening() {
		t.Fatal("turning the microphone off must not turn listening off")
	}
	if src := resolveBackgroundListeningSource(loadConfig()); src == backgroundListeningSourceMicrophone || src == backgroundListeningSourceBoth {
		t.Fatalf("the next capture would still use the microphone: %q", src)
	}

	if got := a.GetBackgroundAudioStatus(); !got.MicrophonePaused {
		t.Fatalf("status does not say the microphone is off: %+v", got)
	}
	for _, snap := range a.liveEngagementCaptureSnapshots() {
		if snap.Kind == "microphone" {
			t.Fatal("a microphone switched off on purpose must not count as a capture outage")
		}
	}

	if err := a.SetPermissionEnabled("microphone", true); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	if got := item(); !got.Enabled {
		t.Fatal("the switch did not come back on")
	}
	if src := resolveBackgroundListeningSource(loadConfig()); src != defaultBackgroundListeningSource() {
		t.Fatalf("the microphone is back as a source: %q", src)
	}
}
