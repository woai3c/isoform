//go:build darwin

package activitymon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics -framework CoreAudio -framework CoreMediaIO -framework IOKit -framework AppKit -framework Foundation
#include <stdbool.h>
#include <stdlib.h>
#include <CoreGraphics/CoreGraphics.h>
#include <CoreAudio/CoreAudio.h>
#include <CoreMediaIO/CMIOHardware.h>
#include <IOKit/ps/IOPowerSources.h>
#include <IOKit/ps/IOPSKeys.h>
#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

// Defined in sysstate_export_darwin.go.
extern void goSystemStateChanged(const char *kind);

// Seconds since the last keyboard, mouse or trackpad event in the login
// session. Needs no permission.
static double idle_seconds(void) {
	return CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateCombinedSessionState, kCGAnyInputEventType);
}

// True when any device with input streams reports
// kAudioDevicePropertyDeviceIsRunningSomewhere: some process, this one
// included, has the microphone open.
static bool any_input_device_running(void) {
	AudioObjectPropertyAddress devices = {
		kAudioHardwarePropertyDevices, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain
	};
	UInt32 size = 0;
	if (AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &devices, 0, NULL, &size) != noErr || size == 0) {
		return false;
	}
	AudioDeviceID *ids = malloc(size);
	if (ids == NULL) {
		return false;
	}
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &devices, 0, NULL, &size, ids) != noErr) {
		free(ids);
		return false;
	}
	UInt32 count = size / sizeof(AudioDeviceID);
	bool running = false;
	for (UInt32 i = 0; i < count && !running; i++) {
		AudioObjectPropertyAddress streams = {
			kAudioDevicePropertyStreams, kAudioObjectPropertyScopeInput, kAudioObjectPropertyElementMain
		};
		UInt32 streamSize = 0;
		if (AudioObjectGetPropertyDataSize(ids[i], &streams, 0, NULL, &streamSize) != noErr || streamSize == 0) {
			continue;
		}
		AudioObjectPropertyAddress somewhere = {
			kAudioDevicePropertyDeviceIsRunningSomewhere, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain
		};
		UInt32 value = 0;
		UInt32 valueSize = sizeof(value);
		if (AudioObjectGetPropertyData(ids[i], &somewhere, 0, NULL, &valueSize, &value) == noErr && value != 0) {
			running = true;
		}
	}
	free(ids);
	return running;
}

// True when any camera reports kCMIODevicePropertyDeviceIsRunningSomewhere.
static bool any_camera_running(void) {
	CMIOObjectPropertyAddress devices = {
		kCMIOHardwarePropertyDevices, kCMIOObjectPropertyScopeGlobal, kCMIOObjectPropertyElementMain
	};
	UInt32 size = 0;
	if (CMIOObjectGetPropertyDataSize(kCMIOObjectSystemObject, &devices, 0, NULL, &size) != noErr || size == 0) {
		return false;
	}
	CMIODeviceID *ids = malloc(size);
	if (ids == NULL) {
		return false;
	}
	UInt32 used = 0;
	if (CMIOObjectGetPropertyData(kCMIOObjectSystemObject, &devices, 0, NULL, size, &used, ids) != noErr) {
		free(ids);
		return false;
	}
	UInt32 count = used / sizeof(CMIODeviceID);
	bool running = false;
	for (UInt32 i = 0; i < count && !running; i++) {
		CMIOObjectPropertyAddress somewhere = {
			kCMIODevicePropertyDeviceIsRunningSomewhere, kCMIOObjectPropertyScopeGlobal, kCMIOObjectPropertyElementMain
		};
		UInt32 value = 0;
		UInt32 valueUsed = 0;
		if (CMIOObjectGetPropertyData(ids[i], &somewhere, 0, NULL, sizeof(value), &valueUsed, &value) == noErr && value != 0) {
			running = true;
		}
	}
	free(ids);
	return running;
}

