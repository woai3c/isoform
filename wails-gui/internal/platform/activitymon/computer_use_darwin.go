//go:build darwin

package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework Cocoa -framework CoreGraphics

#include <ApplicationServices/ApplicationServices.h>
#include "main_queue_darwin.h"
#import <Cocoa/Cocoa.h>
#include <CoreGraphics/CoreGraphics.h>
#include <string.h>
#include <stdbool.h>

// ── Constants ────────────────────────────────────────────────────────

#define AUTO_MAX_DEPTH    12
#define AUTO_MAX_ELEMENTS 400
#define AUTO_MAX_CHILDREN 50

// ── Element struct ───────────────────────────────────────────────────

typedef struct {
	char role[64];
	char title[512];
	char label[512];
	char value[1024];
	char description[512];
	char roleDescription[128];
	char help[512];
	char identifier[256];
	bool isActionable;
	double x, y, w, h;
} CAutoElement;

// ── Walk state ───────────────────────────────────────────────────────

typedef struct {
	CAutoElement* elements;
	int           count;
	int           maxElements;
	int           maxDepth;
} CAutoWalkState;

// ── Attribute helpers ────────────────────────────────────────────────

static bool auto_get_str(AXUIElementRef el, CFStringRef attr, char* out, int outLen) {
	out[0] = '\0';
	CFTypeRef ref = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &ref);
	if (err != kAXErrorSuccess || !ref) return false;
	bool ok = false;
	if (CFGetTypeID(ref) == CFStringGetTypeID()) {
		CFStringRef str = (CFStringRef)ref;
		if (CFStringGetLength(str) > 0)
			ok = CFStringGetCString(str, out, outLen, kCFStringEncodingUTF8);
	}
	CFRelease(ref);
	return ok;
}

static bool auto_get_bool(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef ref = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &ref);
	if (err != kAXErrorSuccess || !ref) return false;
	bool val = false;
	if (CFGetTypeID(ref) == CFBooleanGetTypeID()) {
		val = CFBooleanGetValue(ref);
	}
	CFRelease(ref);
	return val;
}

static void auto_get_frame(AXUIElementRef el, double* x, double* y, double* w, double* h) {
	*x = 0; *y = 0; *w = 0; *h = 0;

	CFTypeRef posRef = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXPositionAttribute, &posRef) == kAXErrorSuccess && posRef) {
		CGPoint pt;
		if (AXValueGetValue(posRef, kAXValueCGPointType, &pt)) {
			*x = pt.x;
			*y = pt.y;
		}
		CFRelease(posRef);
	}

	CFTypeRef sizeRef = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXSizeAttribute, &sizeRef) == kAXErrorSuccess && sizeRef) {
		CGSize sz;
		if (AXValueGetValue(sizeRef, kAXValueCGSizeType, &sz)) {
			*w = sz.width;
			*h = sz.height;
		}
		CFRelease(sizeRef);
	}
}

// ── Roles to skip ────────────────────────────────────────────────────

static bool auto_should_skip(const char* role) {
	return strcmp(role, "AXScrollBar") == 0 ||
	       strcmp(role, "AXSplitter") == 0 ||
	       strcmp(role, "AXGrowArea") == 0 ||
	       strcmp(role, "AXRuler") == 0 ||
	       strcmp(role, "AXRulerMarker") == 0 ||
	       strcmp(role, "AXBusyIndicator") == 0 ||
	       strcmp(role, "AXProgressIndicator") == 0 ||
	       strcmp(role, "AXUnknown") == 0;
}

// ── Collect one element ──────────────────────────────────────────────

