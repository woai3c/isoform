#import <Cocoa/Cocoa.h>
#include <stdbool.h>
#include <math.h>
#include <limits.h>
#include "main_window_darwin.h"

typedef struct {
    int x, y, width, height;
    bool maximised, valid;
} IsoformWindowState;

// Main-thread only. A missing/off-screen window must not overwrite the last
// usable geometry. Do not retain a borrowed NSWindow across a dispatch hop.
static IsoformWindowState isoformWindowStateForWindow(NSWindow *window) {
    IsoformWindowState state = {0};
    if (!window || window.miniaturized) return state;
    NSScreen *screen = window.screen;
    if (!screen) return state;
    NSRect frame = window.frame;
    NSRect visible = screen.visibleFrame;
    double x = frame.origin.x - visible.origin.x;
    double y = visible.size.height - (frame.origin.y - visible.origin.y) - frame.size.height;
    if (!isfinite(x) || !isfinite(y) || x < INT_MIN || x > INT_MAX || y < INT_MIN || y > INT_MAX ||
        !isfinite(frame.size.width) || !isfinite(frame.size.height) ||
        frame.size.width <= 0 || frame.size.height <= 0 ||
        frame.size.width > INT_MAX || frame.size.height > INT_MAX) return state;
    state.x = (int)x;
    state.y = (int)y;
    state.width = (int)frame.size.width;
    state.height = (int)frame.size.height;
    state.maximised = window.zoomed;
    state.valid = true;
    return state;
}

static IsoformWindowState isoformReadWindowState(void) {
    __block IsoformWindowState state = {0};
    void (^readState)(void) = ^{
        @autoreleasepool {
            // Toasts and Quick Chat are panels in the same process. Only
            // capture the Wails window, including while it is ordered out.
            state = isoformWindowStateForWindow(isoformMainWindow());
        }
    };
    if ([NSThread isMainThread]) readState();
    else dispatch_sync(dispatch_get_main_queue(), readState);
    return state;
}
