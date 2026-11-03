//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#import <stdlib.h>

// appIconBase64PNG renders the macOS-resolved icon for the .app at path to a
// `size`x`size` PNG and returns it as a base64 string (caller-owned). Returns
// NULL on any failure. Uses NSWorkspace, so it handles every icon format the
// system knows about: .icns (CFBundleIconFile) and asset catalogs (Assets.car
// referenced by CFBundleIconName) alike.
static char *appIconBase64PNG(const char *path, int size) {
    if (path == NULL) return NULL;
    @autoreleasepool {
        NSString *p = [NSString stringWithUTF8String:path];
        if (p == nil) return NULL;
        NSImage *icon = [[NSWorkspace sharedWorkspace] iconForFile:p];
        if (icon == nil) return NULL;

        NSSize target = NSMakeSize((CGFloat)size, (CGFloat)size);
        NSBitmapImageRep *rep = [[NSBitmapImageRep alloc]
            initWithBitmapDataPlanes:NULL
                          pixelsWide:(NSInteger)size
                          pixelsHigh:(NSInteger)size
                       bitsPerSample:8
                     samplesPerPixel:4
                            hasAlpha:YES
                            isPlanar:NO
                      colorSpaceName:NSDeviceRGBColorSpace
                         bytesPerRow:0
                        bitsPerPixel:32];
        if (rep == nil) return NULL;
        rep.size = target;

        NSGraphicsContext *ctx = [NSGraphicsContext graphicsContextWithBitmapImageRep:rep];
        if (ctx == nil) return NULL;
        [NSGraphicsContext saveGraphicsState];
        [NSGraphicsContext setCurrentContext:ctx];
        [icon drawInRect:NSMakeRect(0, 0, (CGFloat)size, (CGFloat)size)
                fromRect:NSZeroRect
               operation:NSCompositingOperationSourceOver
                fraction:1.0
          respectFlipped:YES
                   hints:nil];
        [NSGraphicsContext restoreGraphicsState];

        NSData *png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
        if (png == nil) return NULL;
        NSString *b64 = [png base64EncodedStringWithOptions:0];
        const char *cstr = [b64 UTF8String];
        if (cstr == NULL) return NULL;
        return strdup(cstr);
    }
}

// appPathForBundleID resolves the filesystem path of the app registered for
// `bundleID` via LaunchServices (caller-owned string, NULL when unknown).
// Unlike a /Applications directory scan this covers apps installed anywhere
// the system knows about — homebrew casks, ~/Downloads launches, DriveFS, etc.
static char *appPathForBundleID(const char *bundleID) {
    if (bundleID == NULL) return NULL;
    @autoreleasepool {
        NSString *bid = [NSString stringWithUTF8String:bundleID];
        if (bid == nil) return NULL;
        NSURL *url = [[NSWorkspace sharedWorkspace] URLForApplicationWithBundleIdentifier:bid];
        if (url == nil || url.path == nil) return NULL;
        const char *cstr = [url.path UTF8String];
        if (cstr == NULL) return NULL;
        return strdup(cstr);
    }
}
*/
import "C"

import (
	"unsafe"
)

// nativeAppIconBase64 returns the macOS-resolved app icon as a 64x64 base64
// PNG data URL, the same format used by getAppIconBase64. 64px (not 32) so
// icons rendered at up to 32pt stay sharp on 2x Retina displays. Uses
// NSWorkspace so it works for every icon format (icns + Assets.car). Returns
// "" on failure; callers should fall back to the existing CFBundleIconFile
// path.
func nativeAppIconBase64(appPath string) string {
	cPath := C.CString(appPath)
	defer C.free(unsafe.Pointer(cPath))
	cstr := C.appIconBase64PNG(cPath, 64)
	if cstr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cstr))
	return "data:image/png;base64," + C.GoString(cstr)
}

// nativeAppIconForBundleID resolves a bundle id to its app's icon as a 64x64
// base64 PNG data URL via LaunchServices, independent of any directory scan.
// Returns "" when the bundle id is unknown to the system.
func nativeAppIconForBundleID(bundleID string) string {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	cPath := C.appPathForBundleID(cBundleID)
	if cPath == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cPath))
	return nativeAppIconBase64(C.GoString(cPath))
}
