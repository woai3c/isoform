//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreAudio -framework Foundation -framework AppKit
#include <stdlib.h>
#include <stdbool.h>
#include <string.h>
#include <unistd.h>

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <CoreAudio/AudioHardware.h>

// _concurrent_mic_get_process_list returns the system-wide list of audio
// process objects (macOS 14+). The caller is responsible for free()ing the
// returned buffer when non-NULL. Returns NULL on any failure; *outCount is
// set to 0 in that case.
static AudioObjectID* _concurrent_mic_get_process_list(UInt32 *outCount) {
    if (outCount) *outCount = 0;

    AudioObjectPropertyAddress addr = {
        kAudioHardwarePropertyProcessObjectList,
        kAudioObjectPropertyScopeGlobal,
        kAudioObjectPropertyElementMain
    };

    UInt32 size = 0;
    OSStatus status = AudioObjectGetPropertyDataSize(
        kAudioObjectSystemObject, &addr, 0, NULL, &size
    );
    if (status != noErr || size == 0) {
        return NULL;
    }

    AudioObjectID *list = (AudioObjectID *)malloc(size);
    if (!list) return NULL;

    status = AudioObjectGetPropertyData(
        kAudioObjectSystemObject, &addr, 0, NULL, &size, list
    );
    if (status != noErr) {
        free(list);
        return NULL;
    }

    if (outCount) *outCount = size / sizeof(AudioObjectID);
    return list;
}

// _concurrent_mic_process_is_input_active reports whether the given audio
// process object is currently doing audio input. Returns false on any
// failure (treated as "not active" — fail-safe).
static bool _concurrent_mic_process_is_input_active(AudioObjectID proc) {
    AudioObjectPropertyAddress addr = {
        kAudioProcessPropertyIsRunningInput,
        kAudioObjectPropertyScopeGlobal,
        kAudioObjectPropertyElementMain
    };
    UInt32 isRunning = 0;
    UInt32 size = sizeof(isRunning);
    OSStatus status = AudioObjectGetPropertyData(proc, &addr, 0, NULL, &size, &isRunning);
    if (status != noErr) return false;
    return isRunning != 0;
}

// _concurrent_mic_process_pid returns the PID of the given audio process
// object, or 0 on failure.
static pid_t _concurrent_mic_process_pid(AudioObjectID proc) {
    AudioObjectPropertyAddress addr = {
        kAudioProcessPropertyPID,
        kAudioObjectPropertyScopeGlobal,
        kAudioObjectPropertyElementMain
    };
    pid_t pid = 0;
    UInt32 size = sizeof(pid);
    OSStatus status = AudioObjectGetPropertyData(proc, &addr, 0, NULL, &size, &pid);
    if (status != noErr) return 0;
    return pid;
}

// _concurrent_mic_process_bundle_id copies the bundle ID of the given audio
// process object into out. It asks CoreAudio first and falls back to the
// running application with that PID (helpers may not report one). Returns
// false when neither knows the bundle ID.
static bool _concurrent_mic_process_bundle_id(AudioObjectID proc, pid_t pid, char *out, size_t outLen) {
    if (!out || outLen == 0) return false;
    out[0] = 0;

    AudioObjectPropertyAddress addr = {
        kAudioProcessPropertyBundleID,
        kAudioObjectPropertyScopeGlobal,
        kAudioObjectPropertyElementMain
    };
    CFStringRef bundle = NULL;
    UInt32 size = sizeof(bundle);
    OSStatus status = AudioObjectGetPropertyData(proc, &addr, 0, NULL, &size, &bundle);
    if (status == noErr && bundle != NULL) {
        bool ok = CFStringGetCString(bundle, out, (CFIndex)outLen, kCFStringEncodingUTF8);
        CFRelease(bundle);
        if (ok && out[0] != 0) return true;
        out[0] = 0;
    }

    if (pid <= 0) return false;
    @autoreleasepool {
        NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        NSString *bid = app.bundleIdentifier;
        if (bid == nil || bid.length == 0) return false;
        return [bid getCString:out maxLength:outLen encoding:NSUTF8StringEncoding] && out[0] != 0;
    }
}

// OtherMicInputProcesses reports the other processes (not this one) that are
// actively reading from the audio input device. It returns how many there
// are and writes their bundle IDs, newline-separated, into out (a process
// whose bundle ID is unknown still counts). Uses the macOS 14+ process-object
// API; it returns -1 when that list cannot be read (macOS before 14).
static int OtherMicInputProcesses(char *out, size_t outLen) {
    if (out && outLen > 0) out[0] = 0;
    pid_t selfPID = getpid();

    UInt32 count = 0;
    AudioObjectID *list = _concurrent_mic_get_process_list(&count);
    if (!list || count == 0) {
        if (list) free(list);
        return -1;
    }

    int found = 0;
    size_t used = 0;
    for (UInt32 i = 0; i < count; i++) {
        if (!_concurrent_mic_process_is_input_active(list[i])) continue;
        pid_t pid = _concurrent_mic_process_pid(list[i]);
        if (pid <= 0 || pid == selfPID) continue;
        found++;

        char bundle[256];
        if (!out || !_concurrent_mic_process_bundle_id(list[i], pid, bundle, sizeof(bundle))) continue;
        size_t n = strlen(bundle);
        if (used + n + 2 > outLen) continue;
        memcpy(out + used, bundle, n);
        used += n;
        out[used++] = '\n';
        out[used] = 0;
    }

    free(list);
    return found;
}
*/
import "C"

import (
	"bytes"
	"strings"
	"unsafe"
)

// otherMicInputApps snapshots the other processes on the system that are
// currently capturing audio input (the microphone). Used by the background
// capture loop as a "is the user dictating into another app right now"
// signal (see captureLoop in background_audio.go) and, through the bundle
// IDs, as the "a meeting app has the microphone open" call signal.
//
// Macros to be aware of:
//   - On macOS < 14 the underlying API is unavailable; the snapshot is then
//     Unknown and reports nobody active (PR 1a's denylist still works).
//   - Isoform's own PID is excluded — we know we're using the mic, that's not
//     the signal we're looking for.
//   - This is cheap (per-process property lookups, no enumeration of audio
//     samples) and safe to call at the per-chunk rate (~5s).
func otherMicInputApps() micInputSnapshot {
	buf := make([]byte, micInputBundleBufferSize)
	n := int(C.OtherMicInputProcesses((*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf))))
	if n < 0 {
		return micInputSnapshot{Unknown: true}
	}
	if n == 0 {
		return micInputSnapshot{}
	}
	end := bytes.IndexByte(buf, 0)
	if end < 0 {
		end = len(buf)
	}
	return micInputSnapshot{Active: true, BundleIDs: strings.Fields(string(buf[:end]))}
}

// anotherProcessUsingMicInput returns true when at least one other process on
// the system is currently capturing audio input (the microphone).
func anotherProcessUsingMicInput() bool {
	return otherMicInputApps().Active
}