static void auto_collect(AXUIElementRef el, const char* role, CAutoWalkState* st) {
	if (st->count >= st->maxElements) return;

	double x, y, w, h;
	auto_get_frame(el, &x, &y, &w, &h);
	// Skip elements smaller than 5x5 pixels (invisible/decorative).
	if (w < 5.0 && h < 5.0) return;

	CAutoElement* elem = &st->elements[st->count];
	memset(elem, 0, sizeof(CAutoElement));

	strncpy(elem->role, role, sizeof(elem->role) - 1);
	auto_get_str(el, kAXTitleAttribute, elem->title, sizeof(elem->title));
	auto_get_str(el, kAXDescriptionAttribute, elem->label, sizeof(elem->label));
	auto_get_str(el, kAXValueAttribute, elem->value, sizeof(elem->value));
	auto_get_str(el, kAXRoleDescriptionAttribute, elem->roleDescription, sizeof(elem->roleDescription));
	auto_get_str(el, kAXHelpAttribute, elem->help, sizeof(elem->help));

	// Description field: use AXDescription, fall back to label.
	auto_get_str(el, kAXDescriptionAttribute, elem->description, sizeof(elem->description));

	// Identifier
	auto_get_str(el, CFSTR("AXIdentifier"), elem->identifier, sizeof(elem->identifier));

	// Actionable: element is enabled (or has no enabled attribute, meaning it's static text).
	CFTypeRef enabledRef = NULL;
	AXError enabledErr = AXUIElementCopyAttributeValue(el, CFSTR("AXEnabled"), &enabledRef);
	if (enabledErr == kAXErrorSuccess && enabledRef) {
		if (CFGetTypeID(enabledRef) == CFBooleanGetTypeID()) {
			elem->isActionable = CFBooleanGetValue(enabledRef);
		}
		CFRelease(enabledRef);
	} else {
		// No enabled attribute — check if the role implies actionability.
		elem->isActionable = (strcmp(role, "AXButton") == 0 ||
		                      strcmp(role, "AXLink") == 0 ||
		                      strcmp(role, "AXMenuItem") == 0 ||
		                      strcmp(role, "AXCheckBox") == 0 ||
		                      strcmp(role, "AXRadioButton") == 0 ||
		                      strcmp(role, "AXPopUpButton") == 0 ||
		                      strcmp(role, "AXMenuButton") == 0 ||
		                      strcmp(role, "AXTextField") == 0 ||
		                      strcmp(role, "AXTextArea") == 0 ||
		                      strcmp(role, "AXComboBox") == 0 ||
		                      strcmp(role, "AXSlider") == 0 ||
		                      strcmp(role, "AXTab") == 0);
	}

	elem->x = x;
	elem->y = y;
	elem->w = w;
	elem->h = h;

	st->count++;
}

// ── Recursive tree walk ──────────────────────────────────────────────

static void auto_walk(AXUIElementRef el, int depth, CAutoWalkState* st) {
	if (depth >= st->maxDepth || st->count >= st->maxElements) return;

	AXUIElementSetMessagingTimeout(el, 0.1f);

	char role[64] = {0};
	if (!auto_get_str(el, kAXRoleAttribute, role, sizeof(role))) return;
	if (auto_should_skip(role)) return;

	auto_collect(el, role, st);

	if (st->count >= st->maxElements) return;

	CFArrayRef children = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, (CFTypeRef*)&children);
	if (err != kAXErrorSuccess || !children) return;

	CFIndex count = CFArrayGetCount(children);
	if (count > AUTO_MAX_CHILDREN) count = AUTO_MAX_CHILDREN;
	for (CFIndex i = 0; i < count; i++) {
		if (st->count >= st->maxElements) break;
		AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
		if (child) auto_walk(child, depth + 1, st);
	}
	CFRelease(children);
}

// ── Try focusing AXWebArea to expose browser page elements ───────────

