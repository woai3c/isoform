//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

static int afterCallLowPowerMode(void) {
    @autoreleasepool {
        if (@available(macOS 12.0, *)) {
            return [[NSProcessInfo processInfo] isLowPowerModeEnabled] ? 1 : 0;
        }
        return 0;
    }
}
*/
import "C"

import "golang.org/x/sys/unix"

// lowPowerModeEnabled reports macOS Low Power Mode.
func lowPowerModeEnabled() bool {
	return C.afterCallLowPowerMode() != 0
}

// memoryPressureHigh reports the kernel's memory-pressure level at warning
// or worse (1 normal, 2 warning, 4 critical).
func memoryPressureHigh() bool {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	return err == nil && level >= 2
}
