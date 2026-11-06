//go:build !darwin

package activitymon

import "time"

// IdleTime is unknown outside macOS; zero means "treat the user as present".
func IdleTime() time.Duration { return 0 }

// MicrophoneInUse is unknown outside macOS.
func MicrophoneInUse() bool { return false }

// CameraInUse is unknown outside macOS.
func CameraInUse() bool { return false }

// PowerSource is unknown outside macOS.
func PowerSource() PowerState { return PowerState{Percent: -1} }

// SystemStateEventsSupported is false: nothing announces lock or app changes
// here, so screen-state consumers must poll.
const SystemStateEventsSupported = false

// StartSystemStateObservers has nothing to observe outside macOS.
func StartSystemStateObservers() {}
