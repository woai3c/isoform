//go:build darwin

package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices

#include <CoreGraphics/CoreGraphics.h>
#include <stdbool.h>

// Get screen logical dimensions.
void get_screen_dimensions(int* outW, int* outH) {
    CGDirectDisplayID mainDisplay = CGMainDisplayID();
    *outW = (int)CGDisplayPixelsWide(mainDisplay);
    *outH = (int)CGDisplayPixelsHigh(mainDisplay);
}

// Check if the screen is locked using CGSessionCopyCurrentDictionary.
// Returns true when the lock screen / screensaver is active.
static bool is_screen_locked(void) {
    CFDictionaryRef session = CGSessionCopyCurrentDictionary();
    if (!session) return false;

    bool locked = false;
    CFBooleanRef val = CFDictionaryGetValue(session, CFSTR("CGSSessionScreenIsLocked"));
    if (val) locked = CFBooleanGetValue(val);
    CFRelease(session);
    return locked;
}

// True while the main display sleeps.
static bool is_display_asleep(void) {
    return CGDisplayIsAsleep(CGMainDisplayID());
}
*/
import "C"

import (
	"bytes"
	"image"
	"image/jpeg"
)

// darwinScreenCapture implements ScreenCapture using CaptureScreen (CGWindowListCreateImage).
type darwinScreenCapture struct{}

// NewScreenCapture creates a platform-specific ScreenCapture.
func NewScreenCapture() ScreenCapture {
	return &darwinScreenCapture{}
}

func (c *darwinScreenCapture) CaptureRaw() ([]byte, int, int, error) {
	// CaptureScreen with (-1,-1) returns BGRA pixel data at full logical resolution.
	pixels, w, h, err := CaptureScreen(-1, -1)
	if err != nil {
		return nil, 0, 0, err
	}

	// Convert BGRA → RGBA in-place
	bpr := w * 4
	for y := range h {
		rowOff := y * bpr
		for x := range w {
			off := rowOff + x*4
			pixels[off], pixels[off+2] = pixels[off+2], pixels[off]
		}
	}

	return pixels, w, h, nil
}

// IsScreenLocked returns true when the macOS lock screen or screensaver is active.
func IsScreenLocked() bool {
	return bool(C.is_screen_locked())
}

// IsDisplayAsleep returns true while the main display sleeps.
func IsDisplayAsleep() bool {
	return bool(C.is_display_asleep())
}

// ScreenDimensions returns the logical screen width and height.
func ScreenDimensions() (int, int) {
	var w, h C.int
	C.get_screen_dimensions(&w, &h)
	return int(w), int(h)
}

func (c *darwinScreenCapture) EncodeJPEG(rgbaPixels []byte, w, h, quality int) ([]byte, error) {
	if quality <= 0 || quality > 100 {
		quality = 80
	}

	img := &image.NRGBA{
		Pix:    rgbaPixels,
		Stride: w * 4,
		Rect:   image.Rect(0, 0, w, h),
	}

	var buf bytes.Buffer
	buf.Grow(w * h / 4)
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
