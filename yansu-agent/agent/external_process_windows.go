//go:build windows

package agent

import (
	"os"
	"os/exec"
)

// Controlled routes are ineligible on Windows until the per-run socket and
// process-tree controls have an equivalent implementation.
func configureExternalProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return killExternalProcess(cmd) }
}

func killExternalProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
