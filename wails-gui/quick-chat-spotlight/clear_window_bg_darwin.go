//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// _spotlightTitle is injected from Go via setSpotlightTitleNative so
// the ObjC layer doesn't have to duplicate the spotlightWindowTitle
// constant from main.go. ARC owns the NSString, so reassigning here
// releases the previous value automatically.
static NSString *_spotlightTitle = nil;

static void setSpotlightTitleNative(const char *title) {
    if (title == NULL) return;
    _spotlightTitle = [NSString stringWithUTF8String:title];
}

static void clearViewBackgrounds(NSView *view) {
    if (!view) return;
    if ([view isKindOfClass:[NSVisualEffectView class]]) {
        [view removeFromSuperview];
        return;
    }
    view.wantsLayer = YES;
    view.layer.backgroundColor = [NSColor clearColor].CGColor;

    if ([view isKindOfClass:[WKWebView class]]) {
        @try {
            [(WKWebView *)view setValue:@NO forKey:@"drawsBackground"];
        } @catch (NSException *exception) {
        }
        // Stop here: WKWebView's private subview tree (WKContentView,
        // WKScrollView, ...) is WebKit's business and the rendered web
        // content owns its own background, so recursing further is
        // both unnecessary and a stability risk across WebKit revisions.
        return;
    }

    NSArray<NSView *> *subviews = [view.subviews copy];
    for (NSView *subview in subviews) {
        clearViewBackgrounds(subview);
    }
}

static void configureSpotlightAppPresentationNative(void) {
    void (^apply)(void) = ^{
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
    };
    if ([NSThread isMainThread]) {
        apply();
    } else {
        dispatch_sync(dispatch_get_main_queue(), apply);
    }
}

static void clearSpotlightWindowBackgroundsNow(void) {
    // Filter by title so we never strip the background/shadow off
    // auxiliary windows the helper process owns — most notably the
    // Wails DevTools window in dev mode, which becomes invisible and
    // unusable if it gets the same clearColor + no-shadow treatment.
    // The title is injected from Go (spotlightWindowTitle in main.go)
    // via setSpotlightTitleNative; bail out if Go forgot to call it.
    if (_spotlightTitle == nil) return;
    for (NSWindow *win in [NSApp windows]) {
        if (![win.title isEqualToString:_spotlightTitle]) continue;
        [win setOpaque:NO];
        [win setBackgroundColor:[NSColor clearColor]];
        [win setHasShadow:NO];
        clearViewBackgrounds(win.contentView);
    }
}

static void clearSpotlightWindowBackgroundsAfter(double seconds) {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(seconds * NSEC_PER_SEC)),
                   dispatch_get_main_queue(), ^{
        clearSpotlightWindowBackgroundsNow();
    });
}

// clearNonOpaqueWindowBackgrounds finds Wails-created windows, replaces
// their background color with NSColor.clearColor, and disables the
// native rectangular window shadow.
//
// Wails v2 (as of v2.11.0) responds to Mac.WindowIsTranslucent by
// calling [window setOpaque:NO] but never assigns a clearColor
// background, so the window's *default* backgroundColor — which is
// system-deep-grey in dark mode — bleeds through the CSS border-radius
// corners as dark "L"-shaped artifacts around the pill. This patches
// that omission without forking Wails. The default NSWindow shadow is
// also rectangular, so it shows up as dark square corners around a
// CSS-rounded transparent webview; disable it here so the pill shape
// itself is the only visible chrome.
//
// Scheduled on the main queue so it runs after Wails finishes its
// own init on the current runloop tick — calling it synchronously
// during OnStartup races Wails' window setup. The 50ms/200ms retries
// cover Wails/macOS work that happens just after startup or WindowShow,
// including late WebView attachment and window background resets.
static void clearNonOpaqueWindowBackgrounds(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        clearSpotlightWindowBackgroundsNow();
    });
    clearSpotlightWindowBackgroundsAfter(0.05);
    clearSpotlightWindowBackgroundsAfter(0.20);
}
*/
import "C"

import "unsafe"

func clearWindowBackgroundForTransparency() {
	C.clearNonOpaqueWindowBackgrounds()
}

func configureSpotlightAppPresentation() {
	C.configureSpotlightAppPresentationNative()
}

// setSpotlightTitle hands the Go-side spotlightWindowTitle constant to
// the ObjC layer so window filtering can match without a duplicated
// literal. Must be called before clearWindowBackgroundForTransparency
// can take effect — otherwise the native filter no-ops by design.
func setSpotlightTitle(title string) {
	cstr := C.CString(title)
	defer C.free(unsafe.Pointer(cstr))
	C.setSpotlightTitleNative(cstr)
}