static void auto_focus_web_area(AXUIElementRef window) {
	CFArrayRef children = NULL;
	AXError err = AXUIElementCopyAttributeValue(window, kAXChildrenAttribute, (CFTypeRef*)&children);
	if (err != kAXErrorSuccess || !children) return;

	CFIndex count = CFArrayGetCount(children);
	for (CFIndex i = 0; i < count; i++) {
		AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
		if (!child) continue;

		char role[64] = {0};
		if (auto_get_str(child, kAXRoleAttribute, role, sizeof(role))) {
			if (strcmp(role, "AXWebArea") == 0) {
				// Focus the web area to expose page elements.
				AXUIElementPerformAction(child, kAXPressAction);
				return;
			}
		}

		// Recurse one level (web area is usually a few levels deep).
		CFArrayRef grandchildren = NULL;
		if (AXUIElementCopyAttributeValue(child, kAXChildrenAttribute, (CFTypeRef*)&grandchildren) == kAXErrorSuccess && grandchildren) {
			CFIndex gc = CFArrayGetCount(grandchildren);
			for (CFIndex j = 0; j < gc; j++) {
				AXUIElementRef grandchild = (AXUIElementRef)CFArrayGetValueAtIndex(grandchildren, j);
				if (!grandchild) continue;
				char grole[64] = {0};
				if (auto_get_str(grandchild, kAXRoleAttribute, grole, sizeof(grole))) {
					if (strcmp(grole, "AXWebArea") == 0) {
						AXUIElementPerformAction(grandchild, kAXPressAction);
						CFRelease(grandchildren);
						CFRelease(children);
						return;
					}
				}
			}
			CFRelease(grandchildren);
		}
	}
	CFRelease(children);
}

// ── Public: collect elements from a named app ────────────────────────

// auto_collect_app_elements finds a running app by name (case-insensitive
// substring match), walks its focused window's accessibility tree, and
// writes structured elements to the output buffer.
// Returns the number of elements collected.
static int auto_collect_app_elements(const char* appName, CAutoElement* out, int maxElements) {
	@autoreleasepool {
		CAutoWalkState st = { .elements = out, .count = 0,
		                      .maxElements = maxElements, .maxDepth = AUTO_MAX_DEPTH };

		// Find the app by name.
		NSString* target = [NSString stringWithUTF8String:appName];
		NSArray<NSRunningApplication*>* apps = activitymon_running_apps();
		NSRunningApplication* found = nil;
		for (NSRunningApplication* app in apps) {
			NSString* name = app.localizedName;
			if (name && [name localizedCaseInsensitiveContainsString:target]) {
				found = app;
				break;
			}
		}
		if (!found) return 0;

		pid_t pid = found.processIdentifier;
		AXUIElementRef appEl = AXUIElementCreateApplication(pid);
		if (!appEl) return 0;
		AXUIElementSetMessagingTimeout(appEl, 0.5f);

		// Get the focused window.
		AXUIElementRef win = NULL;
		AXError err = AXUIElementCopyAttributeValue(appEl, kAXFocusedWindowAttribute, (CFTypeRef*)&win);
		if (err != kAXErrorSuccess || !win) {
			// Fallback: try first window from windows array.
			CFArrayRef windows = NULL;
			err = AXUIElementCopyAttributeValue(appEl, kAXWindowsAttribute, (CFTypeRef*)&windows);
			if (err == kAXErrorSuccess && windows && CFArrayGetCount(windows) > 0) {
				win = (AXUIElementRef)CFRetain(CFArrayGetValueAtIndex(windows, 0));
			}
			if (windows) CFRelease(windows);
		}

		if (!win) {
			CFRelease(appEl);
			return 0;
		}

		// Try to focus web area for browsers (exposes page DOM via accessibility).
		auto_focus_web_area(win);

		// Walk the tree.
		auto_walk(win, 0, &st);

		CFRelease(win);
		CFRelease(appEl);
		return st.count;
	}
}

// ── Public: list running apps ────────────────────────────────────────

typedef struct {
	char name[256];
	char bundleID[256];
	int  pid;
	bool isActive;
} CRunningApp;

