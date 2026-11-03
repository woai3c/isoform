//go:build darwin

package notify

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

static bool isAppActive(void) {
    return [[NSApplication sharedApplication] isActive];
}
*/
import "C"

// IsAppForeground returns true if the app window is the active (foreground)
// application on macOS, using NSApplication.isActive via CGo.
func IsAppForeground() bool {
	return bool(C.isAppActive())
}
