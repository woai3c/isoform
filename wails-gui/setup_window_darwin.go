//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "setup_window_darwin.h"

// Defined in this file below (//export).
extern void goSetupFinishLanded(void);
*/
import "C"

import (
	"sync"
	"unsafe"
)

// setupFinishWait holds the channel of the finish in progress. The landing callback
// runs on the main thread and only does a non-blocking send.
var setupFinishWait struct {
	sync.Mutex
	done chan struct{}
}

//export goSetupFinishLanded
func goSetupFinishLanded() {
	setupFinishWait.Lock()
	done := setupFinishWait.done
	setupFinishWait.Unlock()
	if done == nil {
		return
	}
	select {
	case done <- struct{}{}:
	default:
	}
}

func cBoolInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func nativeSetupWindowApply(mode int, r setupWindowRect) bool {
	return C.isoformSetupWindowApply(C.int(mode), C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H)) != 0
}

func nativeSetupWindowFrame() (setupWindowRect, bool) {
	var out [4]C.double
	if C.isoformSetupWindowFrame(&out[0]) == 0 {
		return setupWindowRect{}, false
	}
	return setupWindowRect{X: float64(out[0]), Y: float64(out[1]), W: float64(out[2]), H: float64(out[3])}, true
}

func nativeSetupMarkSetVisible(visible, pulse bool) bool {
	return C.isoformSetupMarkSetVisible(cBoolInt(visible), cBoolInt(pulse)) != 0
}

func nativeSetupWindowLandingInput() setupWindowLandingInput {
	var out C.IsoformSetupLanding
	if C.isoformSetupLandingInputs(&out) == 0 {
		return setupWindowLandingInput{}
	}
	return setupWindowLandingInput{
		ItemVisible:    out.itemVisible != 0,
		Item:           setupWindowRect{X: float64(out.itemX), Y: float64(out.itemY), W: float64(out.itemW), H: float64(out.itemH)},
		MenuBarVisible: out.menuBarVisible != 0,
		MenuBarTop:     float64(out.menuBarTop),
		Bounds:         setupWindowRect{X: float64(out.boundsX), Y: float64(out.boundsY), W: float64(out.boundsW), H: float64(out.boundsH)},
		WindowScreen:   setupWindowRect{X: float64(out.windowX), Y: float64(out.windowY), W: float64(out.windowW), H: float64(out.windowH)},
		HasNotch:       out.hasNotch != 0,
		NotchX:         float64(out.notchX),
		NotchW:         float64(out.notchW),
	}
}

// nativeSetupFinishPlay starts the finish. The returned channel receives once the
// icon has landed.
func nativeSetupFinishPlay(p setupFinishPlay) <-chan struct{} {
	done := make(chan struct{}, 1)
	setupFinishWait.Lock()
	setupFinishWait.done = done
	setupFinishWait.Unlock()

	callout := C.CString(p.Callout)
	defer C.free(unsafe.Pointer(callout))
	params := C.IsoformSetupFinish{
		iconX:        C.double(p.IconX),
		iconY:        C.double(p.IconY),
		iconSize:     C.double(p.IconSize),
		targetX:      C.double(p.Target.X),
		targetY:      C.double(p.Target.Y),
		anchorX:      C.double(p.Anchor.X),
		anchorY:      C.double(p.Anchor.Y),
		ring:         cBoolInt(p.Ring),
		reduceMotion: cBoolInt(p.ReduceMotion),
		callout:      callout,
	}
	C.isoformSetupFinishPlay(&params)
	return done
}

func nativeSetupReveal(duration float64) {
	C.isoformSetupRevealMainWindow(C.double(duration))
}

func nativeSetupMarkStartsVisible(visible bool) {
	C.isoformSetupMarkSetStartsVisible(cBoolInt(visible))
}