static int auto_list_apps(CRunningApp* out, int maxApps) {
	@autoreleasepool {
		NSArray<NSRunningApplication*>* apps = activitymon_running_apps();
		int count = 0;
		for (NSRunningApplication* app in apps) {
			if (count >= maxApps) break;
			// Skip background-only apps.
			if (app.activationPolicy != NSApplicationActivationPolicyRegular) continue;

			CRunningApp* entry = &out[count];
			memset(entry, 0, sizeof(CRunningApp));

			NSString* name = app.localizedName;
			if (name) [name getCString:entry->name maxLength:sizeof(entry->name) encoding:NSUTF8StringEncoding];

			NSString* bid = app.bundleIdentifier;
			if (bid) [bid getCString:entry->bundleID maxLength:sizeof(entry->bundleID) encoding:NSUTF8StringEncoding];

			entry->pid = (int)app.processIdentifier;
			entry->isActive = app.isActive;
			count++;
		}
		return count;
	}
}

// ── Public: list windows for an app ──────────────────────────────────

typedef struct {
	char title[512];
	int  windowID;
	double x, y, w, h;
	bool isMain;
} CAppWindow;

static int auto_list_windows(const char* appName, CAppWindow* out, int maxWindows) {
	@autoreleasepool {
		NSString* target = [NSString stringWithUTF8String:appName];
		NSArray<NSRunningApplication*>* apps = activitymon_running_apps();
		NSRunningApplication* found = nil;
		for (NSRunningApplication* app in apps) {
			NSString* name = app.localizedName;
			if (name && [name localizedCaseInsensitiveContainsString:target]) {
				found = app;
				break;
			}
		}
		if (!found) return 0;

		pid_t pid = found.processIdentifier;
		AXUIElementRef appEl = AXUIElementCreateApplication(pid);
		if (!appEl) return 0;
		AXUIElementSetMessagingTimeout(appEl, 0.5f);

		CFArrayRef windows = NULL;
		AXError err = AXUIElementCopyAttributeValue(appEl, kAXWindowsAttribute, (CFTypeRef*)&windows);
		if (err != kAXErrorSuccess || !windows) {
			CFRelease(appEl);
			return 0;
		}

		int count = 0;
		CFIndex wc = CFArrayGetCount(windows);
		for (CFIndex i = 0; i < wc && count < maxWindows; i++) {
			AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
			if (!win) continue;

			CAppWindow* entry = &out[count];
			memset(entry, 0, sizeof(CAppWindow));
			auto_get_str(win, kAXTitleAttribute, entry->title, sizeof(entry->title));

			double x, y, w, h;
			auto_get_frame(win, &x, &y, &w, &h);
			entry->x = x;
			entry->y = y;
			entry->w = w;
			entry->h = h;

			// Check if this is the main window.
			CFTypeRef mainRef = NULL;
			if (AXUIElementCopyAttributeValue(win, CFSTR("AXMain"), &mainRef) == kAXErrorSuccess && mainRef) {
				if (CFGetTypeID(mainRef) == CFBooleanGetTypeID()) {
					entry->isMain = CFBooleanGetValue(mainRef);
				}
				CFRelease(mainRef);
			}

			count++;
		}

		CFRelease(windows);
		CFRelease(appEl);
		return count;
	}
}

// ── Public: open URL in default browser ──────────────────────────────

static bool auto_open_url(const char* urlStr) {
	@autoreleasepool {
		NSString* ns = [NSString stringWithUTF8String:urlStr];
		NSURL* url = [NSURL URLWithString:ns];
		if (!url) return false;
		return [[NSWorkspace sharedWorkspace] openURL:url];
	}
}

// ── Public: click element by coordinates ─────────────────────────────

static void auto_click_at(double x, double y) {
	CGPoint point = CGPointMake(x, y);
	CGEventRef mouseDown = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, point, kCGMouseButtonLeft);
	CGEventRef mouseUp = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, point, kCGMouseButtonLeft);
	if (mouseDown) {
		CGEventPost(kCGHIDEventTap, mouseDown);
		CFRelease(mouseDown);
	}
	if (mouseUp) {
		// Small delay for natural click.
		usleep(50000); // 50ms
		CGEventPost(kCGHIDEventTap, mouseUp);
		CFRelease(mouseUp);
	}
}

