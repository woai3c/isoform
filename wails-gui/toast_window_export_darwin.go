//go:build darwin

package main

// This file exists solely to isolate the //export function from the Objective-C
// @implementation blocks in toast_window_darwin.go.  When a cgo file contains
// both //export directives and @implementation blocks, cgo emits the preamble
// into both _cgo_export.c and the per-file C file, leading to duplicate
// symbols at link time.

/*
#include <stdlib.h>
*/
import "C"

//export goToastEvent
func goToastEvent(cID, cType *C.char) {
	handleToastEvent(C.GoString(cID), C.GoString(cType))
}
