#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#import <WebKit/WebKit.h>
#include <dispatch/dispatch.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "menubar_popover_darwin.h"

// Drives the real popover source against a real status item. Modes:
//   instant     page preloaded; a click must show the popover without any
//               call into Go first, within 100 ms (printed as SHOWN ms=N)
//   early       the click lands before the page is handed over; the
//               popover still opens once it is
//   invalidate  a privacy invalidation while open empties the page and
//               keeps the popover open
//   dark        the preloaded model's onedark theme gives dark chrome on
//               the first open
//   invalidate_opening
//               an invalidation lands while the click is still opening (the
//               page not ready yet); our popover still opens, in its loading
//               state, and the native menu never appears
static NSString *pageHTML;
static NSString *modelJSON;
static const char *mode;
static NSStatusItem *statusItem;
static CFTimeInterval clickedAt;
static BOOL goCalledBeforeShow;
static BOOL shown;
static BOOL sawDark;
static BOOL sawWithdraw;
static BOOL sawNativeMenu;

static NSWindow *panelWindow(void) {
    for (NSWindow *window in NSApp.windows) {
        if ([NSStringFromClass(window.class) isEqual:@"IsoformMenuBarPanel"]) return window;
    }
    return nil;
}

static void finishOnBackgroundQueue(NSString *message, int status) {
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
        fprintf(stdout, "%s\n", message.UTF8String);
        fflush(stdout);
        exit(status);
    });
}

void goMenuBarPopoverVisibility(_Bool open) {
    if (!open || shown) return;
    shown = YES;
    double ms = (CACurrentMediaTime() - clickedAt) * 1000;
    if (strcmp(mode, "instant") == 0) {
        NSWindow *panel = panelWindow();
        NSRect icon = [statusItem.button.window convertRectToScreen:[statusItem.button convertRect:statusItem.button.bounds toView:nil]];
        finishOnBackgroundQueue([NSString stringWithFormat:@"SHOWN ms=%.0f go_before_show=%d gap=%.0f width=%.0f opaque=%d", ms, goCalledBeforeShow,
            NSMinY(icon) - NSMaxY(panel.frame), panel.frame.size.width, panel.isOpaque], 0);
    } else if (strcmp(mode, "early") == 0) {
        finishOnBackgroundQueue(@"SHOWN", 0);
    } else if (strcmp(mode, "invalidate_opening") == 0) {
        finishOnBackgroundQueue([NSString stringWithFormat:@"SHOWN withdrawn=%d native_menu=%d", sawWithdraw, sawNativeMenu], 0);
    } else if (strcmp(mode, "dark") == 0) {
        finishOnBackgroundQueue(sawDark ? @"DARK" : @"LIGHT", 0);
    } else if (strcmp(mode, "invalidate") == 0) {
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{ isoformMenuBarInvalidate(); });
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 500 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
            BOOL open = panelWindow().isVisible;
            finishOnBackgroundQueue([NSString stringWithFormat:@"%@ withdrawn=%d", open ? @"STILL_SHOWN" : @"CLOSED", sawWithdraw], 0);
        });
    }
}

void goMenuBarCommand(char *command) { if (!shown) goCalledBeforeShow = YES; }

void goMenuBarPopoverStep(char *step) {
    fprintf(stderr, "[menubar] popover: %s\n", step ?: "(null)");
    fflush(stderr);
    if (!step) return;
    if (strstr(step, "appearance=NSAppearanceNameDarkAqua")) sawDark = YES;
    if (strstr(step, "action=withdraw")) sawWithdraw = YES;
    if (strstr(step, "native menu")) sawNativeMenu = YES;
    if (strstr(step, "page ready") && strcmp(mode, "early") != 0 && strcmp(mode, "invalidate_opening") != 0) {
        dispatch_async(dispatch_get_main_queue(), ^{
            static BOOL clicked;
            if (clicked) return;
            clicked = YES;
            clickedAt = CACurrentMediaTime();
            [statusItem.button performClick:nil];
        });
    }
}

@interface MenuBarPopoverHarness : NSObject <NSApplicationDelegate>
@end

@implementation MenuBarPopoverHarness
- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    NSApplication *app = NSApplication.sharedApplication;
    [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
    statusItem = [[NSStatusBar.systemStatusBar statusItemWithLength:NSVariableStatusItemLength] retain];
    if (!statusItem || !statusItem.button) {
        fprintf(stderr, "NO_WINDOW_SERVER\n");
        exit(77);
    }
    NSMenu *menu = [[[NSMenu alloc] initWithTitle:@"Harness"] autorelease];
    [menu addItemWithTitle:@"Harness item" action:nil keyEquivalent:@""];
    statusItem.menu = menu;
    statusItem.button.title = @"MB";
    if (!isoformMenuBarAttach(statusItem)) {
        fprintf(stderr, "ATTACH_FAILED\n");
        exit(1);
    }
    if (strcmp(mode, "early") == 0 || strcmp(mode, "invalidate_opening") == 0) {
        clickedAt = CACurrentMediaTime();
        [statusItem.button performClick:nil];
        if (strcmp(mode, "invalidate_opening") == 0) isoformMenuBarInvalidate();
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 200 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
            isoformMenuBarPreload(pageHTML.UTF8String, modelJSON.UTF8String);
        });
    } else {
        isoformMenuBarPreload(pageHTML.UTF8String, modelJSON.UTF8String);
    }
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC), dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
        finishOnBackgroundQueue(@"NOT_SHOWN_BY_DEADLINE", 1);
    });
}
@end

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        if (argc != 4) return 2;
        pageHTML = [[NSString alloc] initWithContentsOfFile:[NSString stringWithUTF8String:argv[1]] encoding:NSUTF8StringEncoding error:NULL];
        modelJSON = [[NSString alloc] initWithContentsOfFile:[NSString stringWithUTF8String:argv[2]] encoding:NSUTF8StringEncoding error:NULL];
        mode = argv[3];
        if (!pageHTML || !modelJSON) return 2;
        MenuBarPopoverHarness *delegate = [MenuBarPopoverHarness new];
        NSApplication.sharedApplication.delegate = delegate;
        [NSApplication.sharedApplication run];
        [delegate release];
    }
    return 0;
}
