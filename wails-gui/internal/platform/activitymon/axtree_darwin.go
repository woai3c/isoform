//go:build darwin

package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework Cocoa

#include <ApplicationServices/ApplicationServices.h>
#include <unistd.h>
#include "main_queue_darwin.h"
#import <Cocoa/Cocoa.h>
#include <string.h>
#include <stdbool.h>

// Flat-text walker budget. Same rationale as the JSON tree walker (see
// axtree_tree_darwin.go): bumped because Electron chat apps systematically
// truncated before the message list. The flat-text path also feeds
// fallback parsers (Terminal/Browser chrome stripper) and powers the
// "无障碍文本" debug panel in activity-page, so a fuller capture pays off
// even when the structured parser misses.
#define AXWALK_MAX_DEPTH 25
#define AXWALK_MAX_NODES 10000

typedef struct {
	char* buf;
	int   bufLen;
	int   nodeCount;
	int   maxNodes;
	int   maxDepth;
} AXWalkState;

// Roles to skip entirely (decorative/irrelevant) — matches screenpipe's should_skip_role.
static bool axwalk_should_skip(const char* role) {
	return strcmp(role, "AXScrollBar") == 0 ||
	       strcmp(role, "AXImage") == 0 ||
	       strcmp(role, "AXSplitter") == 0 ||
	       strcmp(role, "AXGrowArea") == 0 ||
	       strcmp(role, "AXMenuBar") == 0 ||
	       strcmp(role, "AXMenu") == 0 ||
	       strcmp(role, "AXToolbar") == 0 ||
	       strcmp(role, "AXSecureTextField") == 0 ||
	       strcmp(role, "AXMenuBarItem") == 0 ||
	       strcmp(role, "AXRuler") == 0 ||
	       strcmp(role, "AXRulerMarker") == 0 ||
	       strcmp(role, "AXBusyIndicator") == 0 ||
	       strcmp(role, "AXProgressIndicator") == 0;
}

// Roles that hold user-visible text — matches screenpipe's should_extract_text.
static bool axwalk_should_extract(const char* role) {
	return strcmp(role, "AXStaticText") == 0 ||
	       strcmp(role, "AXTextField") == 0 ||
	       strcmp(role, "AXTextArea") == 0 ||
	       strcmp(role, "AXButton") == 0 ||
	       strcmp(role, "AXMenuItem") == 0 ||
	       strcmp(role, "AXCell") == 0 ||
	       strcmp(role, "AXHeading") == 0 ||
	       strcmp(role, "AXLink") == 0 ||
	       strcmp(role, "AXMenuButton") == 0 ||
	       strcmp(role, "AXPopUpButton") == 0 ||
	       strcmp(role, "AXComboBox") == 0 ||
	       strcmp(role, "AXCheckBox") == 0 ||
	       strcmp(role, "AXRadioButton") == 0 ||
	       strcmp(role, "AXDisclosureTriangle") == 0 ||
	       strcmp(role, "AXTab") == 0;
}

// Append trimmed text with newline separator — matches screenpipe's append_text.
static void axwalk_append(AXWalkState* st, const char* text) {
	if (!text || !text[0]) return;
	// Trim leading whitespace
	const char* s = text;
	while (*s == ' ' || *s == '\t' || *s == '\n' || *s == '\r') s++;
	if (*s == '\0') return;
	// Trim trailing whitespace
	int tLen = (int)strlen(s);
	while (tLen > 0 && (s[tLen-1]==' '||s[tLen-1]=='\t'||s[tLen-1]=='\n'||s[tLen-1]=='\r')) tLen--;
	if (tLen == 0) return;

	int cur = (int)strlen(st->buf);
	if (cur >= st->bufLen - 2) return;
	// Newline separator between entries (matches screenpipe)
	if (cur > 0) { strncat(st->buf, "\n", st->bufLen - cur - 1); cur++; }
	int rem = st->bufLen - cur - 1;
	if (tLen > rem) tLen = rem;
	strncat(st->buf, s, tLen);
}

