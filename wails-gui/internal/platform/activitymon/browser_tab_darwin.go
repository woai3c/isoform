//go:build darwin

package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#include <stdbool.h>
#include <stdlib.h>
#include "main_queue_darwin.h"

// Close the active tab of the frontmost browser window when its URL contains
// the fragment. Chromium browsers and Safari expose tabs over AppleScript;
// Firefox does not. Returns true when a tab was closed.
static bool close_browser_tab_if_url(const char *bundleID, const char *fragment) {
	@autoreleasepool {
		NSString *bid = [NSString stringWithUTF8String:bundleID];
		NSString *frag = [NSString stringWithUTF8String:fragment];
		NSString *script = nil;
		if ([bid isEqualToString:@"com.google.Chrome"] ||
			[bid isEqualToString:@"com.brave.Browser"] ||
			[bid isEqualToString:@"com.microsoft.edgemac"] ||
			[bid isEqualToString:@"com.vivaldi.Vivaldi"] ||
			[bid hasPrefix:@"company.thebrowser.Browser"]) {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\"\n"
				 "  if (count of windows) is 0 then return \"none\"\n"
				 "  set u to URL of active tab of front window\n"
				 "  if u contains \"%@\" then\n"
				 "    close active tab of front window\n"
				 "    return \"closed\"\n"
				 "  end if\n"
				 "  return \"kept\"\n"
				 "end tell", bid, frag];
		} else if ([bid isEqualToString:@"com.apple.Safari"] ||
				   [bid isEqualToString:@"com.apple.SafariTechnologyPreview"]) {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\"\n"
				 "  if (count of windows) is 0 then return \"none\"\n"
				 "  set u to URL of current tab of front window\n"
				 "  if u contains \"%@\" then\n"
				 "    close current tab of front window\n"
				 "    return \"closed\"\n"
				 "  end if\n"
				 "  return \"kept\"\n"
				 "end tell", bid, frag];
		}
		if (!script) return false;
		NSString *result = activitymon_browser_script(script);
		if (!result) return false;
		return [result isEqualToString:@"closed"];
	}
}

// Identify the active tab of the frontmost window so it can be closed later
// whatever the user does meanwhile: Chromium browsers expose stable window
// and tab ids; Safari exposes the window id and the tab index. Returns an
// empty string when the browser cannot be asked.
static char *browser_active_tab_ref(const char *bundleID) {
	@autoreleasepool {
		NSString *bid = [NSString stringWithUTF8String:bundleID];
		NSString *script = nil;
		if ([bid isEqualToString:@"com.google.Chrome"] ||
			[bid isEqualToString:@"com.brave.Browser"] ||
			[bid isEqualToString:@"com.microsoft.edgemac"] ||
			[bid isEqualToString:@"com.vivaldi.Vivaldi"] ||
			[bid hasPrefix:@"company.thebrowser.Browser"]) {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\"\n"
				 "  if (count of windows) is 0 then return \"\"\n"
				 "  set w to front window\n"
				 "  return \"chromium:\" & (id of w as text) & \":\" & (id of active tab of w as text)\n"
				 "end tell", bid];
		} else if ([bid isEqualToString:@"com.apple.Safari"] ||
				   [bid isEqualToString:@"com.apple.SafariTechnologyPreview"]) {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\"\n"
				 "  if (count of windows) is 0 then return \"\"\n"
				 "  set w to front window\n"
				 "  return \"safari:\" & (id of w as text) & \":\" & (index of current tab of w as text)\n"
				 "end tell", bid];
		}
		if (!script) return NULL;
		NSString *result = activitymon_browser_script(script);
		if (!result) return NULL;
		NSString *value = result;
		if (!value || value.length == 0) return NULL;
		return strdup([value UTF8String]);
	}
}

// Close the tab a ref from browser_active_tab_ref names, when its URL still
// contains the fragment (the page Isoform opened, not something the user put
// there since). Returns true when a tab was closed.
static bool close_browser_tab_ref(const char *bundleID, const char *windowID, const char *tabRef, const char *fragment, bool safari) {
	@autoreleasepool {
		NSString *bid = [NSString stringWithUTF8String:bundleID];
		NSString *wid = [NSString stringWithUTF8String:windowID];
		NSString *tid = [NSString stringWithUTF8String:tabRef];
		NSString *frag = [NSString stringWithUTF8String:fragment];
		NSString *script = nil;
		if (safari) {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\"\n"
				 "  repeat with w in windows\n"
				 "    if (id of w as text) is \"%@\" then\n"
				 "      set i to %@ as integer\n"
				 "      if i > (count of tabs of w) then return \"none\"\n"
				 "      set t to tab i of w\n"
				 "      if (URL of t) contains \"%@\" then\n"
				 "        close t\n"
				 "        return \"closed\"\n"
				 "      end if\n"
				 "      return \"kept\"\n"
				 "    end if\n"
				 "  end repeat\n"
				 "  return \"none\"\n"
				 "end tell", bid, wid, tid, frag];
		} else {
			script = [NSString stringWithFormat:
				@"tell application id \"%@\"\n"
				 "  repeat with w in windows\n"
				 "    if (id of w as text) is \"%@\" then\n"
				 "      repeat with t in tabs of w\n"
				 "        if (id of t as text) is \"%@\" then\n"
				 "          if (URL of t) contains \"%@\" then\n"
				 "            close t\n"
				 "            return \"closed\"\n"
				 "          end if\n"
				 "          return \"kept\"\n"
				 "        end if\n"
				 "      end repeat\n"
				 "    end if\n"
				 "  end repeat\n"
				 "  return \"none\"\n"
				 "end tell", bid, wid, tid, frag];
		}
		NSString *result = activitymon_browser_script(script);
		if (!result) return false;
		return [result isEqualToString:@"closed"];
	}
}

