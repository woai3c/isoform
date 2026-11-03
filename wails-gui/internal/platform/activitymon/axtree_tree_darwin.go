//go:build darwin

package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework Cocoa

#include <ApplicationServices/ApplicationServices.h>
#include "main_queue_darwin.h"
#import <Cocoa/Cocoa.h>
#include <string.h>
#include <stdbool.h>
#include <stdlib.h>
#include <unistd.h>
#include <math.h>

// Tree-walker budget. Originally 20 / 2000 — but Electron-based chat apps
// (Slack, Discord, Linear desktop) have very deep, very wide AX trees: the
// workspace switcher + sidebar alone consume ~1500 nodes before the walker
// even reaches the message list, so app-specific parsers (Slack/Discord/etc)
// systematically returned nil because FindFirst couldn't see the message
// list subtree. Bumping to 25/8000 lets Slack's message panel get fully
// traversed without blowing the per-snapshot capture budget. Each extra
// node costs ~5-10 AX IPC calls; capture runs ~1 Hz on a background
// goroutine so the wall-clock ceiling (~0.1s × budget) is acceptable.
//
// 2026-05-25: bumped depth from 25 → 40 after observing Feishu / Lark
// (com.electron.lark) silently dropping its open-chat message bodies.
// Lark nests each rendered message ~28 levels deep (window → BrowserView
// → MultiWebView → AXWebArea → chat-container chain × ~15 levels →
// chatMessages → message-group → sender/body containers → text leaf);
// at depth=25 the walker entered `chatMessages` but bailed on its
// children, so the conversation pane came back as a 1-child stub even
// though the AX tree itself was richly populated. The Feishu parser's
// dual-signal scope is useless when the bodies aren't even in the dump.
//
// Node count is the real ceiling now — depth is essentially free since
// children at depth N still cost the same per-node IPC. Bumping to 40
// gives Lark, Linear, and any future Electron SPA ~12 levels of headroom
// over what we've empirically observed, without adding nodes (the cap
// still limits the dump size).
#define TREE_MAX_DEPTH   40
#define TREE_MAX_NODES   8000

// Buffer used to assemble the JSON representation of the AX tree.
typedef struct {
	char* buf;
	int   bufLen;
	int   bufCap;
	int   nodeCount;
	int   maxNodes;
	int   maxDepth;
} TreeState;

static void tree_append(TreeState* st, const char* s) {
	int n = (int)strlen(s);
	if (st->bufLen + n >= st->bufCap - 1) return;
	memcpy(st->buf + st->bufLen, s, n);
	st->bufLen += n;
	st->buf[st->bufLen] = '\0';
}

// Escape a string for JSON: quotes, backslashes, control chars.
static void tree_append_json_string(TreeState* st, const char* s) {
	if (st->bufLen + 2 >= st->bufCap - 1) return;
	st->buf[st->bufLen++] = '"';
	int n = (int)strlen(s);
	for (int i = 0; i < n; i++) {
		unsigned char c = (unsigned char)s[i];
		if (st->bufLen + 8 >= st->bufCap - 1) break;
		switch (c) {
			case '"':  st->buf[st->bufLen++] = '\\'; st->buf[st->bufLen++] = '"'; break;
			case '\\': st->buf[st->bufLen++] = '\\'; st->buf[st->bufLen++] = '\\'; break;
			case '\b': st->buf[st->bufLen++] = '\\'; st->buf[st->bufLen++] = 'b'; break;
			case '\f': st->buf[st->bufLen++] = '\\'; st->buf[st->bufLen++] = 'f'; break;
			case '\n': st->buf[st->bufLen++] = '\\'; st->buf[st->bufLen++] = 'n'; break;
			case '\r': st->buf[st->bufLen++] = '\\'; st->buf[st->bufLen++] = 'r'; break;
			case '\t': st->buf[st->bufLen++] = '\\'; st->buf[st->bufLen++] = 't'; break;
			default:
				if (c < 0x20) {
					snprintf(st->buf + st->bufLen, st->bufCap - st->bufLen, "\\u%04x", c);
					st->bufLen += 6;
				} else {
					st->buf[st->bufLen++] = c;
				}
		}
	}
	if (st->bufLen < st->bufCap - 1) st->buf[st->bufLen++] = '"';
	st->buf[st->bufLen] = '\0';
}

