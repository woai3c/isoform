//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// pickSpotlightScreen returns the NSScreen the spotlight should land on.
// When followMouse is true we use [NSEvent mouseLocation] to find the
// screen the user is currently looking at — this matches Raycast / Alfred
// UX and replaces Wails' Screen.IsCurrent, which on macOS is "the screen
// the window is currently on" and therefore sticks to wherever the pill
// was last shown, even after the user switches monitors. When false we
// use the window's own screen so resize transitions don't yank the
// panel to a different monitor mid-conversation.
static NSScreen *pickSpotlightScreen(NSWindow *win, BOOL followMouse) {
    if (followMouse) {
        NSPoint mouse = [NSEvent mouseLocation];
        for (NSScreen *s in [NSScreen screens]) {
            if (NSPointInRect(mouse, s.frame)) return s;
        }
    }
    if (win != nil && win.screen != nil) return win.screen;
    return [NSScreen mainScreen];
}

// moveSpotlightFrameMac centres the spotlight window horizontally and
// anchors its bottom edge at (1 - bottomFraction) * screenHeight above
// the chosen screen's origin (NSScreen coords, origin = bottom-left).
// bottomFraction matches main.go's Wails-coords convention, where 0.88
// means the pill's bottom sits 88% down from the top — i.e. 12% above
// the screen's bottom edge. Returns false if no window matches the
// title so the Go caller can fall back to the Wails-based path.
static bool moveSpotlightFrameMac(const char *title, int w, int h,
                                  double bottomFraction, bool followMouse) {
    if (title == NULL) return false;
    NSString *targetTitle = [NSString stringWithUTF8String:title];

    __block bool moved = false;
    void (^apply)(void) = ^{
        NSWindow *target = nil;
        for (NSWindow *win in [NSApp windows]) {
            if ([win.title isEqualToString:targetTitle]) {
                target = win;
                break;
            }
        }
        if (target == nil) return;

        NSScreen *screen = pickSpotlightScreen(target, followMouse ? YES : NO);
        if (screen == nil) return;

        CGFloat fw = (CGFloat)w;
        CGFloat fh = (CGFloat)h;
        NSRect sf = screen.frame;
        CGFloat x = sf.origin.x + (sf.size.width - fw) / 2.0;
        CGFloat y = sf.origin.y + sf.size.height * (1.0 - bottomFraction);
        NSRect newFrame = NSMakeRect(x, y, fw, fh);
        [target setFrame:newFrame display:YES];
        moved = true;
    };
    if ([NSThread isMainThread]) {
        apply();
    } else {
        dispatch_sync(dispatch_get_main_queue(), apply);
    }
    return moved;
}
*/
import "C"

import "unsafe"

// setSpotlightFrameDarwin moves and resizes the spotlight pill in one
// atomic native call. When followMouse is true the pill jumps to
// whichever NSScreen currently contains the mouse pointer; when false
// it stays on the window's existing screen (used by pill↔conversation
// resizes so a wandering mouse doesn't relocate the panel mid-typing).
//
// Returns false when no NSWindow matches the title, so the Go caller
// can fall through to the Wails-based positioning path.
func setSpotlightFrameDarwin(title string, w, h int, bottomFraction float64, followMouse bool) bool {
	cstr := C.CString(title)
	defer C.free(unsafe.Pointer(cstr))
	return bool(C.moveSpotlightFrameMac(
		cstr, C.int(w), C.int(h), C.double(bottomFraction), C.bool(followMouse),
	))
}
