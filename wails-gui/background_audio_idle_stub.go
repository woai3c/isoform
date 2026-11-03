//go:build !darwin

package main

// Platforms without an output-activity wake signal retain continuous
// capture. The noise gate still skips silent chunks before ASR.
func platformSystemAudioWakeCheck() func() (active, known bool) {
	return nil
}