// Get a string attribute from an AX element.
static bool axwalk_get_str(AXUIElementRef el, CFStringRef attr, char* out, int outLen) {
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

// Extract text from an element — matches screenpipe's extract_text.
// Priority: for text fields/areas → Value; for static text → Value; fallback → Title → Description.
static void axwalk_extract(AXUIElementRef el, const char* role, AXWalkState* st) {
	char tmp[1024];
	// Text fields / text areas / combo boxes: prefer Value
	if (strcmp(role,"AXTextField")==0 || strcmp(role,"AXTextArea")==0 || strcmp(role,"AXComboBox")==0) {
		if (axwalk_get_str(el, kAXValueAttribute, tmp, sizeof(tmp))) {
			axwalk_append(st, tmp); return;
		}
	}
	// Static text: prefer Value
	if (strcmp(role,"AXStaticText")==0) {
		if (axwalk_get_str(el, kAXValueAttribute, tmp, sizeof(tmp))) {
			axwalk_append(st, tmp); return;
		}
	}
	// Fallback: Title
	if (axwalk_get_str(el, kAXTitleAttribute, tmp, sizeof(tmp))) {
		axwalk_append(st, tmp); return;
	}
	// Fallback: Description
	if (axwalk_get_str(el, kAXDescriptionAttribute, tmp, sizeof(tmp))) {
		axwalk_append(st, tmp);
	}
}

// Recursive tree walk — matches screenpipe's walk_element.
static void axwalk_walk(AXUIElementRef el, int depth, AXWalkState* st) {
	if (depth >= st->maxDepth || st->nodeCount >= st->maxNodes) return;
	if ((int)strlen(st->buf) >= st->bufLen - 100) return;
	st->nodeCount++;
	AXUIElementSetMessagingTimeout(el, 0.1f);

	char role[128] = {0};
	if (!axwalk_get_str(el, kAXRoleAttribute, role, sizeof(role))) return;
	if (axwalk_should_skip(role)) return;

	if (axwalk_should_extract(role)) {
		axwalk_extract(el, role, st);
	} else if (strcmp(role,"AXGroup")==0 || strcmp(role,"AXWebArea")==0) {
		// Groups and web areas: only extract direct Value (matches screenpipe)
		char tmp[1024];
		if (axwalk_get_str(el, kAXValueAttribute, tmp, sizeof(tmp)))
			axwalk_append(st, tmp);
	}

	if (st->nodeCount >= st->maxNodes) return;

	CFArrayRef children = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, (CFTypeRef*)&children);
	if (err != kAXErrorSuccess || !children) return;
	CFIndex count = CFArrayGetCount(children);
	for (CFIndex i = 0; i < count; i++) {
		if (st->nodeCount >= st->maxNodes) break;
		AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
		if (child) axwalk_walk(child, depth + 1, st);
	}
	CFRelease(children);
}

// Walk focused window's accessibility tree and collect all visible text content.
// Returns the number of bytes written to buf.
static int axwalk_collect_text(char* buf, int bufLen) {
	buf[0] = '\0';
	AXWalkState st = { .buf=buf, .bufLen=bufLen, .nodeCount=0,
	                    .maxNodes=AXWALK_MAX_NODES, .maxDepth=AXWALK_MAX_DEPTH };
	@autoreleasepool {
		NSRunningApplication *front = activitymon_frontmost_app();
		if (!front) return 0;
		// Never walk our own window: WebKit kills a process that queries its
		// own web view's accessibility from a background thread.
		if (front.processIdentifier == getpid()) return 0;

		AXUIElementRef app = AXUIElementCreateApplication(front.processIdentifier);
		if (!app) return 0;
		AXUIElementSetMessagingTimeout(app, 0.1f);

		AXUIElementRef win = NULL;
		AXError err = AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, (CFTypeRef*)&win);
		CFRelease(app);
		if (err != kAXErrorSuccess || !win) return 0;

		axwalk_walk(win, 0, &st);
		CFRelease(win);
	}
	return (int)strlen(buf);
}
*/
import "C"
import "unsafe"

// CollectFocusedWindowText walks the focused window's accessibility tree
// and returns all visible text content. Matches screenpipe's macOS tree walker:
// same role filtering, same text extraction priority, same newline-joined output.
func CollectFocusedWindowText() string {
	// 500KB ceiling. Originally screenpipe's 50KB default — bumped first
	// to 100KB when AXWALK_MAX_NODES went from 5000 → 10000, then to
	// 500KB so the per-node budget (~50 bytes/node) tracks the JSON
	// walker's own ratio (1MB / 8000 nodes = ~128 bytes/node). Empirically
	// most apps stay well under 50KB, but a chatty Linear board / Notion
	// page with thousands of inline text nodes can push past 100KB and
	// the silent truncation makes the "Accessibility text" debug panel
	// misleading. 400KB extra allocation per ~1Hz capture is negligible.
	const maxLen = 500000
	buf := (*C.char)(C.malloc(C.size_t(maxLen)))
	if buf == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(buf))

	n := C.axwalk_collect_text(buf, C.int(maxLen))
	if n <= 0 {
		return ""
	}
	return C.GoStringN(buf, n)
}
