//go:build !darwin

package main

import "fmt"

func startRelaunchAfterExit(int, string, []string, func()) error {
	return fmt.Errorf("quit and reopen is only available on macOS")
}

func recordScreenRecordingAtLaunch() {}

func screenRecordingNeedsRelaunch() bool { return false }

func setupScreenWasOnAtLaunch() bool { return true }
