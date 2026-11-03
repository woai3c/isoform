//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <unistd.h>

// Another app's ordinary window shows its title only once this process may
// record the screen, and that changes as soon as the person allows it, while
// CGPreflightScreenCaptureAccess can keep saying no until the app reopens
// (macOS's "Quit & Reopen" / "Later").
static int isoform_other_window_titles_visible(void) {
    CFArrayRef windows = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
    if (!windows) return 0;
    pid_t self = getpid();
    int visible = 0;
    for (CFIndex i = 0; i < CFArrayGetCount(windows) && !visible; i++) {
        CFDictionaryRef window = CFArrayGetValueAtIndex(windows, i);
        int pid = 0, layer = -1;
        CFNumberRef pidRef = CFDictionaryGetValue(window, kCGWindowOwnerPID);
        CFNumberRef layerRef = CFDictionaryGetValue(window, kCGWindowLayer);
        if (pidRef) CFNumberGetValue(pidRef, kCFNumberIntType, &pid);
        if (layerRef) CFNumberGetValue(layerRef, kCFNumberIntType, &layer);
        if (pid == self || layer != 0) continue;
        CFStringRef title = CFDictionaryGetValue(window, kCGWindowName);
        if (title && CFStringGetLength(title) > 0) visible = 1;
    }
    CFRelease(windows);
    return visible;
}
*/
import "C"

// screenRecordingGrantedNow says whether Screen Recording is allowed now,
// including a grant made after launch that only takes effect on reopening.
var screenRecordingGrantedNow = func() bool {
	return preflightScreenCaptureAccess() || C.isoform_other_window_titles_visible() != 0
}
