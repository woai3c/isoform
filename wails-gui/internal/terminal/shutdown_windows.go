//go:build windows

package terminal

// shutdownProcess kills the process directly on Windows (no SIGTERM).
func (t *terminalSession) shutdownProcess() {
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}