// Get a CFString attribute as a C string; returns non-null heap-allocated str (caller frees) or NULL.
static char* tree_copy_string(AXUIElementRef el, CFStringRef attr) {
	CFTypeRef ref = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &ref);
	if (err != kAXErrorSuccess || !ref) return NULL;
	if (CFGetTypeID(ref) != CFStringGetTypeID()) { CFRelease(ref); return NULL; }
	CFStringRef str = (CFStringRef)ref;
	CFIndex len = CFStringGetLength(str);
	if (len == 0) { CFRelease(ref); return NULL; }
	CFIndex maxBytes = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
	char* out = (char*)malloc(maxBytes);
	if (!out) { CFRelease(ref); return NULL; }
	if (!CFStringGetCString(str, out, maxBytes, kCFStringEncodingUTF8)) {
		free(out); CFRelease(ref); return NULL;
	}
	CFRelease(ref);
	return out;
}

// Get CFArray<CFString> as a JSON array; writes into buffer. Returns true on success.
static bool tree_append_string_array(TreeState* st, AXUIElementRef el, CFStringRef attr, const char* key) {
	CFTypeRef ref = NULL;
	AXError err = AXUIElementCopyAttributeValue(el, attr, &ref);
	if (err != kAXErrorSuccess || !ref) return false;
	if (CFGetTypeID(ref) != CFArrayGetTypeID()) { CFRelease(ref); return false; }
	CFArrayRef arr = (CFArrayRef)ref;
	CFIndex count = CFArrayGetCount(arr);
	if (count == 0) { CFRelease(ref); return false; }

	tree_append(st, ",\"");
	tree_append(st, key);
	tree_append(st, "\":[");
	int emitted = 0;
	for (CFIndex i = 0; i < count; i++) {
		CFTypeRef item = CFArrayGetValueAtIndex(arr, i);
		if (!item || CFGetTypeID(item) != CFStringGetTypeID()) continue;
		CFIndex slen = CFStringGetLength((CFStringRef)item);
		if (slen == 0) continue;
		CFIndex maxBytes = CFStringGetMaximumSizeForEncoding(slen, kCFStringEncodingUTF8) + 1;
		char* cs = (char*)malloc(maxBytes);
		if (!cs) continue;
		if (CFStringGetCString((CFStringRef)item, cs, maxBytes, kCFStringEncodingUTF8)) {
			if (emitted > 0) tree_append(st, ",");
			tree_append_json_string(st, cs);
			emitted++;
		}
		free(cs);
	}
	tree_append(st, "]");
	CFRelease(ref);
	return true;
}

// Get AXFrame (CGRect) attribute; writes frame as JSON object. Returns true on success.
static bool tree_append_frame(TreeState* st, AXUIElementRef el) {
	CFTypeRef posRef = NULL, sizeRef = NULL;
	CGPoint pos = CGPointZero;
	CGSize size = CGSizeZero;
	bool havePos = false, haveSize = false;

	if (AXUIElementCopyAttributeValue(el, kAXPositionAttribute, &posRef) == kAXErrorSuccess && posRef) {
		if (CFGetTypeID(posRef) == AXValueGetTypeID()) {
			havePos = AXValueGetValue((AXValueRef)posRef, kAXValueCGPointType, &pos);
		}
		CFRelease(posRef);
	}
	if (AXUIElementCopyAttributeValue(el, kAXSizeAttribute, &sizeRef) == kAXErrorSuccess && sizeRef) {
		if (CFGetTypeID(sizeRef) == AXValueGetTypeID()) {
			haveSize = AXValueGetValue((AXValueRef)sizeRef, kAXValueCGSizeType, &size);
		}
		CFRelease(sizeRef);
	}
	if (!havePos && !haveSize) return false;

	// AppKit reports off-screen / collapsed AX elements with frame
	// components set to ±inf (Numbers' off-screen rows, Keynote's
	// hidden navigator slides, etc.). Raw printf("%.1f", inf) emits
	// the literal "inf" / "-inf" / "nan" — which is NOT valid JSON
	// and breaks every consumer (json.Valid, jq, the Go JSON
	// decoder used by tools/ax-tree-dump's verifier path). Coerce
	// any non-finite value to 0 so the JSON envelope stays
	// well-formed without losing the structural hint that the node
	// existed.
	double fx = isfinite(pos.x)       ? pos.x       : 0.0;
	double fy = isfinite(pos.y)       ? pos.y       : 0.0;
	double fw = isfinite(size.width)  ? size.width  : 0.0;
	double fh = isfinite(size.height) ? size.height : 0.0;

	char tmp[128];
	snprintf(tmp, sizeof(tmp), ",\"frame\":{\"x\":%.1f,\"y\":%.1f,\"w\":%.1f,\"h\":%.1f}",
		fx, fy, fw, fh);
	tree_append(st, tmp);
	return true;
}

