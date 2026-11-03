//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework WebKit

#include <stdio.h>
#include <string.h>
#include <dispatch/dispatch.h>
#include <objc/runtime.h>
#include <objc/message.h>
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// Grant WKWebView media capture requests (microphone for voice input).
//
// Wails v2 sets its WailsContext as the WKWebView's UIDelegate but does
// not implement -webView:requestMediaCapturePermissionForOrigin:..., so
// WKWebView denies all getUserMedia() calls by default on macOS 12+.
//
// We inject this method into WailsContext at runtime using class_addMethod
// so the existing delegate gains media-capture support without patching
// the framework.
//
// WKPermissionDecisionGrant == 1
static void handleMediaCapturePermission(
	id self, SEL _cmd, id webView, id origin,
	id frame, long type, id decisionHandler
) {
	fprintf(stderr, "[media-capture] delegate called, granting permission (type=%ld)\n", type);
	// Cast decisionHandler to a block and invoke with Grant (1).
	void (^handler)(long) = (__bridge void (^)(long))decisionHandler;
	handler(1);
}

static const char *mediaCaptureDelegateClassName = "WailsContext";
static const char *mediaCaptureSelectorName =
	"webView:requestMediaCapturePermissionForOrigin:"
	"initiatedByFrame:type:decisionHandler:";
static void (*originalSetUIDelegateIMP)(id, SEL, id) = NULL;

static void ensureMediaCaptureSelectorForDelegate(id delegate) {
	if (!delegate) return;

	Class cls = object_getClass(delegate);
	if (!cls) return;

	const char *className = class_getName(cls);
	if (!className || strcmp(className, mediaCaptureDelegateClassName) != 0) {
		return;
	}

	SEL sel = sel_registerName(mediaCaptureSelectorName);
	if (class_respondsToSelector(cls, sel)) {
		return;
	}

	BOOL ok = class_addMethod(cls, sel, (IMP)handleMediaCapturePermission, "v@:@@@q@?");
	fprintf(stderr, "[media-capture] pre-assign class_addMethod via setUIDelegate: %s\n", ok ? "SUCCESS" : "FAILED");
}

static void yansuSetUIDelegate(id self, SEL _cmd, id delegate) {
	ensureMediaCaptureSelectorForDelegate(delegate);
	if (originalSetUIDelegateIMP) {
		originalSetUIDelegateIMP(self, _cmd, delegate);
	}
}

__attribute__((constructor))
static void installMediaCaptureUIDelegateSwizzle(void) {
	Class wkWebViewClass = objc_getClass("WKWebView");
	if (!wkWebViewClass) {
		fprintf(stderr, "[media-capture] WKWebView class NOT found for setUIDelegate swizzle\n");
		return;
	}

	Method method = class_getInstanceMethod(wkWebViewClass, sel_registerName("setUIDelegate:"));
	if (!method) {
		fprintf(stderr, "[media-capture] setUIDelegate: method NOT found for swizzle\n");
		return;
	}

	originalSetUIDelegateIMP = (void (*)(id, SEL, id))method_getImplementation(method);
	method_setImplementation(method, (IMP)yansuSetUIDelegate);
	fprintf(stderr, "[media-capture] installed WKWebView setUIDelegate swizzle\n");
}

// patchWailsMediaCapture adds the media-capture delegate method to the
// WailsContext class.  Safe to call multiple times — returns 0 if the
// class is not found, 1 if already patched, 2 if successfully added.
static int patchWailsMediaCapture(void) {
	Class cls = objc_getClass("WailsContext");
	if (!cls) {
		fprintf(stderr, "[media-capture] WailsContext class NOT found\n");
		return 0;
	}

	SEL sel = sel_registerName(mediaCaptureSelectorName);

	if (class_respondsToSelector(cls, sel)) {
		fprintf(stderr, "[media-capture] WailsContext already responds to media capture selector\n");
		return 1;
	}

	// Method signature: void (id self, SEL _cmd, WKWebView*, WKSecurityOrigin*,
	//                         WKFrameInfo*, WKMediaCaptureType, block)
	// ObjC type encoding: v@:@@@q@? (void, id, SEL, id, id, id, long, block)
	BOOL ok = class_addMethod(cls, sel, (IMP)handleMediaCapturePermission, "v@:@@@q@?");
	fprintf(stderr, "[media-capture] class_addMethod result: %s\n", ok ? "SUCCESS" : "FAILED");
	if (!ok) return 0;

	// After adding the method, re-assign the UIDelegate on every WKWebView
	// owned by a WailsContext instance so WebKit re-queries respondsToSelector.
	// Without this, WebKit caches the delegate capabilities from when it was
	// first assigned and never sees our newly-added method.
	dispatch_async(dispatch_get_main_queue(), ^{
		id app = ((id (*)(id, SEL))objc_msgSend)((id)objc_getClass("NSApplication"),
			sel_registerName("sharedApplication"));
		id windows = ((id (*)(id, SEL))objc_msgSend)(app, sel_registerName("windows"));
		long winCount = ((long (*)(id, SEL))objc_msgSend)(windows, sel_registerName("count"));

		int patched = 0;
		for (long i = 0; i < winCount; i++) {
			id window = ((id (*)(id, SEL, long))objc_msgSend)(windows,
				sel_registerName("objectAtIndex:"), i);
			id contentView = ((id (*)(id, SEL))objc_msgSend)(window,
				sel_registerName("contentView"));
			id subviews = ((id (*)(id, SEL))objc_msgSend)(contentView,
				sel_registerName("subviews"));
			long subCount = ((long (*)(id, SEL))objc_msgSend)(subviews,
				sel_registerName("count"));

			for (long j = 0; j < subCount; j++) {
				id view = ((id (*)(id, SEL, long))objc_msgSend)(subviews,
					sel_registerName("objectAtIndex:"), j);
				if ([(NSObject *)view isKindOfClass:objc_getClass("WKWebView")]) {
					id currentDelegate = ((id (*)(id, SEL))objc_msgSend)(view,
						sel_registerName("UIDelegate"));
					// Re-assign to force WebKit to re-query capabilities.
					((void (*)(id, SEL, id))objc_msgSend)(view,
						sel_registerName("setUIDelegate:"), nil);
					((void (*)(id, SEL, id))objc_msgSend)(view,
						sel_registerName("setUIDelegate:"), currentDelegate);
					patched++;
				}
			}
		}
		fprintf(stderr, "[media-capture] re-assigned UIDelegate on %d WKWebView(s)\n", patched);
	});

	return 2;
}
*/
import "C"

import "log"

// patchWKWebViewMediaCapture injects the media-capture delegate method into
// the Wails WKWebView UIDelegate.  Must be called after wails.Run() has
// created the WailsContext (i.e. from OnStartup or later).
func patchWKWebViewMediaCapture() {
	result := C.patchWailsMediaCapture()
	switch result {
	case 0:
		log.Println("[media-capture] patch FAILED: WailsContext not found or class_addMethod failed")
	case 1:
		log.Println("[media-capture] patch skipped: method already exists")
	case 2:
		log.Println("[media-capture] patch applied successfully")
	}
}
