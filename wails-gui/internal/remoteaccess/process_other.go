//go:build !unix

package remoteaccess

import (
	"os/exec"
	"time"
)

func configureProcessGroup(*exec.Cmd) {}

func terminateProcessGroup(cmd *exec.Cmd, done <-chan struct{}, _ time.Duration) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		<-done
	}
}