// Power source: returns 1 on battery, 0 on AC, -1 when unknown. *percent
// gets the battery level or -1.
static int power_state(int *percent) {
	*percent = -1;
	CFTypeRef blob = IOPSCopyPowerSourcesInfo();
	if (blob == NULL) {
		return -1;
	}
	int onBattery = -1;
	CFStringRef type = IOPSGetProvidingPowerSourceType(blob);
	if (type != NULL) {
		onBattery = CFEqual(type, CFSTR(kIOPMBatteryPowerKey)) ? 1 : 0;
	}
	CFArrayRef list = IOPSCopyPowerSourcesList(blob);
	if (list != NULL) {
		CFIndex n = CFArrayGetCount(list);
		for (CFIndex i = 0; i < n; i++) {
			CFDictionaryRef desc = IOPSGetPowerSourceDescription(blob, CFArrayGetValueAtIndex(list, i));
			if (desc == NULL) {
				continue;
			}
			CFNumberRef cap = CFDictionaryGetValue(desc, CFSTR(kIOPSCurrentCapacityKey));
			int value = -1;
			if (cap != NULL && CFNumberGetValue(cap, kCFNumberIntType, &value)) {
				*percent = value;
				break;
			}
		}
		CFRelease(list);
	}
	CFRelease(blob);
	return onBattery;
}

// Installs the lock, unlock, sleep and wake observers once. Each fires the Go
// callback, which only queues the change; nothing heavy runs on the posting
// thread.
static void install_system_state_observers(void) {
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		NSDistributedNotificationCenter *dnc = [NSDistributedNotificationCenter defaultCenter];
		[dnc addObserverForName:@"com.apple.screenIsLocked" object:nil queue:nil usingBlock:^(NSNotification *note) {
			goSystemStateChanged("locked");
		}];
		[dnc addObserverForName:@"com.apple.screenIsUnlocked" object:nil queue:nil usingBlock:^(NSNotification *note) {
			goSystemStateChanged("unlocked");
		}];
		NSNotificationCenter *center = [[NSWorkspace sharedWorkspace] notificationCenter];
		[center addObserverForName:NSWorkspaceWillSleepNotification object:nil queue:nil usingBlock:^(NSNotification *note) {
			goSystemStateChanged("sleep");
		}];
		[center addObserverForName:NSWorkspaceScreensDidSleepNotification object:nil queue:nil usingBlock:^(NSNotification *note) {
			goSystemStateChanged("screens-sleep");
		}];
		[center addObserverForName:NSWorkspaceWillPowerOffNotification object:nil queue:nil usingBlock:^(NSNotification *note) {
			goSystemStateChanged("poweroff");
		}];
		[center addObserverForName:NSWorkspaceDidActivateApplicationNotification object:nil queue:nil usingBlock:^(NSNotification *note) {
			goSystemStateChanged("app-activated");
		}];
		[center addObserverForName:NSWorkspaceDidWakeNotification object:nil queue:nil usingBlock:^(NSNotification *note) {
			goSystemStateChanged("wake");
		}];
	});
}
*/
import "C"

import "time"

// IdleTime is how long the user has been away from the keyboard and mouse.
func IdleTime() time.Duration {
	seconds := float64(C.idle_seconds())
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// MicrophoneInUse reports whether any input device is open in some process,
// this one included (kAudioDevicePropertyDeviceIsRunningSomewhere).
func MicrophoneInUse() bool { return bool(C.any_input_device_running()) }

// CameraInUse reports whether any camera is open in some process
// (kCMIODevicePropertyDeviceIsRunningSomewhere).
func CameraInUse() bool { return bool(C.any_camera_running()) }

// PowerSource reads the power source and battery level.
func PowerSource() PowerState {
	var percent C.int
	switch C.power_state(&percent) {
	case 1:
		return PowerState{OnBattery: true, Percent: int(percent), Known: true}
	case 0:
		return PowerState{OnBattery: false, Percent: int(percent), Known: true}
	default:
		return PowerState{Percent: -1}
	}
}

// SystemStateEventsSupported is true here: the lock, sleep and frontmost-app
// changes arrive as notifications, so screen-state consumers need no poll.
const SystemStateEventsSupported = true

// StartSystemStateObservers installs the lock, unlock, sleep and wake
// observers once; changes arrive on SystemStateChanges.
func StartSystemStateObservers() { C.install_system_state_observers() }
