#ifndef ISOFORM_WINDOW_DIAGNOSTICS_DARWIN_H
#define ISOFORM_WINDOW_DIAGNOSTICS_DARWIN_H

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// Structural fields only: never read titles or WebView content, even when
// diagnostics are enabled. NSApp.windows belongs to this process alone.
static NSDictionary *isoformWindowSnapshot(NSWindow *window) {
    return @{
        @"number": @(window.windowNumber),
        @"class": NSStringFromClass(window.class),
        @"frame": NSStringFromRect(window.frame),
        @"level": @(window.level),
        @"alpha": @(window.alphaValue),
        @"visible": @(window.isVisible),
        @"ownerPID": @(NSProcessInfo.processInfo.processIdentifier),
    };
}

// Log only on the main thread, never windows from other applications.
static void isoformLogWindows(NSString *reason) {
    NSMutableArray *windows = [NSMutableArray array];
    for (NSWindow *window in NSApp.windows) {
        [windows addObject:isoformWindowSnapshot(window)];
    }
    NSData *json = [NSJSONSerialization dataWithJSONObject:windows options:NSJSONWritingSortedKeys error:NULL];
    static NSData *previous = nil;
    if ([reason isEqualToString:@"idle"] && [previous isEqualToData:json]) return;
    [previous release];
    previous = [json copy];
    NSLog(@"[window-debug] reason=%@ windows=%@", reason,
          [[[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding] autorelease]);
}

static void (*isoformOriginalOrderWindow)(id, SEL, NSWindowOrderingMode, NSInteger);
static void isoformDebugOrderWindow(NSWindow *window, SEL selector, NSWindowOrderingMode mode, NSInteger relative) {
    BOOL wasVisible = window.isVisible;
    isoformOriginalOrderWindow(window, selector, mode, relative);
    if (!wasVisible && window.isVisible) {
        isoformLogWindows(@"ordered-on-screen");
        NSLog(@"[window-debug] shown number=%ld class=%@ stack=%@", (long)window.windowNumber,
              NSStringFromClass(window.class), [NSThread.callStackSymbols componentsJoinedByString:@" | "]);
    }
}

static void isoformInstallWindowDiagnostics(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        static dispatch_once_t once;
        dispatch_once(&once, ^{
            Method method = class_getInstanceMethod(NSWindow.class, @selector(orderWindow:relativeTo:));
            isoformOriginalOrderWindow = (void (*)(id, SEL, NSWindowOrderingMode, NSInteger))
                method_setImplementation(method, (IMP)isoformDebugOrderWindow);
            isoformLogWindows(@"installed");
            // Also catch windows AppKit creates internally without using the
            // public orderWindow:relativeTo: path, including hidden AX windows.
            [NSTimer scheduledTimerWithTimeInterval:1.0 repeats:YES block:^(NSTimer *timer) {
                isoformLogWindows(@"idle");
            }];
        });
    });
}

#endif
