//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include "window_state_darwin.h"
*/
import "C"

import "context"

func readWindowState(_ context.Context) (WindowState, bool) {
	state := C.isoformReadWindowState()
	return WindowState{
		X: int(state.x), Y: int(state.y), Width: int(state.width), Height: int(state.height),
		Maximised: bool(state.maximised),
	}, bool(state.valid)
}