// The bundle identifier of the app that opens https links.
static char *default_browser_bundle(void) {
	@autoreleasepool {
		NSURL *probe = [NSURL URLWithString:@"https://example.com/"];
		NSURL *appURL = [[NSWorkspace sharedWorkspace] URLForApplicationToOpenURL:probe];
		if (appURL == nil) return NULL;
		NSBundle *bundle = [NSBundle bundleWithURL:appURL];
		NSString *bid = bundle.bundleIdentifier;
		if (!bid || bid.length == 0) return NULL;
		return strdup([bid UTF8String]);
	}
}

// Bring the app with this bundle identifier to the front. Returns true when
// a running app was asked to activate.
static bool activate_app(const char *bundleID) {
	@autoreleasepool {
		NSString *bid = [NSString stringWithUTF8String:bundleID];
		NSArray<NSRunningApplication *> *apps = [NSRunningApplication runningApplicationsWithBundleIdentifier:bid];
		if (apps.count == 0) return false;
		NSRunningApplication *app = apps.firstObject;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
		return [app activateWithOptions:NSApplicationActivateIgnoringOtherApps];
#pragma clang diagnostic pop
	}
}
*/
import "C"
import (
	"strings"
	"unsafe"
)

// CloseBrowserTabIfURL closes the frontmost window's active tab in the
// browser with this bundle id when the tab's URL contains fragment, and
// reports whether it did. Only Chromium browsers and Safari can be asked.
func CloseBrowserTabIfURL(bundleID, fragment string) bool {
	if !IsBrowser(bundleID) || strings.TrimSpace(fragment) == "" {
		return false
	}
	cBundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundle))
	cFragment := C.CString(fragment)
	defer C.free(unsafe.Pointer(cFragment))
	return bool(C.close_browser_tab_if_url(cBundle, cFragment))
}

// BrowserActiveTabRef identifies the active tab of the browser's front
// window ("chromium:<window id>:<tab id>" or "safari:<window id>:<index>"),
// or "" when the browser cannot be asked.
func BrowserActiveTabRef(bundleID string) string {
	if !IsBrowser(bundleID) {
		return ""
	}
	cBundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundle))
	ref := C.browser_active_tab_ref(cBundle)
	if ref == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(ref))
	return C.GoString(ref)
}

// CloseBrowserTabRef closes the tab a BrowserActiveTabRef names when its URL
// still contains fragment, and reports whether it did.
func CloseBrowserTabRef(bundleID, ref, fragment string) bool {
	parts := strings.SplitN(ref, ":", 3)
	if len(parts) != 3 || !IsBrowser(bundleID) || strings.TrimSpace(fragment) == "" {
		return false
	}
	cBundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundle))
	cWindow := C.CString(parts[1])
	defer C.free(unsafe.Pointer(cWindow))
	cTab := C.CString(parts[2])
	defer C.free(unsafe.Pointer(cTab))
	cFragment := C.CString(fragment)
	defer C.free(unsafe.Pointer(cFragment))
	return bool(C.close_browser_tab_ref(cBundle, cWindow, cTab, cFragment, C.bool(parts[0] == "safari")))
}

// DefaultBrowserBundleID is the app that opens https links, or "".
func DefaultBrowserBundleID() string {
	bid := C.default_browser_bundle()
	if bid == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(bid))
	return C.GoString(bid)
}

// BrowserScriptable reports whether a browser can be asked for its tabs over
// AppleScript: Chromium browsers and Safari, not Firefox.
func BrowserScriptable(bundleID string) bool {
	switch {
	case bundleID == "com.google.Chrome", bundleID == "com.brave.Browser", bundleID == "com.microsoft.edgemac",
		bundleID == "com.vivaldi.Vivaldi", strings.HasPrefix(bundleID, "company.thebrowser.Browser"),
		bundleID == "com.apple.Safari", bundleID == "com.apple.SafariTechnologyPreview":
		return true
	default:
		return false
	}
}

// ActivateApp brings a running app to the front by bundle id.
func ActivateApp(bundleID string) bool {
	if strings.TrimSpace(bundleID) == "" {
		return false
	}
	cBundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundle))
	return bool(C.activate_app(cBundle))
}
