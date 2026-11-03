//go:build darwin

package activitymon

// Kept apart from sysstate_darwin.go so the //export directive never shares a
// file with Objective-C definitions in the cgo preamble (duplicate-symbol
// link errors otherwise; see audio_capture_events_export_darwin.go).

/*
#include <stdlib.h>
*/
import "C"

//export goSystemStateChanged
func goSystemStateChanged(kind *C.char) {
	// Only a non-blocking channel send: the notification centers post on
	// their own threads, and nothing here may touch Wails or the store.
	pushSystemStateChange(SystemStateKind(C.GoString(kind)))
}
