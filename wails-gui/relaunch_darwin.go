//go:build darwin

package main

import (
	"log"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
)

// relaunchWaitScript waits for the quitting process ($1) to exit, checking
// every 0.2 s at most $2 times, then opens the bundle ($4) as a new instance
// with open(1) at $3, passing the remaining arguments (--env pairs) first.
// The arguments arrive as positional parameters, so no path or value is
// ever parsed by the shell. A process still running after the wait is left
// alone: a second copy would be turned away by the single-instance lock.
// open's own exit status is the script's. There is no second try without
// the --env pairs: open can fail after the app is already up (a -1712
// timeout), and a development build without YANSU_APP_FLAVOR would run as
// Yansu on the user's real data folder.
const relaunchWaitScript = `pid="$1"; tries="$2"; opener="$3"; bundle="$4"; shift 4
i=0
while kill -0 "$pid" 2>/dev/null && [ "$i" -lt "$tries" ]; do sleep 0.2; i=$((i+1)); done
kill -0 "$pid" 2>/dev/null && exit 0
exec "$opener" -n "$@" "$bundle"`

// relaunchWaitTries is 30 seconds of 0.2 s checks.
const relaunchWaitTries = 150

// waitThenOpen opens bundle with opener once process pid has exited, in
// its own session so it outlives the caller (see startDetachedShell).
func waitThenOpen(pid, tries int, opener, bundle string, args []string) *exec.Cmd {
	cmdArgs := append([]string{"-c", relaunchWaitScript, "isoform-relaunch", strconv.Itoa(pid), strconv.Itoa(tries), opener, bundle}, args...)
	cmd := exec.Command("/bin/sh", cmdArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// relaunchEnvArgs turns KEY=VALUE entries into open(1)'s --env pairs. These
// preserve the resolved data home as well as development launch settings;
// current macOS provides open(1)'s --env option.
func relaunchEnvArgs(env []string) []string {
	args := []string{}
	for _, entry := range env {
		args = append(args, "--env", entry)
	}
	return args
}

// startRelaunchAfterExit opens bundle again once process pid (this one) has
// exited. gaveUp runs if the waiting shell ends while this process is still
// alive to see it: the quit stalled past the wait, so nothing was opened.
func startRelaunchAfterExit(pid int, bundle string, env []string, gaveUp func()) error {
	return startWaiter(waitThenOpen(pid, relaunchWaitTries, "/usr/bin/open", bundle, relaunchEnvArgs(env)), gaveUp)
}

// startWaiter starts cmd and runs whenDone once it exits.
func startWaiter(cmd *exec.Cmd, whenDone func()) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		err := cmd.Wait()
		log.Printf("[relaunch] the reopen ended while this process still runs (%v); nothing was opened", err)
		whenDone()
	}()
	return nil
}

var (
	screenRecordingLaunchRecorded atomic.Bool
	screenRecordingAtLaunch       atomic.Bool
)

// recordScreenRecordingAtLaunch remembers whether Screen Recording was on
// when this process started. Called once from startup.
func recordScreenRecordingAtLaunch() {
	screenRecordingAtLaunch.Store(preflightScreenCaptureAccess())
	screenRecordingLaunchRecorded.Store(true)
}

// screenRecordingNeedsRelaunch: off at launch, allowed since. A grant with
// macOS's "Later" counts too (fresh-install rehearsal, 2026-10-27: the row
// stayed on Turn on), via the live check in screenRecordingGrantedNow.
func screenRecordingNeedsRelaunch() bool {
	if !screenRecordingLaunchRecorded.Load() || screenRecordingAtLaunch.Load() {
		return false
	}
	return screenRecordingGrantedNow()
}

func setupScreenWasOnAtLaunch() bool { return screenRecordingAtLaunch.Load() }
