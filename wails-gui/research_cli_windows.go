//go:build windows

package main

import "os/exec"

func setResearchProcessGroup(cmd *exec.Cmd) {}

func killResearchProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
