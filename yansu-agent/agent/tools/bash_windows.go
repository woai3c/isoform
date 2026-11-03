//go:build windows

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	// CREATE_NEW_PROCESS_GROUP gives us a process group to signal on
	// timeout. CREATE_NO_WINDOW + HideWindow prevent cmd.exe from
	// flashing a console window for every Bash call — without these,
	// the desktop GUI spawns a visible terminal that steals focus on
	// each tool invocation. Matches internal/gitutil/procattr_windows.go
	// and internal/daemon/procattr_windows.go.
	c.SysProcAttr.HideWindow = true
	c.SysProcAttr.CreationFlags |= 0x00000200 | 0x08000000 // CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
}

func killProcessGroup(c *exec.Cmd, sig syscall.Signal) {
	if c == nil || c.Process == nil {
		return
	}
	// Windows lacks POSIX signals; best effort is to kill the process.
	_ = c.Process.Kill()
}

// fileLinkCount returns how many names (hard links) a file has. Windows
// does not report it through os.FileInfo, so every file counts as one.
func fileLinkCount(info os.FileInfo) uint64 {
	return 1
}
