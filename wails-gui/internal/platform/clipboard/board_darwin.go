//go:build darwin

package clipboard

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>
#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

static char *yansu_clipboard_copy_string(NSString *value) {
	const char *utf8 = [value UTF8String];
	if (utf8 == NULL) {
		return NULL;
	}
	size_t length = strlen(utf8);
	char *out = malloc(length + 1);
	if (out == NULL) {
		return NULL;
	}
	memcpy(out, utf8, length + 1);
	return out;
}

// yansu_clipboard_change_count reads the general pasteboard's change
// counter. It never reads contents, so it never shows an alert.
long yansu_clipboard_change_count(void) {
	@autoreleasepool {
		return (long)[[NSPasteboard generalPasteboard] changeCount];
	}
}

// yansu_clipboard_types returns the current pasteboard types joined by
// newlines, malloc'd; the caller frees it. Types are metadata, not
// contents.
char *yansu_clipboard_types(void) {
	char *out = NULL;
	@autoreleasepool {
		NSArray<NSPasteboardType> *types = [[NSPasteboard generalPasteboard] types];
		out = yansu_clipboard_copy_string([(types ?: @[]) componentsJoinedByString:@"\n"]);
	}
	return out;
}

// yansu_clipboard_content_readable reports whether reading contents is
// allowed without macOS asking. From macOS 15.4 the general pasteboard asks
// on programmatic reads unless the user chose Always Allow for Isoform in
// System Settings; before that there is no alert.
bool yansu_clipboard_content_readable(void) {
#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 150400
	if (@available(macOS 15.4, *)) {
		@autoreleasepool {
			return [[NSPasteboard generalPasteboard] accessBehavior] == NSPasteboardAccessBehaviorAlwaysAllow;
		}
	}
	return true;
#else
	// Built with an SDK that cannot ask: on 15.4 and later, never read.
	NSOperatingSystemVersion alerting = {15, 4, 0};
	return ![[NSProcessInfo processInfo] isOperatingSystemAtLeastVersion:alerting];
#endif
}

// yansu_clipboard_front returns the frontmost app's bundle id and name
// joined by a newline, malloc'd. NSWorkspace only: no accessibility call,
// cheap enough for every tick.
char *yansu_clipboard_front(void) {
	char *out = NULL;
	@autoreleasepool {
		NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
		NSString *joined = [NSString stringWithFormat:@"%@\n%@", front.bundleIdentifier ?: @"", front.localizedName ?: @""];
		out = yansu_clipboard_copy_string(joined);
	}
	return out;
}

// yansu_clipboard_self_bundle returns Isoform's own bundle id, malloc'd.
char *yansu_clipboard_self_bundle(void) {
	char *out = NULL;
	@autoreleasepool {
		out = yansu_clipboard_copy_string([[NSBundle mainBundle] bundleIdentifier] ?: @"");
	}
	return out;
}

// yansu_clipboard_read_text returns at most maxUnits UTF-16 units of the
// plain text, cut on a character boundary, malloc'd; *length is the whole
// text's length and *source the org.nspasteboard.source value (malloc'd or
// NULL). It returns NULL when there is no text. Call it only when
// yansu_clipboard_content_readable is true.
char *yansu_clipboard_read_text(long maxUnits, long *length, char **source) {
	*length = 0;
	*source = NULL;
	char *out = NULL;
	@autoreleasepool {
		NSPasteboard *board = [NSPasteboard generalPasteboard];
		NSString *text = [board stringForType:NSPasteboardTypeString];
		if (text == nil) {
			return NULL;
		}
		*length = (long)[text length];
		if ((long)[text length] > maxUnits && maxUnits > 0) {
			NSRange range = [text rangeOfComposedCharacterSequencesForRange:NSMakeRange(0, (NSUInteger)maxUnits)];
			if (NSMaxRange(range) > (NSUInteger)maxUnits && range.length > 0) {
				range = [text rangeOfComposedCharacterSequenceAtIndex:(NSUInteger)maxUnits - 1];
				range = NSMakeRange(0, range.location);
			}
			text = [text substringWithRange:range];
		}
		out = yansu_clipboard_copy_string(text);
		NSString *from = [board stringForType:@"org.nspasteboard.source"];
		if (from.length > 0) {
			*source = yansu_clipboard_copy_string(from);
		}
	}
	return out;
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

// SystemBoard is the macOS general pasteboard.
type SystemBoard struct{}

// Supported reports whether this platform has a pasteboard to watch.
func Supported() bool { return true }

func (SystemBoard) ChangeCount() int { return int(C.yansu_clipboard_change_count()) }

func (SystemBoard) Types() []string {
	raw := C.yansu_clipboard_types()
	if raw == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(raw))
	joined := C.GoString(raw)
	if joined == "" {
		return nil
	}
	return strings.Split(joined, "\n")
}

func (SystemBoard) ContentReadable() bool { return bool(C.yansu_clipboard_content_readable()) }

func (SystemBoard) ReadText(maxUnits int) (string, int, string, bool) {
	var length C.long
	var source *C.char
	raw := C.yansu_clipboard_read_text(C.long(maxUnits), &length, &source)
	from := ""
	if source != nil {
		from = C.GoString(source)
		C.free(unsafe.Pointer(source))
	}
	if raw == nil {
		return "", 0, from, false
	}
	defer C.free(unsafe.Pointer(raw))
	return C.GoString(raw), int(length), from, true
}

// FrontApp is the frontmost app's bundle id and name, without any
// accessibility call.
func FrontApp() App {
	raw := C.yansu_clipboard_front()
	if raw == nil {
		return App{}
	}
	defer C.free(unsafe.Pointer(raw))
	bundle, name, _ := strings.Cut(C.GoString(raw), "\n")
	return App{BundleID: bundle, Name: name}
}

// SelfBundleID is Isoform's own bundle id; empty when running unbundled.
func SelfBundleID() string {
	raw := C.yansu_clipboard_self_bundle()
	if raw == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(raw))
	return C.GoString(raw)
}

// NoteOwnWrite records that Isoform itself just wrote to the pasteboard, so
// the watcher never reports that change as the user's copy. Call it right
// after the write.
func NoteOwnWrite() { noteOwnWrite(SystemBoard{}.ChangeCount()) }
