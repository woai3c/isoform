//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation

#import <Foundation/Foundation.h>

// The user's locale with its System Settings overrides: a 12-hour clock
// keeps its period symbol ("a") in the hour template.
static int isoformClockUses24Hour(void) {
	@autoreleasepool {
		NSString *format = [NSDateFormatter dateFormatFromTemplate:@"j" options:0 locale:[NSLocale autoupdatingCurrentLocale]];
		return format != nil && [format rangeOfString:@"a"].location == NSNotFound ? 1 : 0;
	}
}
*/
import "C"

func systemUses24HourClock() bool { return C.isoformClockUses24Hour() == 1 }
