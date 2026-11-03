//go:build !darwin && !windows

package main

// anotherProcessUsingMicInput returns false on platforms without a known
// "is any other process actively using the microphone" API. Darwin uses
// CoreAudio's process-object enumeration; Windows uses WASAPI session
// enumeration; other platforms are no-op (the bundle-ID denylist still works).
func anotherProcessUsingMicInput() bool {
	return false
}

// otherMicInputApps reports that nobody can tell who reads the microphone
// on platforms without a process-level audio API.
func otherMicInputApps() micInputSnapshot {
	return micInputSnapshot{Unknown: true}
}
