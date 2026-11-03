//go:build !darwin && !windows

package main

import (
	"fmt"
	"time"
)

// stubAudioRecorder is a no-op implementation for unsupported platforms.
type stubAudioRecorder struct{}

func isBackgroundListeningSourceSupported(source backgroundListeningSource) bool {
	return source == backgroundListeningSourceMicrophone
}

func newPlatformAudioRecorder(_ backgroundListeningSource) audioRecorder {
	return &stubAudioRecorder{}
}

func (r *stubAudioRecorder) Start() error {
	return fmt.Errorf("background audio capture not supported on this platform")
}

func (r *stubAudioRecorder) ReadChunk(_ time.Duration) (capturedAudioChunk, error) {
	return capturedAudioChunk{}, fmt.Errorf("not supported")
}

func (r *stubAudioRecorder) Stop() {}

func (r *stubAudioRecorder) ActiveSources() []backgroundListeningSource {
	return []backgroundListeningSource{backgroundListeningSourceMicrophone}
}