// Recursive walker. Emits one JSON object per node.
static void tree_walk(AXUIElementRef el, int depth, TreeState* st) {
	if (depth >= st->maxDepth) return;
	if (st->nodeCount >= st->maxNodes) return;
	if (st->bufLen >= st->bufCap - 256) return;

	st->nodeCount++;
	AXUIElementSetMessagingTimeout(el, 0.1f);

	// Get role (required)
	char* role = tree_copy_string(el, kAXRoleAttribute);
	if (!role) return;

	tree_append(st, "{\"role\":");
	tree_append_json_string(st, role);

	// Optional fields: subrole, title, description, value, identifier
	char* subrole = tree_copy_string(el, kAXSubroleAttribute);
	if (subrole) {
		tree_append(st, ",\"subrole\":");
		tree_append_json_string(st, subrole);
		free(subrole);
	}

	char* title = tree_copy_string(el, kAXTitleAttribute);
	if (title) {
		tree_append(st, ",\"title\":");
		tree_append_json_string(st, title);
		free(title);
	}

	char* desc = tree_copy_string(el, kAXDescriptionAttribute);
	if (desc) {
		tree_append(st, ",\"description\":");
		tree_append_json_string(st, desc);
		free(desc);
	}

	char* value = tree_copy_string(el, kAXValueAttribute);
	if (value) {
		tree_append(st, ",\"value\":");
		tree_append_json_string(st, value);
		free(value);
	}

	char* identifier = tree_copy_string(el, kAXIdentifierAttribute);
	if (identifier) {
		tree_append(st, ",\"identifier\":");
		tree_append_json_string(st, identifier);
		free(identifier);
	}

	// Selected state. Many app surfaces hide their "user intent focus" only
	// behind kAXSelectedAttribute (Finder file rows, NSTableView selections,
	// sidebar pickers, segmented controls). Without this attribute the AX
	// tree only tells us "what is on screen", not "what the user has
	// chosen" — and choice is usually the load-bearing signal. We emit the
	// key only when it's explicitly true to keep the JSON small (most
	// elements are not selected).
	CFTypeRef selectedRef = NULL;
	AXError serr = AXUIElementCopyAttributeValue(el, kAXSelectedAttribute, &selectedRef);
	if (serr == kAXErrorSuccess && selectedRef) {
		if (CFGetTypeID(selectedRef) == CFBooleanGetTypeID() &&
			CFBooleanGetValue((CFBooleanRef)selectedRef)) {
			tree_append(st, ",\"selected\":true");
		}
		CFRelease(selectedRef);
	}

	// DOM class list (browsers expose this via WebKit)
	tree_append_string_array(st, el, CFSTR("AXDOMClassList"), "domClassList");

	// Frame geometry
	tree_append_frame(st, el);

	// Walk children. Some apps' AX trees return a non-array value
	// (e.g. an NSString) for kAXChildrenAttribute — bridging that
	// straight to CFArrayGetCount triggers an Obj-C "unrecognized
	// selector 'count'" exception that crashes the whole process via
	// cgo. Type-check before treating it as an array.
	CFTypeRef childrenRef = NULL;
	AXError cerr = AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, &childrenRef);
	if (cerr == kAXErrorSuccess && childrenRef) {
		if (CFGetTypeID(childrenRef) == CFArrayGetTypeID()) {
			CFArrayRef children = (CFArrayRef)childrenRef;
			CFIndex count = CFArrayGetCount(children);
			if (count > 0) {
				int childrenStart = st->bufLen;
				tree_append(st, ",\"children\":[");
				int arrayStart = st->bufLen;
				int emitted = 0;
				for (CFIndex i = 0; i < count; i++) {
					if (st->nodeCount >= st->maxNodes) break;
					AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
					if (!child) continue;
					// Probe: tree_walk may bail out without writing
					// anything (missing role, hit budget mid-walk).
					// Tentatively write the comma, attempt the walk,
					// and roll back if no bytes were appended — this
					// avoids producing `[,]` which makes the JSON
					// invalid downstream and breaks json.Unmarshal.
					int beforeComma = st->bufLen;
					if (emitted > 0) tree_append(st, ",");
					int beforeWalk = st->bufLen;
					tree_walk(child, depth + 1, st);
					if (st->bufLen == beforeWalk) {
						// Walk produced nothing — undo the comma.
						st->bufLen = beforeComma;
						st->buf[st->bufLen] = '\0';
						continue;
					}
					emitted++;
				}
				if (emitted == 0) {
					// All children were budget-skipped or roleless —
					// drop the entire `,"children":[` prefix so the
					// parent stays a leaf with a clean `}`.
					st->bufLen = childrenStart;
					st->buf[st->bufLen] = '\0';
				} else {
					(void)arrayStart;
					tree_append(st, "]");
				}
			}
		}
		CFRelease(childrenRef);
	}

	tree_append(st, "}");
	free(role);
}

