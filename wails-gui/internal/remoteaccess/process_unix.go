//go:build unix

package remoteaccess

import (
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessGroup(cmd *exec.Cmd, done <-chan struct{}, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	<-done
}
