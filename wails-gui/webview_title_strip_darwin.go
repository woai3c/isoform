//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit

#include <objc/runtime.h>
#include <os/lock.h>
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// Let the first click on an inactive window reach the page in the title
// strip, so dragging there moves the window like a real title bar.
//
// Wails drags the window from JavaScript: its mousedown handler checks
// --wails-draggable on the element under the pointer and asks the native
// side to start the drag. On an inactive window that mousedown never
// happens: WKWebView accepts the activating click only over a text
// selection (WebKit's acceptsFirstMouse), so the click only brings the
// window forward. Above the strip macOS's own title bar (28pt) still drags,
// which is why the sidebar's strip seemed to work while the lower part of
// the main panel's 44pt strip did nothing until the window was active.
//
// WailsWebView does not implement acceptsFirstMouse:, so it is added to that
// subclass only (other WKWebViews keep WebKit's answer). It says yes only
// over the strip's draggable areas, which the page reports
// (title-strip-drag-regions.ts): a dialog, the onboarding screen or a
// control in the strip is not draggable there, so its first click still
// only activates the window, as everywhere on macOS.

static const CGFloat yansuTitleStripHeight = 44.0; // App.tsx: the h-[44px] title strip
static BOOL (*yansuWebKitAcceptsFirstMouse)(id, SEL, NSEvent *) = NULL;

#define YANSU_STRIP_MAX_RECTS 64
static os_unfair_lock yansuStripLock = OS_UNFAIR_LOCK_INIT;
static CGRect yansuStripRects[YANSU_STRIP_MAX_RECTS];
static int yansuStripRectCount = 0;

// xywh holds n rects as x, y, width, height in page points from the
// window's top-left corner.
static void yansuSetTitleStripDragRects(const double *xywh, int n) {
	if (n > YANSU_STRIP_MAX_RECTS) n = YANSU_STRIP_MAX_RECTS;
	if (n < 0) n = 0;
	os_unfair_lock_lock(&yansuStripLock);
	for (int i = 0; i < n; i++) {
		yansuStripRects[i] = CGRectMake(xywh[4*i], xywh[4*i+1], xywh[4*i+2], xywh[4*i+3]);
	}
	yansuStripRectCount = n;
	os_unfair_lock_unlock(&yansuStripLock);
}

static BOOL yansuOverStripDragRect(CGFloat x, CGFloat y) {
	BOOL hit = NO;
	os_unfair_lock_lock(&yansuStripLock);
	for (int i = 0; i < yansuStripRectCount && !hit; i++) {
		hit = CGRectContainsPoint(yansuStripRects[i], CGPointMake(x, y));
	}
	os_unfair_lock_unlock(&yansuStripLock);
	return hit;
}

static BOOL yansuWebViewAcceptsFirstMouse(id self, SEL _cmd, NSEvent *event) {
	NSView *view = (NSView *)self;
	if (event != nil && [view window] != nil) {
		NSPoint p = [view convertPoint:[event locationInWindow] fromView:nil];
		CGFloat fromTop = [view isFlipped] ? p.y : NSHeight([view bounds]) - p.y;
		if (fromTop >= 0 && fromTop < yansuTitleStripHeight && yansuOverStripDragRect(p.x, fromTop)) {
			return YES;
		}
	}
	return yansuWebKitAcceptsFirstMouse ? yansuWebKitAcceptsFirstMouse(self, _cmd, event) : NO;
}

// 0: WailsWebView or WKWebView not found, 1: already installed,
// 2: installed.
static int yansuInstallTitleStripFirstMouse(void) {
	Class webViewClass = objc_getClass("WailsWebView");
	Class webKitClass = objc_getClass("WKWebView");
	if (!webViewClass || !webKitClass) return 0;
	SEL sel = @selector(acceptsFirstMouse:);
	Method inherited = class_getInstanceMethod(webKitClass, sel);
	if (!inherited) return 0;
	if (yansuWebKitAcceptsFirstMouse != NULL) return 1;
	yansuWebKitAcceptsFirstMouse = (BOOL (*)(id, SEL, NSEvent *))method_getImplementation(inherited);
	if (class_addMethod(webViewClass, sel, (IMP)yansuWebViewAcceptsFirstMouse, method_getTypeEncoding(inherited))) {
		return 2;
	}
	// A Wails version that implements it itself: wrap its implementation.
	Method own = class_getInstanceMethod(webViewClass, sel);
	yansuWebKitAcceptsFirstMouse = (BOOL (*)(id, SEL, NSEvent *))method_setImplementation(own, (IMP)yansuWebViewAcceptsFirstMouse);
	return 2;
}
*/
import "C"

import "log"

// patchWebViewTitleStripFirstMouse makes the first click on an inactive
// window reach the page over the title strip's draggable areas, so the strip
// drags the window on that click. Call it after wails.Run() has loaded the
// Wails classes (OnStartup or later).
func patchWebViewTitleStripFirstMouse() {
	switch C.yansuInstallTitleStripFirstMouse() {
	case 0:
		log.Println("[window-drag] title strip first click not installed: WailsWebView or WKWebView not found")
	case 1:
		log.Println("[window-drag] title strip first click already installed")
	case 2:
		log.Println("[window-drag] title strip takes the first click on an inactive window")
	}
}

// setTitleStripDragRegions replaces the strip's draggable areas.
func setTitleStripDragRegions(rects []TitleStripRect) {
	if len(rects) == 0 {
		C.yansuSetTitleStripDragRects(nil, 0)
		return
	}
	xywh := make([]C.double, 0, 4*len(rects))
	for _, r := range rects {
		xywh = append(xywh, C.double(r.X), C.double(r.Y), C.double(r.Width), C.double(r.Height))
	}
	C.yansuSetTitleStripDragRects(&xywh[0], C.int(len(rects)))
}