// app_needs_prime reports whether the given bundle ID belongs to an
// app whose AX tree only becomes useful after we set
// AXEnhancedUserInterface=true (Chromium / Electron apps with a
// dormant WebView).
//
// Why opt-in instead of "always prime": empirically, native AppKit
// apps (iWork, Mail, Finder, Notes, Reminders, App Store, Calendar,
// …) react badly when an external AX client toggles
// AXEnhancedUserInterface on them — Numbers in particular returns an
// empty AX tree on every subsequent walk while the (un-primed) flat
// AXText path keeps returning real cell data. Confirmed 2026-05-14
// against Numbers 14: 5 consecutive snapshots of the same focused
// window came back with nil structured tree and 879-byte flat text.
//
// Default is no-prime — only the substrings below opt in. Add a new
// entry when onboarding an Electron-based parser.
static bool app_needs_prime(const char* bundleID) {
	if (!bundleID || !*bundleID) return false;
	static const char* fragments[] = {
		"slack",     // com.tinyspeck.slackmacgap
		"discord",   // com.hnc.Discord, com.discordapp.Discord
		"chrome",    // com.google.Chrome*, com.brave.Browser uses Chromium too
		"edge",      // com.microsoft.edgemac
		"brave",     // com.brave.Browser
		"vivaldi",   // com.vivaldi.Vivaldi
		"code",      // com.microsoft.VSCode, com.visualstudio.code.oss
		"cursor",    // com.todesktop.230313mzl4w4u92, com.cursor.Cursor
		"linear",    // com.linear.linear
		"notion",    // notion.id
		"obsidian",  // md.obsidian
		"figma",     // com.figma.Desktop
		"electron",  // generic Electron apps
		// The three below were discovered missing in 2026-05 when comparing
		// our prime list against the apps PR #1817 marks as canvas-only.
		// All three are LIKELY no-ops on top of the existing partial-AX
		// fallback (WhatsApp is Catalyst not Electron, WeChat opts out of
		// AX entirely, Termius renders the terminal grid to canvas via
		// xterm.js so the flag wouldn't unblock the body either), but the
		// prime call is cheap (one IPC per attribute on cold path) and
		// having them in the list means future readers don't waste time
		// rediscovering this. Document any actual coverage change in
		// unsupported.go's Notes.
		"whatsapp",  // net.whatsapp.WhatsApp, desktop.WhatsApp — Catalyst, unlikely
		"tencent",   // com.tencent.xinWeChat (WeChat) — Tencent opts out of AX
		"termius",   // com.termius-dmg.mac — Electron but body is xterm.js canvas
	};
	for (size_t i = 0; i < sizeof(fragments) / sizeof(fragments[0]); i++) {
		if (strcasestr(bundleID, fragments[i]) != NULL) return true;
	}
	return false;
}

