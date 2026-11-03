//go:build darwin

package main

import "testing"

// TestGoAudioCaptureEvent_DeliversOffTheCallerThread reports "stopped" the
// way CoreAudioInputRecorder_Stop does: from C through _emit_audio_event into
// the goAudioCaptureEvent export. The held-lock check in
// assertNativeAudioEventDeliveredOffCaller stands in for the real condition,
// that no Wails call runs on that thread while the native autorelease pool is
// active.
func TestGoAudioCaptureEvent_DeliversOffTheCallerThread(t *testing.T) {
	assertNativeAudioEventDeliveredOffCaller(t, func() {
		emitAudioCaptureEventFromNative("microphone", "stopped", "", "caller requested stop")
	})
}
