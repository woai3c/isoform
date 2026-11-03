//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreAudio
#include <CoreAudio/AudioHardware.h>
#include <stdlib.h>
#include <unistd.h>

// Query output process activity without keeping an audio device or tap alive.
// A process with an open silent stream counts as active: resuming unnecessarily
// is preferable to missing its next speech. -1 means the wake signal is unknown.
static int BackgroundSystemAudioOutputActive(void) {
    if (@available(macOS 14.2, *)) {
        AudioObjectPropertyAddress listAddr = {
            kAudioHardwarePropertyProcessObjectList,
            kAudioObjectPropertyScopeGlobal,
            kAudioObjectPropertyElementMain
        };
        UInt32 size = 0;
        if (AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &listAddr, 0, NULL, &size) != noErr) return -1;
        if (size == 0) return 0;
        AudioObjectID *processes = (AudioObjectID *)malloc(size);
        if (!processes) return -1;
        if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &listAddr, 0, NULL, &size, processes) != noErr) {
            free(processes);
            return -1;
        }
        int result = 0;
        for (UInt32 i = 0; i < size / sizeof(AudioObjectID); i++) {
            AudioObjectPropertyAddress pidAddr = {
                kAudioProcessPropertyPID,
                kAudioObjectPropertyScopeGlobal,
                kAudioObjectPropertyElementMain
            };
            pid_t pid = 0;
            UInt32 pidSize = sizeof(pid);
            if (AudioObjectGetPropertyData(processes[i], &pidAddr, 0, NULL, &pidSize, &pid) != noErr) {
                result = -1;
                continue;
            }
            if (pid == getpid()) continue;
            AudioObjectPropertyAddress activeAddr = {
                kAudioProcessPropertyIsRunningOutput,
                kAudioObjectPropertyScopeGlobal,
                kAudioObjectPropertyElementMain
            };
            UInt32 active = 0;
            UInt32 activeSize = sizeof(active);
            if (AudioObjectGetPropertyData(processes[i], &activeAddr, 0, NULL, &activeSize, &active) != noErr) {
                result = -1;
                continue;
            }
            if (active) {
                result = 1;
                break;
            }
        }
        free(processes);
        return result;
    }
    return -1;
}
*/
import "C"

func platformSystemAudioWakeCheck() func() (active, known bool) {
	if !darwinSystemAudioCaptureSupported() {
		return nil
	}
	return func() (bool, bool) {
		state := int(C.BackgroundSystemAudioOutputActive())
		return state > 0, state >= 0
	}
}