// Walks the focused (or first available) window of the given AXUIElement
// and writes the JSON serialization into a heap-allocated buffer. Caller
// frees. Returns NULL if no window can be reached.
//
// bundleID is consulted by app_needs_prime to decide whether to
// toggle AXEnhancedUserInterface. Pass "" to skip prime entirely
// (caller doesn't know the bundle, default to native behavior).
static char* tree_walk_app_to_buf(AXUIElementRef app, const char* bundleID) {
	if (!app) return NULL;
	AXUIElementSetMessagingTimeout(app, 0.1f);

	bool prime = app_needs_prime(bundleID);

	// Prime Chromium/Electron-based apps (Chrome, Slack, Discord, Linear
	// desktop, VS Code, …). They keep their WebView AX tree dormant until
	// an external client sets AXEnhancedUserInterface=true on the app
	// element — VoiceOver does this implicitly. Without this, the tree we
	// walk stops at the native NSWindow chrome (RootView/NonClientView/…)
	// and the entire web content (message lists, channel sidebar bodies)
	// is invisible. Verified empirically: Chrome jumps from 8 → 500+ nodes
	// after this call. Slack's main app currently does NOT honor either
	// attribute (its Electron build appears to suppress AX); for those
	// apps we still walk the chrome and downstream parsers receive nil,
	// which is no worse than before. Bootstrap is asynchronous on
	// Chromium's UI thread, so give it a brief window to populate the
	// tree before we copy.
	//
	// Skipped for native AppKit apps because prime is hostile to them
	// (see app_needs_prime).
	CFBooleanRef yes = kCFBooleanTrue;
	if (prime) {
		AXUIElementSetAttributeValue(app, CFSTR("AXEnhancedUserInterface"), yes);
		AXUIElementSetAttributeValue(app, CFSTR("AXManualAccessibility"), yes);

		// Brief settle — Chromium populates the WebView AX tree on its
		// UI thread after AXEnhancedUserInterface flips on. 30 ms is
		// empirically enough on M-series Macs to let the bootstrap
		// land for already-active apps; first-ever priming after app
		// launch may need a second call.
		usleep(30 * 1000);
	}

	// Prefer the focused window; fall back to the first entry in
	// kAXWindowsAttribute when the app is not frontmost (focused window
	// returns kAXErrorAttributeUnsupported / nil for background apps on
	// recent macOS releases).
	AXUIElementRef win = NULL;
	AXError err = AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, (CFTypeRef*)&win);
	if (err != kAXErrorSuccess || !win) {
		CFTypeRef windowsRef = NULL;
		if (AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &windowsRef) == kAXErrorSuccess && windowsRef) {
			if (CFGetTypeID(windowsRef) == CFArrayGetTypeID()) {
				CFArrayRef windows = (CFArrayRef)windowsRef;
				if (CFArrayGetCount(windows) > 0) {
					win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, 0);
					if (win) CFRetain(win);
				}
			}
			CFRelease(windowsRef);
		}
	}
	if (!win) return NULL;

	// Some Chromium builds (Slack, VS Code) only expand the WebView AX
	// after the priming attribute is set on the WINDOW element, not on
	// the app. Set both layers — overhead is one IPC call per attribute,
	// and only Electron apps will care.
	if (prime) {
		AXUIElementSetAttributeValue(win, CFSTR("AXEnhancedUserInterface"), yes);
		AXUIElementSetAttributeValue(win, CFSTR("AXManualAccessibility"), yes);
		usleep(30 * 1000);
	}

	// 1MB ceiling. Each node emits ~50-150 bytes of JSON (role, subrole,
	// title, description, value, frame, dom class list); at 8000 nodes the
	// upper bound lands well under 1MB but Electron apps with rich descs
	// (Slack message bodies, Linear issue cards) can climb fast — the old
	// 256KB cap was clipping mid-tree and causing parsers to see partial
	// JSON / fail silently.
	const int capInit = 1024 * 1024;
	char* buf = (char*)malloc(capInit);
	if (!buf) { CFRelease(win); return NULL; }
	buf[0] = '\0';

	TreeState st = { .buf = buf, .bufLen = 0, .bufCap = capInit,
		.nodeCount = 0, .maxNodes = TREE_MAX_NODES, .maxDepth = TREE_MAX_DEPTH };

	tree_walk(win, 0, &st);
	CFRelease(win);

	if (st.bufLen == 0) { free(buf); return NULL; }
	return buf;
}

// Collect the focused window as a JSON-serialized AX tree.
// Returns a heap-allocated C string (caller must free), or NULL on failure.
static char* tree_collect_focused_window_json(void) {
	@autoreleasepool {
		NSRunningApplication *front = activitymon_frontmost_app();
		if (!front) return NULL;
		// Never walk our own window. The walk runs off the main thread, and
		// WebKit kills a process that queries its own web view's accessibility
		// from a background thread (see Yansu-2026-10-12-101016.ips).
		if (front.processIdentifier == getpid()) return NULL;

		AXUIElementRef app = AXUIElementCreateApplication(front.processIdentifier);
		const char* bid = front.bundleIdentifier ? [front.bundleIdentifier UTF8String] : "";
		char* out = tree_walk_app_to_buf(app, bid);
		if (app) CFRelease(app);
		return out;
	}
}

