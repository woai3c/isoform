//go:build darwin

package main

// This file is separate from background_audio_darwin.go purely to keep
// the //export directive out of a file that also embeds Objective-C in
// its CGO preamble — cgo sometimes emits the preamble into both
// _cgo_export.c and the per-file C file, causing duplicate-symbol link
// errors. Following the pattern established in toast_window_export_darwin.go.

/*
#include <stdlib.h>
*/
import "C"

//export goAudioCaptureEvent
func goAudioCaptureEvent(cSource, cKind, cOutcome, cMessage *C.char) {
	// Copy the C strings here and send the Wails event off this thread.
	// The native recorders call this from inside their @autoreleasepool
	// (CoreAudioInputRecorder_Stop does on every stop). Wails' ExecJS, which
	// EventsEmit uses, autoreleases the script string and also releases it in
	// its main-queue block, so when that pool drains first the main thread
	// releases a freed string: SIGSEGV, exit status 2 and no crash report,
	// every time the desktop locks or observation pauses with capture on.
	recordNativeAudioCaptureEvent(AudioCaptureEvent{
		Source:  C.GoString(cSource),
		Kind:    C.GoString(cKind),
		Outcome: C.GoString(cOutcome),
		Message: C.GoString(cMessage),
	})
}
