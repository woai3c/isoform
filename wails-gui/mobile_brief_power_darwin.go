//go:build darwin

package main

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <stdint.h>
#include <stdlib.h>
#include <IOKit/IOReturn.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <CoreFoundation/CoreFoundation.h>

// yansuBriefCreateSleepAssertion asks macOS to keep the system awake while
// the brief is generated. The assertion ID is written only on success.
static int yansuBriefCreateSleepAssertion(uint32_t *outID, const char *reason) {
    CFStringRef name = CFStringCreateWithCString(kCFAllocatorDefault, reason, kCFStringEncodingUTF8);
    if (name == NULL) {
        return -1;
    }
    IOPMAssertionID id = 0;
    IOReturn status = IOPMAssertionCreateWithName(
        kIOPMAssertionTypePreventUserIdleSystemSleep,
        kIOPMAssertionLevelOn,
        name,
        &id);
    CFRelease(name);
    if (status != kIOReturnSuccess) {
        return (int)status;
    }
    *outID = id;
    return 0;
}

static int yansuBriefReleaseSleepAssertion(uint32_t id) {
    return (int)IOPMAssertionRelease(id);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// holdMobileBriefPowerAssertion keeps the Mac from idle-sleeping while a brief
// is generated. The returned release function drops the assertion and is safe
// to call more than once.
func holdMobileBriefPowerAssertion(reason string) (func(), error) {
	cReason := C.CString(reason)
	defer C.free(unsafe.Pointer(cReason))

	var id C.uint32_t
	if status := C.yansuBriefCreateSleepAssertion(&id, cReason); status != 0 {
		return nil, fmt.Errorf("sleep assertion was not granted (status %d)", int(status))
	}
	assertion := id
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = C.yansuBriefReleaseSleepAssertion(assertion)
		})
	}, nil
}
