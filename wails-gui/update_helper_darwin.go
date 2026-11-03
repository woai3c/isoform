package main

import (
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func detachUpdateHelper(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func updateProcessAlive(pid int) bool  { return syscall.Kill(pid, 0) == nil }

// waitUpdateProcessExit returns true once pid has exited, woken by the kernel
// (kqueue NOTE_EXIT) rather than a polling interval. It falls back to a short
// poll if the process cannot be watched.
func waitUpdateProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	if kq, err := unix.Kqueue(); err == nil {
		defer unix.Close(kq)
		var change unix.Kevent_t
		unix.SetKevent(&change, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
		change.Fflags = unix.NOTE_EXIT
		events := make([]unix.Kevent_t, 1)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return !updateProcessAlive(pid)
			}
			ts := unix.NsecToTimespec(remaining.Nanoseconds())
			n, err := unix.Kevent(kq, []unix.Kevent_t{change}, events, &ts)
			if err == unix.EINTR {
				continue
			}
			if err != nil {
				break
			}
			// An exit event, or an error event (ESRCH: already gone).
			if n > 0 {
				return waitUpdateProcessGone(pid, deadline)
			}
			return !updateProcessAlive(pid)
		}
	}
	return waitUpdateProcessGone(pid, deadline)
}

// waitUpdateProcessGone polls until launchd has reaped pid.
func waitUpdateProcessGone(pid int, deadline time.Time) bool {
	for updateProcessAlive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}
