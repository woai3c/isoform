//go:build !windows

package terminal

import (
	"os"
	"runtime"
)

// defaultShellCommand returns the shell and arguments for Unix platforms.
func defaultShellCommand() (string, []string) {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell, []string{"-l"}
	}
	switch runtime.GOOS {
	case "darwin":
		return "/bin/zsh", []string{"-l"}
	default:
		return "/bin/bash", []string{"-l"}
	}
}
