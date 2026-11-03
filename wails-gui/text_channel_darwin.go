//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreServices -framework AppKit
#include <CoreServices/CoreServices.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
#import <AppKit/AppKit.h>

// messages_automation_status asks macOS whether this app may send Apple events
// to Messages. It returns noErr when allowed, errAEEventNotPermitted (-1743)
// when denied, errAEEventWouldRequireUserConsent (-1744) when undecided and
// askUser is false, and procNotFound (-600) when Messages is not running. With
// askUser true, macOS may show the consent prompt and block until it is answered.
static int messages_automation_status(const char *bundleID, bool askUser) {
    AEAddressDesc target;
    OSStatus status = AECreateDesc(typeApplicationBundleID, bundleID, strlen(bundleID), &target);
    if (status != noErr) {
        return (int)status;
    }
    status = AEDeterminePermissionToAutomateTarget(&target, typeWildCard, typeWildCard, askUser);
    AEDisposeDesc(&target);
    return (int)status;
}

static bool messages_app_running(const char *bundleID) {
    NSString *identifier = [NSString stringWithUTF8String:bundleID];
    return [NSRunningApplication runningApplicationsWithBundleIdentifier:identifier].count > 0;
}
*/
import "C"

import (
	"log"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"wails-gui/internal/platform/nativeprompt"
)

func defaultTextSender() textSender { return messagesSender{} }

// A stored grant may outlive a TCC reset while Messages is closed. Start the
// target without an Apple event, then check without consent before any send,
// so a proactive delivery cannot ask for Automation access.
func prepareMessagesSend() error {
	launchMessagesHidden()
	if textMessagesAutomation(false) != textAutomationAllowed {
		return errTextMessagesNotAllowed
	}
	return nil
}

// textSenderSupported reports whether osascript is present. Messages itself
// is checked by the permission and by the send.
func textSenderSupported() bool {
	_, err := os.Stat(messagesOsascriptPath)
	return err == nil
}

var passiveMessagesPermission passiveTextAutomationProbe

// textMessagesAutomation asks macOS whether Yansu may send to Messages. With
// askUser, Messages is launched hidden first because macOS answers only for a
// running target, and the consent prompt may appear.
func textMessagesAutomation(askUser bool) textAutomationState {
	// Status-only reads from an unbundled helper must fail closed without
	// entering the Apple Events consent service. Explicit requests stay live.
	if !askUser && !appleEventsHasApplicationIdentity() {
		return textAutomationUnknown
	}
	if askUser {
		if !permissionPromptsAllowed() {
			return textAutomationNotAllowed
		}
		launchMessagesHidden()
		return readMessagesAutomationStatus(true)
	}
	// Even askUser=false can wedge inside Apple's IPC. Bound passive display
	// reads and coalesce a stalled call. Explicit prompts remain synchronous.
	return passiveMessagesPermission.read(500*time.Millisecond, func() textAutomationState {
		return readMessagesAutomationStatus(false)
	})
}

func readMessagesAutomationStatus(askUser bool) textAutomationState {
	if askUser && !nativeprompt.Allowed() {
		return textAutomationNotAllowed
	}
	bundleID := C.CString(messagesBundleID)
	defer C.free(unsafe.Pointer(bundleID))
	switch int(C.messages_automation_status(bundleID, C.bool(askUser))) {
	case 0:
		return textAutomationAllowed
	case -1743, -1744:
		return textAutomationNotAllowed
	default:
		// procNotFound (-600) means Messages is not running. Other codes are
		// also unknown. The caller keeps the last recorded answer.
		return textAutomationUnknown
	}
}

func messagesRunning() bool {
	bundleID := C.CString(messagesBundleID)
	defer C.free(unsafe.Pointer(bundleID))
	return bool(C.messages_app_running(bundleID))
}

// launchMessagesHidden opens Messages without activating it, so it can answer
// the permission question. It waits up to five seconds for the app to start.
func launchMessagesHidden() {
	if messagesRunning() {
		return
	}
	if err := exec.Command(messagesOpenPath, "-g", "-j", "-b", messagesBundleID).Run(); err != nil {
		log.Printf("[text-channel] could not open Messages in the background: %v", err)
		return
	}
	deadline := time.Now().Add(messagesLaunchWait)
	for !messagesRunning() && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
}
