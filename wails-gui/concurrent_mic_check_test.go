//go:build !darwin && !windows

package main

import "testing"

// On platforms without a real concurrent-mic-check implementation the
// function is a no-op stub. The real behavior is only testable on macOS 14+
// (CoreAudio process-object API) and Windows (WASAPI session enumeration);
// both paths are exercised manually since they require live audio sessions.
func TestAnotherProcessUsingMicInput_StubReturnsFalse(t *testing.T) {
	if got := anotherProcessUsingMicInput(); got {
		t.Errorf("anotherProcessUsingMicInput() = %v on stub platform, want false", got)
	}
}
