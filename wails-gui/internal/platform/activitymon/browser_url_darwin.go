//go:build darwin

package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include "main_queue_darwin.h"

// Get the active tab URL from a browser using AppleScript, by the way its
// family exposes tabs (1 Chromium, 2 Safari; Firefox does not). Returns
// NULL when the browser cannot be asked or has no tab.
static const char* get_browser_url(const char* bundleID, int family, ActivityMonReadStatus *status) {
	@autoreleasepool {
		NSString *bid = [NSString stringWithUTF8String:bundleID];
		NSString *script = nil;

		if (family == 1) {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\" to get URL of active tab of front window", bid];
		} else if (family == 2) {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\" to get URL of front document", bid];
		}

		if (!script) return NULL;

		NSString *url = activitymon_browser_script_read(script, status);
		if (!url || [url length] == 0) return NULL;

		return strdup([url UTF8String]);
	}
}
*/
import "C"
import (
	"flag"
	"unsafe"

	"wails-gui/internal/browsers"
)

// IsBrowser returns true if the bundle ID belongs to a known web browser
// (internal/browsers, the one list).
func IsBrowser(bundleID string) bool {
	return browsers.FamilyOf(bundleID) != browsers.NotABrowser
}

// GetBrowserURL returns the URL of the active tab in the frontmost browser.
// Returns empty string if the browser doesn't support AppleScript URL access
// or if no browser is focused. The call has a short timeout to avoid blocking.
func GetBrowserURL(bundleID string) string {
	return ReadBrowserURL(bundleID).URL
}

// ReadBrowserURL preserves native timeout and admission status for diagnostics.
func ReadBrowserURL(bundleID string) BrowserURLRead {
	var family C.int
	switch browsers.FamilyOf(bundleID) {
	case browsers.Chromium:
		family = 1
	case browsers.Safari:
		family = 2
	default:
		return BrowserURLRead{}
	}
	if flag.Lookup("test.v") != nil {
		panic("native browser probe reached from a test binary")
	}
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))

	var status C.ActivityMonReadStatus
	cURL := C.get_browser_url(cBundleID, family, &status)
	result := BrowserURLRead{TimedOut: status == C.ActivityMonReadTimedOut, InFlight: status == C.ActivityMonReadBusy}
	if cURL == nil {
		return result
	}
	defer C.free(unsafe.Pointer(cURL))
	result.URL = C.GoString(cURL)
	return result
}
