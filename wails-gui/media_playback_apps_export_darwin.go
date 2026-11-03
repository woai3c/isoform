//go:build darwin

package main

// Separate file (rather than putting //export in media_playback_apps.go)
// to keep the cgo //export directive out of any file that also embeds
// Objective-C in its CGO preamble — cgo sometimes emits the preamble into
// both _cgo_export.c and the per-file C file, causing duplicate-symbol link
// errors. Same pattern as audio_capture_events_export_darwin.go.

/*
#include <stdlib.h>
#include <stdbool.h>
*/
import "C"

// goIsMediaPlaybackApp is invoked from the Objective-C side of
// background_audio_system_darwin.go to decide whether a running
// application's audio should be excluded from the system audio process tap.
// Keeping the lookup table on the Go side keeps a single source of truth
// shared with the post-capture filter in activity_triage.go.
//
//export goIsMediaPlaybackApp
func goIsMediaPlaybackApp(cBundleID *C.char) C.bool {
	if cBundleID == nil {
		return C.bool(false)
	}
	return C.bool(isMediaPlaybackApp(C.GoString(cBundleID)))
}
