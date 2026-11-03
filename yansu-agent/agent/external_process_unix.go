//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"syscall"
)

// The assistant and its stdio shim share one process group. Killing only the
// assistant leaves a live shim capable of requesting actions after cancellation.
func configureExternalProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return killExternalProcess(cmd) }
}

func killExternalProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}