// ── Public: type text ────────────────────────────────────────────────

static void auto_type_text(const char* text) {
	@autoreleasepool {
		NSString* str = [NSString stringWithUTF8String:text];
		for (NSUInteger i = 0; i < str.length; i++) {
			unichar ch = [str characterAtIndex:i];

			CGEventRef keyDown = CGEventCreateKeyboardEvent(NULL, 0, true);
			CGEventRef keyUp = CGEventCreateKeyboardEvent(NULL, 0, false);
			if (keyDown && keyUp) {
				CGEventKeyboardSetUnicodeString(keyDown, 1, &ch);
				CGEventKeyboardSetUnicodeString(keyUp, 1, &ch);
				CGEventPost(kCGHIDEventTap, keyDown);
				usleep(10000); // 10ms between keystrokes
				CGEventPost(kCGHIDEventTap, keyUp);
			}
			if (keyDown) CFRelease(keyDown);
			if (keyUp) CFRelease(keyUp);
		}
	}
}

// ── Public: press a single key ───────────────────────────────────────

// Key name → CGKeyCode mapping for common keys.
static CGKeyCode auto_keycode(const char* keyName) {
	if (strcasecmp(keyName, "return") == 0 || strcasecmp(keyName, "enter") == 0) return 36;
	if (strcasecmp(keyName, "tab") == 0) return 48;
	if (strcasecmp(keyName, "space") == 0) return 49;
	if (strcasecmp(keyName, "delete") == 0 || strcasecmp(keyName, "backspace") == 0) return 51;
	if (strcasecmp(keyName, "escape") == 0 || strcasecmp(keyName, "esc") == 0) return 53;
	if (strcasecmp(keyName, "left") == 0) return 123;
	if (strcasecmp(keyName, "right") == 0) return 124;
	if (strcasecmp(keyName, "down") == 0) return 125;
	if (strcasecmp(keyName, "up") == 0) return 126;
	if (strcasecmp(keyName, "f1") == 0) return 122;
	if (strcasecmp(keyName, "f2") == 0) return 120;
	if (strcasecmp(keyName, "f3") == 0) return 101;
	if (strcasecmp(keyName, "f4") == 0) return 118;
	if (strcasecmp(keyName, "f5") == 0) return 96;
	if (strcasecmp(keyName, "home") == 0) return 115;
	if (strcasecmp(keyName, "end") == 0) return 119;
	if (strcasecmp(keyName, "pageup") == 0) return 116;
	if (strcasecmp(keyName, "pagedown") == 0) return 121;
	if (strcasecmp(keyName, "forwarddelete") == 0) return 117;
	// Single character keys.
	if (strlen(keyName) == 1) {
		// For letter keys, we use the Unicode character approach via type_text.
		return 0xFF; // sentinel: use unicode path
	}
	return 0xFF; // unknown
}

static void auto_press_key(const char* keyName) {
	CGKeyCode code = auto_keycode(keyName);
	if (code == 0xFF) {
		// Fallback: type the key as text.
		if (strlen(keyName) == 1) {
			auto_type_text(keyName);
		}
		return;
	}
	CGEventRef keyDown = CGEventCreateKeyboardEvent(NULL, code, true);
	CGEventRef keyUp = CGEventCreateKeyboardEvent(NULL, code, false);
	if (keyDown) { CGEventPost(kCGHIDEventTap, keyDown); CFRelease(keyDown); }
	usleep(30000);
	if (keyUp) { CGEventPost(kCGHIDEventTap, keyUp); CFRelease(keyUp); }
}

// ── Public: hotkey (key combo with modifiers) ────────────────────────

