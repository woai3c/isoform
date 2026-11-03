//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

// cua_get_frontmost reads NSWorkspace's current frontmost app and copies
// its pid + bundle id into the supplied C buffers. Caller-owned memory;
// we don't allocate. We read inside an autorelease pool so any objects
// NSWorkspace hands back are released as soon as we return — the
// proxy may call this twice per request and we don't want to leak.
static int cua_get_frontmost(char* bundleID, int bidLen, char* appName, int nameLen) {
    @autoreleasepool {
        NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
        if (!front) return 0;
        const char *bid = [front.bundleIdentifier UTF8String];
        const char *name = [front.localizedName UTF8String];
        if (bid && bidLen > 0) {
            strncpy(bundleID, bid, bidLen - 1);
            bundleID[bidLen - 1] = '\0';
        }
        if (name && nameLen > 0) {
            strncpy(appName, name, nameLen - 1);
            appName[nameLen - 1] = '\0';
        }
        return front.processIdentifier;
    }
}
*/
import "C"

import "unsafe"

// cuaFrontmost returns the pid, bundle id, and localised name of the
// macOS frontmost app, or zero values if NSWorkspace returns nil. The
// proxy uses this to audit-log a before/after pair on every CUA call
// so we can pinpoint which tool invocation actually pulled focus —
// the LLM logs alone don't tell us that, since cua-driver's "no focus
// steal" guarantee is internal to the binary and we can only observe
// it externally by watching NSWorkspace.
func cuaFrontmost() (pid int32, bundleID string, name string) {
	var bidBuf [256]C.char
	var nameBuf [256]C.char
	cpid := C.cua_get_frontmost(&bidBuf[0], C.int(len(bidBuf)), &nameBuf[0], C.int(len(nameBuf)))
	if cpid == 0 {
		return 0, "", ""
	}
	return int32(cpid), C.GoString((*C.char)(unsafe.Pointer(&bidBuf[0]))), C.GoString((*C.char)(unsafe.Pointer(&nameBuf[0])))
}
