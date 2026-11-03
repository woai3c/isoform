//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation

#import <AppKit/AppKit.h>
#import <stdlib.h>

// defaultAppForFile returns the .app path of the user's default handler for the
// file at path, or NULL if none. Uses NSWorkspace (macOS 12+) so it reflects
// the same default the Finder "Open" action would use.
static char *defaultAppForFile(const char *path) {
    if (path == NULL) return NULL;
    @autoreleasepool {
        NSString *p = [NSString stringWithUTF8String:path];
        if (p == nil) return NULL;
        NSURL *fileURL = [NSURL fileURLWithPath:p];
        NSURL *appURL = [[NSWorkspace sharedWorkspace] URLForApplicationToOpenURL:fileURL];
        if (appURL == nil) return NULL;
        const char *s = [[appURL path] UTF8String];
        if (s == NULL) return NULL;
        return strdup(s);
    }
}

// allAppsForFile returns a newline-separated list of .app paths that can open
// the file at path. Order follows LaunchServices' ranking. Uses NSWorkspace
// (macOS 12+). Returns NULL if none resolve.
static char *allAppsForFile(const char *path) {
    if (path == NULL) return NULL;
    @autoreleasepool {
        NSString *p = [NSString stringWithUTF8String:path];
        if (p == nil) return NULL;
        NSURL *fileURL = [NSURL fileURLWithPath:p];
        NSArray<NSURL *> *urls = [[NSWorkspace sharedWorkspace] URLsForApplicationsToOpenURL:fileURL];
        if (urls == nil || [urls count] == 0) return NULL;
        NSMutableArray<NSString *> *paths = [NSMutableArray array];
        for (NSURL *u in urls) {
            NSString *ap = [u path];
            if (ap != nil) [paths addObject:ap];
        }
        NSString *joined = [paths componentsJoinedByString:@"\n"];
        const char *s = [joined UTF8String];
        if (s == NULL) return NULL;
        return strdup(s);
    }
}
*/
import "C"

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"
)

// listOpenWithApps enumerates the GUI applications that can open absPath,
// resolving each app's display name and macOS icon. The user's default
// handler is marked IsDefault and floated to the front. Duplicate app paths
// are collapsed (LaunchServices can list the same bundle more than once).
func listOpenWithApps(absPath string) []DeliverableOpenWithApp {
	cPath := C.CString(absPath)
	defer C.free(unsafe.Pointer(cPath))

	var defaultPath string
	if cDef := C.defaultAppForFile(cPath); cDef != nil {
		defaultPath = C.GoString(cDef)
		C.free(unsafe.Pointer(cDef))
	}

	cAll := C.allAppsForFile(cPath)
	if cAll == nil {
		return nil
	}
	all := C.GoString(cAll)
	C.free(unsafe.Pointer(cAll))

	seen := make(map[string]bool)
	apps := make([]DeliverableOpenWithApp, 0, 8)
	for _, appPath := range strings.Split(all, "\n") {
		appPath = strings.TrimSpace(appPath)
		if appPath == "" || seen[appPath] {
			continue
		}
		seen[appPath] = true
		apps = append(apps, DeliverableOpenWithApp{
			Name:      strings.TrimSuffix(filepath.Base(appPath), ".app"),
			Path:      appPath,
			Icon:      nativeAppIconBase64(appPath),
			IsDefault: appPath == defaultPath,
		})
	}

	// Float the default handler to the front so the menu mirrors what the
	// primary "Open" button does.
	for i, app := range apps {
		if app.IsDefault && i != 0 {
			apps = append(apps[:i], apps[i+1:]...)
			apps = append([]DeliverableOpenWithApp{app}, apps...)
			break
		}
	}
	return apps
}

// revealInFileManager opens the Finder with absPath selected.
func revealInFileManager(absPath string) error {
	if err := exec.Command("open", "-R", absPath).Start(); err != nil {
		return fmt.Errorf("reveal in Finder: %w", err)
	}
	return nil
}

// openWithApp launches the application bundle at appPath to open absPath.
func openWithApp(absPath, appPath string) error {
	if appPath == "" {
		return fmt.Errorf("app path is required")
	}
	if err := exec.Command("open", "-a", appPath, absPath).Start(); err != nil {
		return fmt.Errorf("open with %s: %w", filepath.Base(appPath), err)
	}
	return nil
}