// Collect the focused (or first) window of the running app with the given
// bundle ID. Used by the ax-tree-dump CLI to bypass `frontmostApplication`,
// which gets hijacked by `com.apple.universalcontrol` on multi-monitor
// setups and returns an empty tree.
static char* tree_collect_window_by_bundle_json(const char* bundleID) {
	if (!bundleID || !*bundleID) return NULL;
	@autoreleasepool {
		NSString *target = [NSString stringWithUTF8String:bundleID];
		NSArray<NSRunningApplication*> *apps = activitymon_running_apps();
		NSRunningApplication *match = nil;
		for (NSRunningApplication *a in apps) {
			if (a.bundleIdentifier && [a.bundleIdentifier isEqualToString:target]) {
				match = a;
				break;
			}
		}
		if (!match) return NULL;
		if (match.processIdentifier == getpid()) return NULL;

		AXUIElementRef app = AXUIElementCreateApplication(match.processIdentifier);
		char* out = tree_walk_app_to_buf(app, bundleID);
		if (app) CFRelease(app);
		return out;
	}
}

// Collect the AX tree of the Nth top-level window of the running app with
// the given bundle ID. Lets the AX-parser-probe UI walk a specific window
// after listing them, which matters for multi-window Electron apps where
// the focused-window heuristic in tree_walk_app_to_buf may pick the wrong
// surface (Slack workspace switcher vs main channel, VS Code editor vs
// command palette, …).
//
// Performs the same Chromium priming + 30ms settle as tree_walk_app_to_buf
// so the result is comparable to the focused-window path.
static char* tree_collect_window_by_bundle_index_json(const char* bundleID, int windowIndex) {
	if (!bundleID || !*bundleID || windowIndex < 0) return NULL;
	@autoreleasepool {
		NSString *target = [NSString stringWithUTF8String:bundleID];
		NSArray<NSRunningApplication*> *apps = activitymon_running_apps();
		NSRunningApplication *match = nil;
		for (NSRunningApplication *a in apps) {
			if (a.bundleIdentifier && [a.bundleIdentifier isEqualToString:target]) {
				match = a;
				break;
			}
		}
		if (!match) return NULL;
		if (match.processIdentifier == getpid()) return NULL;

		AXUIElementRef app = AXUIElementCreateApplication(match.processIdentifier);
		if (!app) return NULL;
		AXUIElementSetMessagingTimeout(app, 0.1f);

		bool prime = app_needs_prime(bundleID);
		CFBooleanRef yes = kCFBooleanTrue;
		if (prime) {
			AXUIElementSetAttributeValue(app, CFSTR("AXEnhancedUserInterface"), yes);
			AXUIElementSetAttributeValue(app, CFSTR("AXManualAccessibility"), yes);
			usleep(30 * 1000);
		}

		CFTypeRef windowsRef = NULL;
		AXError werr = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &windowsRef);
		if (werr != kAXErrorSuccess || !windowsRef) {
			CFRelease(app);
			return NULL;
		}
		if (CFGetTypeID(windowsRef) != CFArrayGetTypeID()) {
			CFRelease(windowsRef);
			CFRelease(app);
			return NULL;
		}
		CFArrayRef windows = (CFArrayRef)windowsRef;
		CFIndex count = CFArrayGetCount(windows);
		if ((CFIndex)windowIndex >= count) {
			CFRelease(windowsRef);
			CFRelease(app);
			return NULL;
		}

		AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, (CFIndex)windowIndex);
		if (!win) {
			CFRelease(windowsRef);
			CFRelease(app);
			return NULL;
		}
		CFRetain(win);
		CFRelease(windowsRef);

		if (prime) {
			AXUIElementSetAttributeValue(win, CFSTR("AXEnhancedUserInterface"), yes);
			AXUIElementSetAttributeValue(win, CFSTR("AXManualAccessibility"), yes);
			usleep(30 * 1000);
		}

		const int capInit = 1024 * 1024;
		char* buf = (char*)malloc(capInit);
		if (!buf) { CFRelease(win); CFRelease(app); return NULL; }
		buf[0] = '\0';
		TreeState st = { .buf = buf, .bufLen = 0, .bufCap = capInit,
			.nodeCount = 0, .maxNodes = TREE_MAX_NODES, .maxDepth = TREE_MAX_DEPTH };
		tree_walk(win, 0, &st);
		CFRelease(win);
		CFRelease(app);

		if (st.bufLen == 0) { free(buf); return NULL; }
		return buf;
	}
}

