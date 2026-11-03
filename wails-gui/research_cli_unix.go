//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setResearchProcessGroup starts the research CLI in its own process group,
// so signalling the group also ends anything it started.
func setResearchProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killResearchProcess kills the CLI's process group, or the CLI alone when
// the group is gone.
func killResearchProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) == nil {
		return nil
	}
	return cmd.Process.Kill()
}
