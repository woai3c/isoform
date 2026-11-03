//go:build darwin

package main

import (
	"context"
	"os/exec"
	"runtime"
	"syscall"

	"wails-gui/internal/platform/cmdutil"
)

// From <sys/resource.h>: setpriority on the calling thread, background band
// (low CPU and I/O priority, the QoS background class).
const (
	prioDarwinThread = 3
	prioDarwinBG     = 0x1000
)

// withBackgroundPriority runs fn on one OS thread put in the background
// band, so storage's media work (shrinking screenshots, compressing calls)
// yields to the app and everything else on the Mac.
func withBackgroundPriority(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := syscall.Setpriority(prioDarwinThread, 0, prioDarwinBG); err == nil {
		defer syscall.Setpriority(prioDarwinThread, 0, 0)
	}
	fn()
}

// lowPriorityCommand starts name at the lowest scheduling priority (nice 19)
// when nice is available.
func lowPriorityCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if nice, err := exec.LookPath("nice"); err == nil {
		return cmdutil.CommandContext(ctx, nice, append([]string{"-n", "19", name}, args...)...)
	}
	return cmdutil.CommandContext(ctx, name, args...)
}