// Returns a JSON array describing all top-level windows of the running app
// with the given bundle ID — one object per window with role/title/frame
// and the byte count of its serialized AX tree. Lets the diagnostic CLI
// pick the right window when an app has several (Slack: workspace switcher,
// main, popouts, …) and confirm whether AXEnhancedUserInterface priming
// has expanded any of them.
static char* tree_list_windows_by_bundle_json(const char* bundleID) {
	if (!bundleID || !*bundleID) return NULL;
	@autoreleasepool {
		NSString *target = [NSString stringWithUTF8String:bundleID];
		NSArray<NSRunningApplication*> *apps = activitymon_running_apps();
		NSRunningApplication *match = nil;
		for (NSRunningApplication *a in apps) {
			if (a.bundleIdentifier && [a.bundleIdentifier isEqualToString:target]) {
				match = a;
				break;
			}
		}
		if (!match) return NULL;

		AXUIElementRef app = AXUIElementCreateApplication(match.processIdentifier);
		if (!app) return NULL;
		AXUIElementSetMessagingTimeout(app, 0.1f);

		// Same priming + settle as tree_walk_app_to_buf so the listing
		// reflects the post-priming tree shape.
		AXUIElementSetAttributeValue(app, CFSTR("AXEnhancedUserInterface"), kCFBooleanTrue);
		AXUIElementSetAttributeValue(app, CFSTR("AXManualAccessibility"), kCFBooleanTrue);
		usleep(30 * 1000);

		CFTypeRef windowsRef = NULL;
		AXError werr = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &windowsRef);
		if (werr != kAXErrorSuccess || !windowsRef) {
			CFRelease(app);
			return NULL;
		}
		if (CFGetTypeID(windowsRef) != CFArrayGetTypeID()) {
			CFRelease(windowsRef);
			CFRelease(app);
			return NULL;
		}

		const int capInit = 64 * 1024;
		char* buf = (char*)malloc(capInit);
		if (!buf) { CFRelease(windowsRef); CFRelease(app); return NULL; }
		buf[0] = '\0';
		TreeState st = { .buf = buf, .bufLen = 0, .bufCap = capInit,
			.nodeCount = 0, .maxNodes = TREE_MAX_NODES, .maxDepth = TREE_MAX_DEPTH };

		tree_append(&st, "[");
		CFArrayRef windows = (CFArrayRef)windowsRef;
		CFIndex count = CFArrayGetCount(windows);
		for (CFIndex i = 0; i < count; i++) {
			AXUIElementRef w = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
			if (!w) continue;
			if (i > 0) tree_append(&st, ",");
			tree_append(&st, "{\"index\":");
			char idx[16]; snprintf(idx, sizeof(idx), "%ld", (long)i); tree_append(&st, idx);

			char* role = tree_copy_string(w, kAXRoleAttribute);
			if (role) {
				tree_append(&st, ",\"role\":");
				tree_append_json_string(&st, role);
				free(role);
			}
			char* title = tree_copy_string(w, kAXTitleAttribute);
			if (title) {
				tree_append(&st, ",\"title\":");
				tree_append_json_string(&st, title);
				free(title);
			}
			tree_append_frame(&st, w);

			// Walk this window into a separate buffer just to measure size
			// — gives us a quick "tree fatness" indicator per window.
			char* perWin = (char*)malloc(capInit);
			if (perWin) {
				perWin[0] = '\0';
				TreeState sub = { .buf = perWin, .bufLen = 0, .bufCap = capInit,
					.nodeCount = 0, .maxNodes = TREE_MAX_NODES, .maxDepth = TREE_MAX_DEPTH };
				tree_walk(w, 0, &sub);
				char meta[64];
				snprintf(meta, sizeof(meta), ",\"treeBytes\":%d,\"nodeCount\":%d", sub.bufLen, sub.nodeCount);
				tree_append(&st, meta);
				free(perWin);
			}
			tree_append(&st, "}");
		}
		tree_append(&st, "]");

		CFRelease(windowsRef);
		CFRelease(app);
		return buf;
	}
}
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

