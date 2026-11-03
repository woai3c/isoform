//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

static void setPasteboardString(const char *s) {
    if (s == NULL) return;
    @autoreleasepool {
        NSString *text = [NSString stringWithUTF8String:s];
        if (text == nil) return;
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        [pb clearContents];
        [pb setString:text forType:NSPasteboardTypeString];
    }
}
*/
import "C"

import (
	"unsafe"

	"wails-gui/internal/platform/clipboard"
)

// NativeClipboardSetText writes `text` to the macOS general pasteboard via
// NSPasteboard. Preferred over the bundled wails runtime ClipboardSetText,
// which shells out to `pbcopy` — that path fails silently inside a Finder-
// launched hardened-runtime app when the child PATH doesn't include
// /usr/bin, and the promise reports success regardless.
func (a *App) NativeClipboardSetText(text string) error {
	c := C.CString(text)
	defer C.free(unsafe.Pointer(c))
	C.setPasteboardString(c)
	// Isoform's own write is not the user's copy.
	clipboard.NoteOwnWrite()
	return nil
}
