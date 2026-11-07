package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Quit-install probe. Before a quit install swaps the bundle, the candidate app
// runs headless with its data folder set to a temporary folder. Only a clean
// exit lets the swap commit. The probe covers process start, the code
// signature the OS accepts at launch, Go package initialization for the whole
// binary, and the data-folder setup in main. It does not open windows, stores,
// services, the network or the singleton lock, so a crash in any of those is
// not caught and falls to the click path's rollback.
const updateProbeFlag = "--yansu-update-probe"

// updateProbeTimeout bounds the probe. A slow start counts as a failure, since
// the quit path must not hold up quitting.
var updateProbeTimeout = 3 * time.Second

// runUpdateProbeCommand is the candidate side. It runs before the singleton
// lock and before Wails, and it only resolves the data folder. Set
// YANSU_AGENT_HOME to a temporary folder before running it.
func runUpdateProbeCommand(args []string) (int, bool) {
	if len(args) != 2 || args[0] != updateProbeFlag {
		return 0, false
	}
	home := args[1]
	if home == "" || os.Getenv(agentHomeEnv) != home {
		return 2, true
	}
	if err := exportAgentHome(); err != nil {
		return 1, true
	}
	return 0, true
}

// probeReason classifies how the probe ended. The classes are the only values
// that reach telemetry.
const (
	probeReasonClean       = "clean"
	probeReasonTimeout     = "timeout"
	probeReasonExitStatus  = "exit_status"
	probeReasonSignal      = "signal"
	probeReasonLaunchError = "launch_error"
)

// runQuitProbe runs the candidate executable with a temporary data folder and
// reports whether it exited cleanly. The temporary folder is removed afterward.
func runQuitProbe(executable string) (bool, string) {
	home, err := os.MkdirTemp("", "isoform-probe-")
	if err != nil {
		return false, probeReasonLaunchError
	}
	defer os.RemoveAll(home)

	cmd := exec.Command(executable, updateProbeFlag, home)
	cmd.Env = append(os.Environ(), agentHomeEnv+"="+home)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return false, probeReasonLaunchError
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(updateProbeTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return probeOutcome(err)
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return false, probeReasonTimeout
	}
}

// probeOutcome maps a finished process to a result and reason.
func probeOutcome(err error) (bool, string) {
	if err == nil {
		return true, probeReasonClean
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return false, probeReasonSignal
		}
		return false, probeReasonExitStatus
	}
	return false, probeReasonLaunchError
}

// probeCandidateBundle is the production probe for a prepared bundle.
func probeCandidateBundle(candidateBundle string) (bool, string) {
	exe, err := candidateExecutable(candidateBundle)
	if err != nil {
		return false, probeReasonLaunchError
	}
	return runQuitProbe(exe)
}

// candidateExecutable is the app's main binary inside the prepared bundle. It
// has the same name as the running app's binary.
func candidateExecutable(candidateBundle string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(candidateBundle, "Contents", "MacOS", filepath.Base(exe)), nil
}
