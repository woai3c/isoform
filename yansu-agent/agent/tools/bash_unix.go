//go:build !windows

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the subprocess in its own process group
// on Unix so we can kill the whole tree (children, grandchildren) by
// signalling -pgid.
func configureProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// killProcessGroup sends sig to the entire process group started by c.
// It's a best-effort operation: we ignore "no such process" since the
// process may have exited between the decision to kill and the syscall.
func killProcessGroup(c *exec.Cmd, sig syscall.Signal) {
	if c == nil || c.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(c.Process.Pid)
	if err != nil {
		// Fall back to just the process itself.
		_ = c.Process.Signal(sig)
		return
	}
	_ = syscall.Kill(-pgid, sig)
}

// fileLinkCount returns how many names (hard links) a file has.
func fileLinkCount(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
