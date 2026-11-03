//go:build !windows

package terminal

import (
	"syscall"
	"time"
)

// shutdownProcess sends SIGTERM then SIGKILL after a timeout on Unix.
func (t *terminalSession) shutdownProcess() {
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Signal(syscall.SIGTERM)
		go func() {
			select {
			case <-time.After(3 * time.Second):
				if t.cmd.Process != nil {
					_ = t.cmd.Process.Kill()
				}
			case <-t.done:
				// Process already exited.
			}
		}()
	}
}
