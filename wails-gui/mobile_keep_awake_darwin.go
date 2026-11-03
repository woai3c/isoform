package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation -framework Foundation
int isoKeepAwakeHold(void);
void isoKeepAwakeRelease(void);
void isoKeepAwakePowerState(int *onAC, double *batteryPercent, int *hasBattery, int *lowPowerMode, int *lidClosed);
int isoWatchPowerSource(void);
*/
import "C"

import (
	"errors"
	"sync"
)

// The C side lives in mobile_keep_awake_darwin.c. The exported Go callback below
// is declared here, and cgo forbids definitions in a file with //export, so the
// C code that calls it sits in its own file.

var keepAwakePowerWatch struct {
	mu      sync.Mutex
	changed func()
}

type darwinKeepAwakePower struct{}

func newMobileKeepAwakeOSPower() mobileKeepAwakePower {
	return darwinKeepAwakePower{}
}

// State reads the current power inputs without polling for changes.
func (darwinKeepAwakePower) State() mobileKeepAwakePowerState {
	var onAC, hasBattery, lowPowerMode, lidClosed C.int
	var percent C.double
	C.isoKeepAwakePowerState(&onAC, &percent, &hasBattery, &lowPowerMode, &lidClosed)
	return mobileKeepAwakePowerState{
		onAC: onAC == 1, batteryPercent: float64(percent), hasBattery: hasBattery == 1,
		lowPowerMode: lowPowerMode == 1, lidClosed: lidClosed == 1,
	}
}

// Watch registers for power-source and Low Power Mode events on a background
// run loop. Nothing is polled; the run loop calls back only when an input changes.
func (darwinKeepAwakePower) Watch(changed func()) error {
	keepAwakePowerWatch.mu.Lock()
	keepAwakePowerWatch.changed = changed
	keepAwakePowerWatch.mu.Unlock()
	if C.isoWatchPowerSource() == 0 {
		return errors.New("the power source events could not be registered")
	}
	return nil
}

//export goKeepAwakePowerChanged
func goKeepAwakePowerChanged() {
	keepAwakePowerWatch.mu.Lock()
	changed := keepAwakePowerWatch.changed
	keepAwakePowerWatch.mu.Unlock()
	if changed != nil {
		changed()
	}
}

type darwinKeepAwakeAssertion struct{}

func newMobileKeepAwakeOSAssertion() mobileKeepAwakeAssertion {
	return darwinKeepAwakeAssertion{}
}

// Hold takes PreventUserIdleSystemSleep. The display may still sleep on its own
// schedule; the assertion does not keep it on.
func (darwinKeepAwakeAssertion) Hold() error {
	if C.isoKeepAwakeHold() == 0 {
		return errors.New("the system did not accept the keep-awake assertion")
	}
	return nil
}

func (darwinKeepAwakeAssertion) Release() {
	C.isoKeepAwakeRelease()
}