static void auto_hotkey(const char* combo) {
	// Parse comma-separated combo: "cmd,c" or "cmd,shift,a"
	CGEventFlags flags = 0;
	char keyCopy[256] = {0};
	strncpy(keyCopy, combo, sizeof(keyCopy) - 1);

	char* lastKey = NULL;
	char* saveptr = NULL;
	char* token = strtok_r(keyCopy, ",", &saveptr);
	while (token) {
		// Trim whitespace.
		while (*token == ' ') token++;

		if (strcasecmp(token, "cmd") == 0 || strcasecmp(token, "command") == 0) {
			flags |= kCGEventFlagMaskCommand;
		} else if (strcasecmp(token, "shift") == 0) {
			flags |= kCGEventFlagMaskShift;
		} else if (strcasecmp(token, "alt") == 0 || strcasecmp(token, "option") == 0) {
			flags |= kCGEventFlagMaskAlternate;
		} else if (strcasecmp(token, "ctrl") == 0 || strcasecmp(token, "control") == 0) {
			flags |= kCGEventFlagMaskControl;
		} else {
			lastKey = token;
		}
		token = strtok_r(NULL, ",", &saveptr);
	}

	if (!lastKey) return;

	CGKeyCode code = auto_keycode(lastKey);
	if (code == 0xFF && strlen(lastKey) == 1) {
		// For single character keys with modifiers, use keycode 0 + unicode.
		unichar ch = lastKey[0];
		CGEventRef keyDown = CGEventCreateKeyboardEvent(NULL, 0, true);
		CGEventRef keyUp = CGEventCreateKeyboardEvent(NULL, 0, false);
		if (keyDown && keyUp) {
			CGEventKeyboardSetUnicodeString(keyDown, 1, &ch);
			CGEventKeyboardSetUnicodeString(keyUp, 1, &ch);
			CGEventSetFlags(keyDown, flags);
			CGEventSetFlags(keyUp, flags);
			CGEventPost(kCGHIDEventTap, keyDown);
			usleep(30000);
			CGEventPost(kCGHIDEventTap, keyUp);
		}
		if (keyDown) CFRelease(keyDown);
		if (keyUp) CFRelease(keyUp);
		return;
	}

	if (code == 0xFF) return;

	CGEventRef keyDown = CGEventCreateKeyboardEvent(NULL, code, true);
	CGEventRef keyUp = CGEventCreateKeyboardEvent(NULL, code, false);
	if (keyDown && keyUp) {
		CGEventSetFlags(keyDown, flags);
		CGEventSetFlags(keyUp, flags);
		CGEventPost(kCGHIDEventTap, keyDown);
		usleep(30000);
		CGEventPost(kCGHIDEventTap, keyUp);
	}
	if (keyDown) CFRelease(keyDown);
	if (keyUp) CFRelease(keyUp);
}

// ── Public: scroll ───────────────────────────────────────────────────

static void auto_scroll(int deltaX, int deltaY) {
	CGEventRef event = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, deltaY, deltaX);
	if (event) {
		CGEventPost(kCGHIDEventTap, event);
		CFRelease(event);
	}
}

// ── Public: drag ─────────────────────────────────────────────────────

static void auto_drag(double fromX, double fromY, double toX, double toY) {
	CGPoint from = CGPointMake(fromX, fromY);
	CGPoint to = CGPointMake(toX, toY);

	CGEventRef mouseDown = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, from, kCGMouseButtonLeft);
	if (mouseDown) {
		CGEventPost(kCGHIDEventTap, mouseDown);
		CFRelease(mouseDown);
	}
	usleep(100000); // 100ms hold

	// Interpolate a few drag events for smooth motion.
	int steps = 10;
	for (int i = 1; i <= steps; i++) {
		double t = (double)i / (double)steps;
		CGPoint pt = CGPointMake(fromX + (toX - fromX) * t, fromY + (toY - fromY) * t);
		CGEventRef drag = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDragged, pt, kCGMouseButtonLeft);
		if (drag) {
			CGEventPost(kCGHIDEventTap, drag);
			CFRelease(drag);
		}
		usleep(20000); // 20ms per step
	}

	CGEventRef mouseUp = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, to, kCGMouseButtonLeft);
	if (mouseUp) {
		CGEventPost(kCGHIDEventTap, mouseUp);
		CFRelease(mouseUp);
	}
}

