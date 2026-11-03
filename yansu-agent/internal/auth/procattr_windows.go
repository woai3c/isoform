//go:build windows

package auth

import (
	"os/exec"
	"syscall"
)

func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags = 0x08000000 // CREATE_NO_WINDOW
}
