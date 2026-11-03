//go:build windows

package main

import (
	"log"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// AudioSessionStateActive is the session-state value WASAPI returns when a
// session is currently producing audio. Mirrors the constant from go-wca's
// constants.go; duplicated here so this file isn't tied to a specific
// constants ordering in the upstream package.
const audioSessionStateActive = 1 // wca.AudioSessionStateActive

// anotherProcessUsingMicInput returns true when at least one process OTHER
// than us is currently in an active audio session on the default capture
// endpoint (microphone). Used as a "is the user dictating into another app
// right now" signal by the background capture loop — see captureLoop in
// background_audio.go.
//
// Implementation: enumerate IAudioSessionManager2's session list on the
// default capture device, filter to AudioSessionStateActive, drop our own
// PID and PID=0 (system sounds). Anything left = other process actively
// reading the mic.
//
// COM lifecycle: each call locks an OS thread, CoInitializeEx in MTA mode,
// does the enumeration, and CoUninitializes. Called once per chunk (~5 s
// cadence) so the per-call overhead (a few hundred microseconds) is
// negligible compared to the audio chunk duration. Crucially we do NOT
// share COM state with the WASAPI capture session — that lives on its own
// locked thread (see background_audio_wasapi_windows.go).
//
// Failure modes are conservative: any error returns false so we never
// over-classify normal speech as ambient. The function logs at most once
// per error category (logged via package-level atomic counters) to avoid
// spamming the log every 5 s when something is genuinely broken.
func anotherProcessUsingMicInput() bool {
	active, _ := micInputSessionActive()
	return active
}

// micInputSessionActive reports whether another process holds the
// microphone, and whether the sessions could be read at all.
func micInputSessionActive() (active, ok bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		logConcurrentMicCheckOnce("CoInitializeEx", err)
		return false, false
	}
	defer ole.CoUninitialize()

	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		logConcurrentMicCheckOnce("CoCreateInstance(MMDeviceEnumerator)", err)
		return false, false
	}
	defer enumerator.Release()

	var device *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &device); err != nil {
		logConcurrentMicCheckOnce("GetDefaultAudioEndpoint(capture)", err)
		return false, false
	}
	defer device.Release()

	var sessionManager *wca.IAudioSessionManager2
	if err := device.Activate(wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &sessionManager); err != nil {
		logConcurrentMicCheckOnce("Activate(IAudioSessionManager2)", err)
		return false, false
	}
	defer sessionManager.Release()

	var enumSessions *wca.IAudioSessionEnumerator
	if err := sessionManager.GetSessionEnumerator(&enumSessions); err != nil {
		logConcurrentMicCheckOnce("GetSessionEnumerator", err)
		return false, false
	}
	defer enumSessions.Release()

	var count int
	if err := enumSessions.GetCount(&count); err != nil {
		logConcurrentMicCheckOnce("GetCount", err)
		return false, false
	}

	ourPID := uint32(os.Getpid())
	for i := 0; i < count; i++ {
		if sessionActiveBelongsToAnotherProcess(enumSessions, i, ourPID) {
			return true, true
		}
	}
	return false, true
}

// otherMicInputApps reports whether another process holds the microphone.
// WASAPI sessions name a PID, not a bundle, so BundleIDs stays empty and the
// meeting-app-on-microphone call signal is macOS-only for now.
func otherMicInputApps() micInputSnapshot {
	active, ok := micInputSessionActive()
	return micInputSnapshot{Active: active, Unknown: !ok}
}

// sessionActiveBelongsToAnotherProcess inspects one session in the enumerator
// and returns true when:
//   - the session is in AudioSessionStateActive (producing audio), AND
//   - the session's owning PID is non-zero (i.e. not the Windows system
//     sounds session, which has PID 0), AND
//   - the PID is not our own process.
//
// Any error during inspection returns false (conservative — never false-
// positive into ambient classification).
func sessionActiveBelongsToAnotherProcess(enumSessions *wca.IAudioSessionEnumerator, index int, ourPID uint32) bool {
	var session *wca.IAudioSessionControl
	if err := enumSessions.GetSession(index, &session); err != nil {
		return false
	}
	defer session.Release()

	var state uint32
	if err := session.GetState(&state); err != nil {
		return false
	}
	if state != audioSessionStateActive {
		return false
	}

	// QueryInterface to IAudioSessionControl2 so we can read the PID.
	// IAudioSessionControl2 inherits from IAudioSessionControl, so this
	// is always available on Windows 7+ (which is below our minimum).
	dispatch, err := session.QueryInterface(wca.IID_IAudioSessionControl2)
	if err != nil {
		return false
	}
	defer dispatch.Release()
	session2 := (*wca.IAudioSessionControl2)(unsafePointerOfDispatch(dispatch))

	var pid uint32
	if err := session2.GetProcessId(&pid); err != nil {
		return false
	}
	if pid == 0 || pid == ourPID {
		return false
	}
	return true
}

// unsafePointerOfDispatch reinterprets an *ole.IDispatch as the unsafe
// pointer needed to construct a *wca.IAudioSessionControl2. Necessary
// because go-ole's QueryInterface returns an *IDispatch but the WCA Go
// types have their own struct layout that inherits from ole.IUnknown.
// The two layouts have an identical first-pointer slot (the vtbl), so
// this cast is safe.
//
//go:nosplit
func unsafePointerOfDispatch(d *ole.IDispatch) unsafe.Pointer {
	return unsafe.Pointer(d)
}

// concurrentMicCheckLoggedErrors deduplicates per-category error logging
// so a permanently-broken WASAPI path doesn't spam the log every 5 s. Each
// category logs at most once per process lifetime.
var concurrentMicCheckLoggedErrors sync.Map

func logConcurrentMicCheckOnce(stage string, err error) {
	if _, loaded := concurrentMicCheckLoggedErrors.LoadOrStore(stage, struct{}{}); loaded {
		return
	}
	log.Printf("[concurrent-mic-check] %s failed (further failures suppressed): %v", stage, err)
}