// ── Public: atomic paste (set clipboard + Cmd+V + restore) ───────────

static void auto_paste_text(const char* text) {
	@autoreleasepool {
		NSPasteboard* pb = [NSPasteboard generalPasteboard];

		// Save current clipboard text (non-text content is not preserved).
		NSString* oldText = [pb stringForType:NSPasteboardTypeString];

		// Set new text.
		[pb clearContents];
		[pb setString:[NSString stringWithUTF8String:text] forType:NSPasteboardTypeString];

		// Press Cmd+V.
		usleep(50000); // 50ms for clipboard to settle
		auto_hotkey("cmd,v");
		usleep(100000); // 100ms for paste to complete

		// Restore previous clipboard.
		if (oldText) {
			[pb clearContents];
			[pb setString:oldText forType:NSPasteboardTypeString];
		}
	}
}

// ── Public: check accessibility permission ───────────────────────────

static bool auto_check_accessibility(void) {
	CFStringRef keys[1] = {kAXTrustedCheckOptionPrompt};
	CFTypeRef values[1] = {kCFBooleanFalse}; // Don't prompt, just check.

	CFDictionaryRef options = CFDictionaryCreate(
		NULL,
		(const void **)keys,
		(const void **)values,
		1,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);

	bool trusted = AXIsProcessTrustedWithOptions(options) != 0;
	if (options != NULL) {
		CFRelease(options);
	}
	return trusted;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"wails-gui/internal/platform/clipboard"
)

// ComputerUseSupported returns true on macOS where accessibility APIs exist.
func ComputerUseSupported() bool { return true }

// ComputerUsePermitted checks whether macOS Accessibility permission has been
// granted to this process. Uses AXIsProcessTrustedWithOptions without
// prompting (kAXTrustedCheckOptionPrompt = false).
func ComputerUsePermitted() bool {
	return bool(C.auto_check_accessibility())
}

// CollectAppElements walks the accessibility tree of the named app and
// returns structured UI elements. The app is matched by case-insensitive
// substring on its display name (e.g., "Safari", "Chrome", "Finder").
func CollectAppElements(appName string) []UIElement {
	cName := C.CString(appName)
	defer C.free(unsafe.Pointer(cName))

	const maxElements = 400
	buf := (*C.CAutoElement)(C.malloc(C.size_t(maxElements) * C.size_t(unsafe.Sizeof(C.CAutoElement{}))))
	if buf == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(buf))

	count := int(C.auto_collect_app_elements(cName, buf, C.int(maxElements)))
	if count <= 0 {
		return nil
	}

	elements := make([]UIElement, count)
	for i := 0; i < count; i++ {
		ptr := (*C.CAutoElement)(unsafe.Pointer(uintptr(unsafe.Pointer(buf)) + uintptr(i)*unsafe.Sizeof(C.CAutoElement{})))
		elements[i] = UIElement{
			ID:              fmt.Sprintf("elem_%d", i),
			Role:            C.GoString(&ptr.role[0]),
			Title:           C.GoString(&ptr.title[0]),
			Label:           C.GoString(&ptr.label[0]),
			Value:           C.GoString(&ptr.value[0]),
			Description:     C.GoString(&ptr.description[0]),
			RoleDescription: C.GoString(&ptr.roleDescription[0]),
			Help:            C.GoString(&ptr.help[0]),
			Identifier:      C.GoString(&ptr.identifier[0]),
			IsActionable:    bool(ptr.isActionable),
			X:               float64(ptr.x),
			Y:               float64(ptr.y),
			Width:           float64(ptr.w),
			Height:          float64(ptr.h),
		}
	}
	return elements
}

