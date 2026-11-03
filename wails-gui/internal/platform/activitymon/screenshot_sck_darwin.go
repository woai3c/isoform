//go:build darwin

// Per-window screenshot via ScreenCaptureKit. CGWindowListCreateImage was
// obsoleted in macOS 15, and SCK captures GPU-accelerated windows (Chrome,
// Electron, WKWebView) cleanly by windowID — which full-screen capture +
// focus-rect cropping cannot do reliably.
package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ScreenCaptureKit -framework CoreMedia -framework CoreGraphics -framework ImageIO -framework Cocoa

#include "main_queue_darwin.h"
#import <Cocoa/Cocoa.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <stdatomic.h>
#include <dispatch/dispatch.h>
#include "capture_geometry_darwin.h"

// frontmost_window_id returns the CGWindowID of the largest on-screen
// normal-layer window owned by the frontmost application (its main window — the
// "largest" rule skips thin overlay/title-strip windows that sit ahead of it in
// z-order). outBundle receives that application's bundle id, sampled together
// with the window so callers can tell whether focus moved since they last
// sampled the frontmost app.
static uint32_t frontmost_window_id(char* outOwner, int ownerLen, char* outBundle, int bundleLen, int* outPid) {
    @autoreleasepool {
        if (outOwner && ownerLen > 0) outOwner[0] = '\0';
        if (outBundle && bundleLen > 0) outBundle[0] = '\0';
        if (outPid) *outPid = 0;
        NSRunningApplication *front = activitymon_frontmost_app();
        if (!front) return 0;
        pid_t pid = front.processIdentifier;
        if (outPid) *outPid = (int)pid;
        const char *bid = [front.bundleIdentifier UTF8String];
        if (bid && outBundle && bundleLen > 0) {
            strncpy(outBundle, bid, bundleLen - 1);
            outBundle[bundleLen - 1] = '\0';
        }
        CFArrayRef list = CGWindowListCopyWindowInfo(
            kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
        if (!list) return 0;
        uint32_t best = 0;
        double bestArea = 0;
        CFIndex n = CFArrayGetCount(list);
        for (CFIndex i = 0; i < n; i++) {
            CFDictionaryRef info = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
            CFNumberRef pidRef = (CFNumberRef)CFDictionaryGetValue(info, kCGWindowOwnerPID);
            if (!pidRef) continue;
            pid_t winPid = 0; CFNumberGetValue(pidRef, kCFNumberIntType, &winPid);
            if (winPid != pid) continue;
            CFNumberRef layerRef = (CFNumberRef)CFDictionaryGetValue(info, kCGWindowLayer);
            int layer = 0; if (layerRef) CFNumberGetValue(layerRef, kCFNumberIntType, &layer);
            if (layer != 0) continue;
            CFDictionaryRef b = (CFDictionaryRef)CFDictionaryGetValue(info, kCGWindowBounds);
            if (!b) continue;
            CGRect r; if (!CGRectMakeWithDictionaryRepresentation(b, &r)) continue;
            double area = r.size.width * r.size.height;
            if (area > bestArea) {
                bestArea = area;
                CFNumberRef numRef = (CFNumberRef)CFDictionaryGetValue(info, kCGWindowNumber);
                if (numRef) { uint32_t wid = 0; CFNumberGetValue(numRef, kCFNumberIntType, &wid); best = wid; }
                // Owner app name straight from the window list — reliable even
                // when NSWorkspace's frontmostApplication reports an empty
                // bundleId/localizedName (Termius, AirJelly, ...).
                CFStringRef ownerRef = (CFStringRef)CFDictionaryGetValue(info, kCGWindowOwnerName);
                if (ownerRef && outOwner && ownerLen > 0) {
                    CFStringGetCString(ownerRef, outOwner, ownerLen, kCFStringEncodingUTF8);
                }
            }
        }
        CFRelease(list);
        return best;
    }
}

// cap_ctx is a heap-allocated, reference-counted capture context shared between
// capture_window_jpeg and the async SCK completion blocks. It lets us wait with a
// BOUNDED timeout (so a stuck SCK callback never holds the caller's global
// capture lock forever) without the use-after-free a stack-based context would
// cause: a late callback writes/signals the still-alive heap context, and the
// last reference frees it.
typedef struct {
    dispatch_semaphore_t sem;
    CGImageRef result;
    int width;
    int height;
    atomic_int rc;
} cap_ctx;

static void cap_ctx_unref(cap_ctx *c) {
    if (atomic_fetch_sub_explicit(&c->rc, 1, memory_order_acq_rel) == 1) {
        if (c->result) CGImageRelease(c->result);
        if (c->sem) dispatch_release(c->sem);
        free(c);
    }
}

// capture_window_jpeg captures a single window via ScreenCaptureKit and
// JPEG-encodes it (snapshot files are .jpg). quality is 0-100. outW/outH receive
// the captured pixel dimensions so the Go caller can store correct metadata.
// Returns NULL on failure or timeout. Uses ImageIO (thread-safe) for encoding.
static unsigned char* capture_window_jpeg(uint32_t windowID, int quality, int* outLen, int* outW, int* outH) {
    @autoreleasepool {
        cap_ctx *ctx = (cap_ctx*)calloc(1, sizeof(cap_ctx));
        if (!ctx) return NULL;
        ctx->sem = dispatch_semaphore_create(0);
        atomic_init(&ctx->rc, 2); // held by: this function + the async chain

        [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *content, NSError *error) {
            @autoreleasepool {
                if (error || !content) { dispatch_semaphore_signal(ctx->sem); cap_ctx_unref(ctx); return; }
                SCWindow *target = nil;
                for (SCWindow *w in content.windows) {
                    if (w.windowID == windowID) { target = w; break; }
                }
                if (!target) { dispatch_semaphore_signal(ctx->sem); cap_ctx_unref(ctx); return; }
                SCContentFilter *filter = [[[SCContentFilter alloc] initWithDesktopIndependentWindow:target] autorelease];
                SCStreamConfiguration *cfg = [[[SCStreamConfiguration alloc] init] autorelease];
                // SCK completes on a background queue. NSScreen is AppKit
                // state; read the filter's own scale instead (also correct
                // when the target moved to a different display).
                if (!capture_pixel_dimensions(target.frame.size.width, target.frame.size.height,
                                              filter.pointPixelScale, &ctx->width, &ctx->height)) {
                    dispatch_semaphore_signal(ctx->sem);
                    cap_ctx_unref(ctx);
                    return;
                }
                cfg.width = (size_t)ctx->width;
                cfg.height = (size_t)ctx->height;
                [SCScreenshotManager captureImageWithFilter:filter configuration:cfg
                    completionHandler:^(CGImageRef img, NSError *err) {
                        @autoreleasepool {
                            if (!err && img) ctx->result = CGImageRetain(img);
                            dispatch_semaphore_signal(ctx->sem);
                            cap_ctx_unref(ctx); // release the async-chain ref
                        }
                    }];
                // The async-chain ref is transferred to the captureImage
                // completion above; this block does NOT unref.
            }
        }];

        // Bounded wait: never hold the caller's global capture lock forever. If
        // SCK fails to call back (TCC/WindowServer edge), abandon the context. The
        // refcount keeps a late callback safe — it writes the still-alive heap
        // context and the last unref frees it (leaking one small ctx in that rare
        // case, vs. stalling all captures until restart).
        long timedOut = dispatch_semaphore_wait(ctx->sem,
            dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));

        unsigned char* out = NULL;
        if (timedOut == 0 && ctx->result) {
            double q = quality / 100.0; if (q < 0) q = 0; if (q > 1) q = 1;
            CFMutableDataRef jpeg = CFDataCreateMutable(kCFAllocatorDefault, 0);
            CGImageDestinationRef dest = CGImageDestinationCreateWithData(jpeg, CFSTR("public.jpeg"), 1, NULL);
            if (dest) {
                NSDictionary *props = @{(id)kCGImageDestinationLossyCompressionQuality: @(q)};
                CGImageDestinationAddImage(dest, ctx->result, (CFDictionaryRef)props);
                if (CGImageDestinationFinalize(dest)) {
                    int len = (int)CFDataGetLength(jpeg);
                    unsigned char* buf = (unsigned char*)malloc(len);
                    if (buf) {
                        CFDataGetBytes(jpeg, CFRangeMake(0, len), buf);
                        *outLen = len;
                        *outW = ctx->width;
                        *outH = ctx->height;
                        out = buf;
                    }
                }
                CFRelease(dest);
            }
            CFRelease(jpeg);
        }
        cap_ctx_unref(ctx); // release this function's ref
        return out;
    }
}
*/
import "C"
import "unsafe"

