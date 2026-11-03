#ifndef ISOFORM_MAIN_WINDOW_DARWIN_H
#define ISOFORM_MAIN_WINDOW_DARWIN_H

#import <Cocoa/Cocoa.h>

// Main-thread only. AppKit's key/main window may be nil while the app is
// inactive; NSApp.windows also contains hidden WebKit and system panels.
// Restoring the desktop must never order one of those auxiliary windows on.
static NSWindow *isoformMainWindow(void) {
    Class wailsWindow = NSClassFromString(@"WailsWindow");
    if (!wailsWindow) return nil;
    for (NSWindow *window in NSApp.windows) {
        if ([window isKindOfClass:wailsWindow]) return window;
    }
    return nil;
}

#endif