// ListRunningApps returns all running regular (non-background) applications.
func ListRunningApps() []RunningApp {
	const maxApps = 100
	buf := (*C.CRunningApp)(C.malloc(C.size_t(maxApps) * C.size_t(unsafe.Sizeof(C.CRunningApp{}))))
	if buf == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(buf))

	count := int(C.auto_list_apps(buf, C.int(maxApps)))
	if count <= 0 {
		return nil
	}

	apps := make([]RunningApp, count)
	for i := 0; i < count; i++ {
		ptr := (*C.CRunningApp)(unsafe.Pointer(uintptr(unsafe.Pointer(buf)) + uintptr(i)*unsafe.Sizeof(C.CRunningApp{})))
		apps[i] = RunningApp{
			Name:     C.GoString(&ptr.name[0]),
			BundleID: C.GoString(&ptr.bundleID[0]),
			PID:      int(ptr.pid),
			IsActive: bool(ptr.isActive),
		}
	}
	return apps
}

// ListAppWindows returns all windows for the named app.
func ListAppWindows(appName string) []AppWindow {
	cName := C.CString(appName)
	defer C.free(unsafe.Pointer(cName))

	const maxWindows = 50
	buf := (*C.CAppWindow)(C.malloc(C.size_t(maxWindows) * C.size_t(unsafe.Sizeof(C.CAppWindow{}))))
	if buf == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(buf))

	count := int(C.auto_list_windows(cName, buf, C.int(maxWindows)))
	if count <= 0 {
		return nil
	}

	windows := make([]AppWindow, count)
	for i := 0; i < count; i++ {
		ptr := (*C.CAppWindow)(unsafe.Pointer(uintptr(unsafe.Pointer(buf)) + uintptr(i)*unsafe.Sizeof(C.CAppWindow{})))
		windows[i] = AppWindow{
			Title:  C.GoString(&ptr.title[0]),
			X:      float64(ptr.x),
			Y:      float64(ptr.y),
			Width:  float64(ptr.w),
			Height: float64(ptr.h),
			IsMain: bool(ptr.isMain),
		}
	}
	return windows
}

// OpenURL opens a URL in the user's default browser.
func OpenURL(url string) bool {
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	return bool(C.auto_open_url(cURL))
}

// ClickAt clicks at the given screen coordinates.
func ClickAt(x, y float64) {
	C.auto_click_at(C.double(x), C.double(y))
}

// TypeText types the given text character by character.
func TypeText(text string) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	C.auto_type_text(cText)
}

// PressKey presses a named key (e.g., "return", "tab", "escape", "up").
func PressKey(keyName string) {
	cKey := C.CString(keyName)
	defer C.free(unsafe.Pointer(cKey))
	C.auto_press_key(cKey)
}

// Hotkey presses a key combination (e.g., "cmd,c" for Cmd+C, "cmd,shift,a").
func Hotkey(combo string) {
	cCombo := C.CString(combo)
	defer C.free(unsafe.Pointer(cCombo))
	C.auto_hotkey(cCombo)
}

// Scroll scrolls by the given delta (positive = up/right, negative = down/left).
func Scroll(deltaX, deltaY int) {
	C.auto_scroll(C.int(deltaX), C.int(deltaY))
}

// Drag performs a drag from (fromX, fromY) to (toX, toY).
func Drag(fromX, fromY, toX, toY float64) {
	C.auto_drag(C.double(fromX), C.double(fromY), C.double(toX), C.double(toY))
}

// PasteText atomically pastes text: sets clipboard, presses Cmd+V, restores
// clipboard. The clipboard watcher counts both changes as Isoform's own.
func PasteText(text string) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	clipboard.OwnWrites(func() { C.auto_paste_text(cText) })
}
