// Power assertion and event-driven power monitoring (mobile_keep_awake_darwin.go).
#include <pthread.h>
#import <Foundation/Foundation.h>
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/ps/IOPowerSources.h>
#include <IOKit/ps/IOPSKeys.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include "_cgo_export.h"

static IOPMAssertionID isoKeepAwakeAssertionID = kIOPMNullAssertionID;
static CFRunLoopSourceRef isoPowerSource = NULL;
static CFRunLoopRef isoPowerRunLoop = NULL;
static id isoLowPowerModeObserver = nil;

// Holds PreventUserIdleSystemSleep. Called only under the Go state machine's lock.
int isoKeepAwakeHold(void) {
	if (isoKeepAwakeAssertionID != kIOPMNullAssertionID) {
		return 1;
	}
	CFStringRef reason = CFSTR("Isoform keeps this computer reachable for your phone");
	IOPMAssertionID assertionID = kIOPMNullAssertionID;
	IOReturn result = IOPMAssertionCreateWithName(kIOPMAssertPreventUserIdleSystemSleep,
		kIOPMAssertionLevelOn, reason, &assertionID);
	if (result != kIOReturnSuccess) {
		return 0;
	}
	isoKeepAwakeAssertionID = assertionID;
	return 1;
}

void isoKeepAwakeRelease(void) {
	if (isoKeepAwakeAssertionID == kIOPMNullAssertionID) {
		return;
	}
	IOPMAssertionRelease(isoKeepAwakeAssertionID);
	isoKeepAwakeAssertionID = kIOPMNullAssertionID;
}

// Reads one snapshot: whether the computer is on AC power, whether it has a
// battery, Low Power Mode and the lid state.
void isoKeepAwakePowerState(int *onAC, int *hasBattery, int *lowPowerMode, int *lidClosed) {
	*onAC = 0;
	*hasBattery = 0;
	*lowPowerMode = 0;
	*lidClosed = 0;
	@autoreleasepool {
		if (@available(macOS 12.0, *)) {
			*lowPowerMode = NSProcessInfo.processInfo.isLowPowerModeEnabled ? 1 : 0;
		}
		CFTypeRef info = IOPSCopyPowerSourcesInfo();
		if (info != NULL) {
			CFStringRef source = IOPSGetProvidingPowerSourceType(info);
			*onAC = source != NULL && CFEqual(source, CFSTR(kIOPSACPowerValue));
			CFArrayRef sources = IOPSCopyPowerSourcesList(info);
			if (sources != NULL) {
				for (CFIndex i = 0; i < CFArrayGetCount(sources); i++) {
					CFDictionaryRef description = IOPSGetPowerSourceDescription(info, CFArrayGetValueAtIndex(sources, i));
					if (description == NULL) continue;
					CFTypeRef type = CFDictionaryGetValue(description, CFSTR(kIOPSTypeKey));
					if (type == NULL || !CFEqual(type, CFSTR(kIOPSInternalBatteryType))) continue;
					*hasBattery = 1;
					break;
				}
				CFRelease(sources);
			}
			CFRelease(info);
		}
		io_service_t rootDomain = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"));
		if (rootDomain != IO_OBJECT_NULL) {
			CFTypeRef state = IORegistryEntryCreateCFProperty(rootDomain, CFSTR("AppleClamshellState"), kCFAllocatorDefault, 0);
			if (state != NULL) {
				if (CFGetTypeID(state) == CFBooleanGetTypeID()) {
					*lidClosed = CFBooleanGetValue(state) ? 1 : 0;
				}
				CFRelease(state);
			}
			IOObjectRelease(rootDomain);
		}
	}
}

static void isoPowerSourceChanged(void *context) {
	(void)context;
	goKeepAwakePowerChanged();
}

static void isoLowPowerModeChanged(NSNotification *note) {
	(void)note;
	CFRunLoopRef runLoop = isoPowerRunLoop;
	if (runLoop == NULL) return;
	CFRunLoopPerformBlock(runLoop, kCFRunLoopDefaultMode, ^{
		goKeepAwakePowerChanged();
	});
	CFRunLoopWakeUp(runLoop);
}

static void *isoPowerSourceRunLoop(void *unused) {
	(void)unused;
	@autoreleasepool {
		isoPowerRunLoop = CFRunLoopGetCurrent();
		CFRunLoopAddSource(isoPowerRunLoop, isoPowerSource, kCFRunLoopDefaultMode);
		if (@available(macOS 12.0, *)) {
			isoLowPowerModeObserver = [[NSNotificationCenter defaultCenter]
				addObserverForName:NSProcessInfoPowerStateDidChangeNotification
				object:nil queue:nil usingBlock:^(NSNotification *note) {
					isoLowPowerModeChanged(note);
				}];
		}
		CFRunLoopRun();
	}
	return NULL;
}

// Creates the power-source event source, then runs its run loop on a detached
// thread. Low Power Mode notifications are delivered back to the same run loop.
// Returns 0 when the source cannot be created.
int isoWatchPowerSource(void) {
	if (isoPowerSource != NULL) {
		return 1;
	}
	isoPowerSource = IOPSNotificationCreateRunLoopSource(isoPowerSourceChanged, NULL);
	if (isoPowerSource == NULL) {
		return 0;
	}
	pthread_t thread;
	if (pthread_create(&thread, NULL, isoPowerSourceRunLoop, NULL) != 0) {
		CFRelease(isoPowerSource);
		isoPowerSource = NULL;
		return 0;
	}
	pthread_detach(thread);
	return 1;
}