// AXNode / AXFrame are declared in axtree_tree_types.go so they are shared
// across darwin / windows / stub builds. The darwin C walker emits matching
// JSON; the Go side unmarshals into the shared type.

// CollectFocusedWindowTree returns a structured representation of the
// focused window's accessibility tree. Returns nil if the tree is unavailable
// (no focused window, permission denied, tree too large).
//
// Collection is bounded by TREE_MAX_NODES and TREE_MAX_DEPTH (see top of
// file) to keep CPU and memory usage reasonable even for deeply-nested
// web pages.
func CollectFocusedWindowTree() *AXNode {
	ptr := C.tree_collect_focused_window_json()
	if ptr == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(ptr))
	jsonBytes := []byte(C.GoString(ptr))
	if len(jsonBytes) == 0 {
		return nil
	}
	var node AXNode
	if err := json.Unmarshal(jsonBytes, &node); err != nil {
		return nil
	}
	if node.Role == "" {
		return nil
	}
	return &node
}

// CollectFocusedWindowTreeJSON returns the raw JSON serialization of the
// focused window's AX tree without round-tripping through Go structs. Used
// by the AX-tree-dump debug tool so the output preserves every field the
// walker emitted (frame, domClassList, identifier, etc.) — round-tripping
// through AXNode would silently drop unknown fields and obscure the very
// information the parser-selector debugger needs.
func CollectFocusedWindowTreeJSON() string {
	ptr := C.tree_collect_focused_window_json()
	if ptr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(ptr))
	return C.GoString(ptr)
}

// CollectWindowTreeByBundleJSON returns the AX tree JSON for the focused
// (or first available) window of the running app with the given bundle ID.
// Bypasses NSWorkspace.frontmostApplication, which on multi-monitor setups
// often reports `com.apple.universalcontrol` mid-cursor-cross and produces
// an empty tree.
func CollectWindowTreeByBundleJSON(bundleID string) string {
	if bundleID == "" {
		return ""
	}
	cBundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundle))
	ptr := C.tree_collect_window_by_bundle_json(cBundle)
	if ptr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(ptr))
	return C.GoString(ptr)
}

// CollectWindowTreeByBundle is the structured-Go counterpart of
// CollectWindowTreeByBundleJSON.
func CollectWindowTreeByBundle(bundleID string) *AXNode {
	raw := CollectWindowTreeByBundleJSON(bundleID)
	if raw == "" {
		return nil
	}
	var node AXNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return nil
	}
	if node.Role == "" {
		return nil
	}
	return &node
}

// CollectWindowTreeByBundleIndexJSON walks the Nth top-level window of the
// running app with the given bundle ID. Pair with ListWindowsByBundleJSON
// to let an operator pick a specific window — useful for multi-window
// Electron apps where the default focused-window heuristic picks the wrong
// surface (Slack workspace switcher instead of the channel pane, …).
//
// Returns an empty string when the app is not running, the index is out of
// range, or the walk produces no nodes.
func CollectWindowTreeByBundleIndexJSON(bundleID string, windowIndex int) string {
	if bundleID == "" || windowIndex < 0 {
		return ""
	}
	cBundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundle))
	ptr := C.tree_collect_window_by_bundle_index_json(cBundle, C.int(windowIndex))
	if ptr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(ptr))
	return C.GoString(ptr)
}

// CollectWindowTreeByBundleIndex is the structured-Go counterpart of
// CollectWindowTreeByBundleIndexJSON.
func CollectWindowTreeByBundleIndex(bundleID string, windowIndex int) *AXNode {
	raw := CollectWindowTreeByBundleIndexJSON(bundleID, windowIndex)
	if raw == "" {
		return nil
	}
	var node AXNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return nil
	}
	if node.Role == "" {
		return nil
	}
	return &node
}

// ListWindowsByBundleJSON returns a JSON array describing every top-level
// window of the running app with the given bundle ID, including each
// window's role/title/frame and the byte size of its serialized AX tree.
// Used by the diagnostic CLI to figure out which window in a multi-window
// app (Slack, VS Code, …) holds the content the parser cares about.
func ListWindowsByBundleJSON(bundleID string) string {
	if bundleID == "" {
		return ""
	}
	cBundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundle))
	ptr := C.tree_list_windows_by_bundle_json(cBundle)
	if ptr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(ptr))
	return C.GoString(ptr)
}