// SupportsWindowCapture reports whether per-window capture is available. It is
// true on macOS (ScreenCaptureKit), so a 0 window ID from FrontmostWindowInfo means
// the frontmost app has no normal window and callers should skip the frame.
func SupportsWindowCapture() bool {
	return true
}

// FrontmostWindowInfo returns the frontmost app's largest window (ID 0 if
// none), that window's owner app name from the window list — useful when
// NSWorkspace's frontmostApplication reports an empty bundleId/localizedName —
// and the bundle id and pid of the app the window was picked for.
func FrontmostWindowInfo() FrontmostWindow {
	var owner [256]C.char
	var bundle [256]C.char
	var pid C.int
	wid := C.frontmost_window_id(&owner[0], 256, &bundle[0], 256, &pid)
	return FrontmostWindow{
		ID:        uint32(wid),
		OwnerName: C.GoString(&owner[0]),
		BundleID:  C.GoString(&bundle[0]),
		PID:       int(pid),
	}
}

// CaptureWindowJPEG captures a window and returns JPEG bytes (quality 0-100) plus
// the captured pixel width and height, or (nil, 0, 0) on failure (window gone, no
// Screen Recording permission, or capture timeout).
func CaptureWindowJPEG(windowID uint32, quality int) ([]byte, int, int) {
	var n, width, height C.int
	buf := C.capture_window_jpeg(C.uint32_t(windowID), C.int(quality), &n, &width, &height)
	if buf == nil || n <= 0 {
		return nil, 0, 0
	}
	defer C.free(unsafe.Pointer(buf))
	return C.GoBytes(unsafe.Pointer(buf), n), int(width), int(height)
}
