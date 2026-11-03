//go:build darwin

package main

import "runtime"

func init() {
	// This command has no Cocoa run loop to service queued native reads.
	// Pin main to the startup thread so workspace and browser helpers take
	// their direct main-thread path, including after --delay sleeps.
	runtime.LockOSThread()
}
