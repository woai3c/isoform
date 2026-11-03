//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreServices -framework AppKit
#include <CoreServices/CoreServices.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
#import <AppKit/AppKit.h>

// mail_automation_status asks macOS whether this app may send Apple events to
// Mail. noErr when allowed, errAEEventNotPermitted (-1743) when denied,
// errAEEventWouldRequireUserConsent (-1744) when undecided and askUser is
// false, procNotFound (-600) when Mail is not running. With askUser true,
// macOS may show the consent prompt and block until it is answered.
static int mail_automation_status(const char *bundleID, bool askUser) {
    AEAddressDesc target;
    OSStatus status = AECreateDesc(typeApplicationBundleID, bundleID, strlen(bundleID), &target);
    if (status != noErr) {
        return (int)status;
    }
    status = AEDeterminePermissionToAutomateTarget(&target, typeWildCard, typeWildCard, askUser);
    AEDisposeDesc(&target);
    return (int)status;
}

static bool mail_app_running(const char *bundleID) {
    NSString *identifier = [NSString stringWithUTF8String:bundleID];
    return [NSRunningApplication runningApplicationsWithBundleIdentifier:identifier].count > 0;
}

// mail_app_pid is the process id of the running Mail, or 0.
static int mail_app_pid(const char *bundleID) {
    NSString *identifier = [NSString stringWithUTF8String:bundleID];
    for (NSRunningApplication *app in [NSRunningApplication runningApplicationsWithBundleIdentifier:identifier]) {
        if (!app.terminated && app.processIdentifier > 0) {
            return (int)app.processIdentifier;
        }
    }
    return 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"wails-gui/internal/platform/mailheaders"
	"wails-gui/internal/platform/nativeprompt"
)

const (
	mailOsascriptPath = "/usr/bin/osascript"
	// mailScriptTimeout bounds one read; a week of a busy inbox answers in
	// a few seconds.
	mailScriptTimeout = 60 * time.Second
)

// systemMailSupported reports whether this platform has Mail.app.
func systemMailSupported() bool { return true }

// mailAutomationStatus asks macOS, never showing a prompt unless askUser is
// set, whether Isoform may automate Mail.
func mailAutomationStatus(askUser bool) mailAutomationState {
	if askUser && !permissionPromptsAllowed() {
		return mailAutomationUndecided
	}
	// Status-only reads from an unbundled helper must fail closed without
	// entering the Apple Events consent service. Explicit requests stay live.
	if !askUser && !appleEventsHasApplicationIdentity() {
		return mailAutomationUnknown
	}
	bundleID := C.CString(mailheaders.BundleID)
	defer C.free(unsafe.Pointer(bundleID))
	switch int(C.mail_automation_status(bundleID, C.bool(askUser))) {
	case 0:
		return mailAutomationAllowed
	case -1743:
		return mailAutomationDenied
	case -1744:
		return mailAutomationUndecided
	default:
		// procNotFound (-600): Mail is not running, and macOS answers only
		// for a running target.
		return mailAutomationUnknown
	}
}

// mailRunning reports whether Mail is running. It never starts it.
func mailRunning() bool {
	bundleID := C.CString(mailheaders.BundleID)
	defer C.free(unsafe.Pointer(bundleID))
	return bool(C.mail_app_running(bundleID))
}

// mailProcessID is the running Mail's process id, 0 when Mail is not
// running. The header script addresses Mail by it, so a Mail that quits
// during a read is not started again.
func mailProcessID() int {
	bundleID := C.CString(mailheaders.BundleID)
	defer C.free(unsafe.Pointer(bundleID))
	return int(C.mail_app_pid(bundleID))
}

// requestMailAutomation shows macOS's consent prompt for Mail. Mail must
// already be running: Isoform never opens it, so with Mail closed nothing
// is asked and the answer is false.
func requestMailAutomation() bool {
	if !mailRunning() {
		return false
	}
	return mailAutomationStatus(true) == mailAutomationAllowed
}

// runMailScript runs the header script with osascript as JavaScript for
// Automation and returns what it printed.
func runMailScript(ctx context.Context, script string) ([]byte, error) {
	if !nativeprompt.Allowed() && mailOsascriptPath == "/usr/bin/osascript" {
		return nil, errMailNotAllowed
	}
	runCtx, cancel := context.WithTimeout(ctx, mailScriptTimeout)
	defer cancel()
	out, err := exec.CommandContext(runCtx, mailOsascriptPath, "-l", "JavaScript", "-e", script).Output()
	if err == nil {
		return out, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, errMailSlow
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && mailAutomationRefused(string(exitErr.Stderr)) {
		return nil, errMailNotAllowed
	}
	if errors.As(err, &exitErr) && mailQuit(string(exitErr.Stderr)) {
		return nil, errMailClosed
	}
	// The error text may quote a subject; only its code is kept.
	return nil, errors.New("Mail could not be read")
}

// mailAutomationRefused matches osascript's text for a refused (-1743) or
// undecided (-1744) Apple event.
func mailAutomationRefused(output string) bool {
	return strings.Contains(output, "-1743") || strings.Contains(output, "-1744") ||
		strings.Contains(output, "Not authorized to send Apple events") ||
		strings.Contains(output, "not allowed to send Apple events")
}

// mailQuit matches osascript's text for an Apple event to a process that is
// gone (procNotFound, -600): Mail quit during the read.
func mailQuit(output string) bool {
	return strings.Contains(output, "(-600)") || strings.Contains(output, "isn\u2019t running") ||
		strings.Contains(output, "isn't running")
}
