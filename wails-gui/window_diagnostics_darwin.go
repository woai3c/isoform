//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include "window_diagnostics_darwin.h"
*/
import "C"

import "os"

// enableWindowDiagnostics opts into structural window metadata and show-call
// stacks in the process stderr log. Leave it off during ordinary use.
func enableWindowDiagnostics() {
	if os.Getenv("ISOFORM_DEBUG_WINDOWS") == "1" {
		C.isoformInstallWindowDiagnostics()
	}
}
