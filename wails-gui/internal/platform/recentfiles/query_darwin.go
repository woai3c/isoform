//go:build darwin

package recentfiles

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreServices -framework Foundation
#include <stdlib.h>
#include <string.h>
#import <CoreServices/CoreServices.h>
#import <Foundation/Foundation.h>

// Codes returned through yansu_spotlight_query.
enum {
	YANSU_SPOTLIGHT_OK = 0,
	YANSU_SPOTLIGHT_BAD_QUERY = 1,
	YANSU_SPOTLIGHT_FAILED = 2,
};

static NSNumber *yansu_spotlight_millis(MDItemRef item, CFStringRef attr) {
	CFTypeRef value = MDItemCopyAttribute(item, attr);
	if (value == NULL) {
		return @0;
	}
	NSNumber *out = @0;
	if (CFGetTypeID(value) == CFDateGetTypeID()) {
		out = @((long long)(CFDateGetAbsoluteTime((CFDateRef)value) * 1000.0 + kCFAbsoluteTimeIntervalSince1970 * 1000.0));
	}
	CFRelease(value);
	return out;
}

static NSString *yansu_spotlight_string(MDItemRef item, CFStringRef attr) {
	CFTypeRef value = MDItemCopyAttribute(item, attr);
	if (value == NULL) {
		return @"";
	}
	NSString *out = @"";
	if (CFGetTypeID(value) == CFStringGetTypeID()) {
		out = [[(NSString *)value copy] autorelease];
	}
	CFRelease(value);
	return out;
}

static NSArray *yansu_spotlight_strings(MDItemRef item, CFStringRef attr) {
	CFTypeRef value = MDItemCopyAttribute(item, attr);
	if (value == NULL) {
		return @[];
	}
	NSMutableArray *out = [NSMutableArray array];
	if (CFGetTypeID(value) == CFArrayGetTypeID()) {
		for (id entry in (NSArray *)value) {
			if ([entry isKindOfClass:[NSString class]]) {
				[out addObject:entry];
			}
		}
	}
	CFRelease(value);
	return out;
}

// yansu_spotlight_query runs one synchronous Spotlight query inside scope and
// returns a malloc'd JSON array of {path, name, type, tree, lastUsed,
// modified, created, added}, dates in Unix milliseconds. Only metadata from
// Spotlight's index is read; no file is opened. It never shows a dialog.
char *yansu_spotlight_query(const char *text, const char *scope, int limit, int *status) {
	*status = YANSU_SPOTLIGHT_OK;
	@autoreleasepool {
		NSString *queryText = [NSString stringWithUTF8String:text];
		NSString *scopePath = [NSString stringWithUTF8String:scope];
		if (queryText == nil || scopePath == nil) {
			*status = YANSU_SPOTLIGHT_BAD_QUERY;
			return NULL;
		}
		MDQueryRef query = MDQueryCreate(kCFAllocatorDefault, (CFStringRef)queryText, NULL, NULL);
		if (query == NULL) {
			*status = YANSU_SPOTLIGHT_BAD_QUERY;
			return NULL;
		}
		MDQuerySetSearchScope(query, (CFArrayRef)@[scopePath], 0);
		if (limit > 0) {
			MDQuerySetMaxCount(query, limit);
		}
		if (!MDQueryExecute(query, kMDQuerySynchronous)) {
			CFRelease(query);
			*status = YANSU_SPOTLIGHT_FAILED;
			return NULL;
		}
		MDQueryStop(query);
		CFIndex count = MDQueryGetResultCount(query);
		NSMutableArray *out = [NSMutableArray arrayWithCapacity:(NSUInteger)count];
		for (CFIndex i = 0; i < count; i++) {
			MDItemRef item = (MDItemRef)MDQueryGetResultAtIndex(query, i);
			if (item == NULL) {
				continue;
			}
			NSString *path = yansu_spotlight_string(item, kMDItemPath);
			if (path.length == 0) {
				continue;
			}
			NSNumber *modified = yansu_spotlight_millis(item, kMDItemContentModificationDate);
			if (modified.longLongValue == 0) {
				modified = yansu_spotlight_millis(item, kMDItemFSContentChangeDate);
			}
			[out addObject:@{
				@"path": path,
				@"name": yansu_spotlight_string(item, kMDItemFSName),
				@"type": yansu_spotlight_string(item, kMDItemContentType),
				@"tree": yansu_spotlight_strings(item, kMDItemContentTypeTree),
				@"lastUsed": yansu_spotlight_millis(item, kMDItemLastUsedDate),
				@"modified": modified,
				@"created": yansu_spotlight_millis(item, kMDItemFSCreationDate),
				@"added": yansu_spotlight_millis(item, kMDItemDateAdded),
			}];
		}
		CFRelease(query);
		NSData *json = [NSJSONSerialization dataWithJSONObject:out options:0 error:nil];
		if (json == nil) {
			*status = YANSU_SPOTLIGHT_FAILED;
			return NULL;
		}
		char *buf = malloc(json.length + 1);
		if (buf == NULL) {
			*status = YANSU_SPOTLIGHT_FAILED;
			return NULL;
		}
		memcpy(buf, json.bytes, json.length);
		buf[json.length] = 0;
		return buf;
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

var errQueryFailed = errors.New("spotlight query failed")

// Supported reports whether this platform has Spotlight.
func Supported() bool { return true }

// Query runs one Spotlight query inside scope, returning at most limit items
// (0 for no bound). It reads Spotlight's index only and never prompts: a
// folder macOS does not let Isoform see returns nothing.
func Query(text, scope string, limit int) ([]Item, error) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	cScope := C.CString(scope)
	defer C.free(unsafe.Pointer(cScope))
	var status C.int
	raw := C.yansu_spotlight_query(cText, cScope, C.int(limit), &status)
	if raw == nil {
		return nil, fmt.Errorf("%w (code %d)", errQueryFailed, int(status))
	}
	defer C.free(unsafe.Pointer(raw))
	return ParseItems([]byte(C.GoString(raw)))
}
