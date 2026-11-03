//go:build !windows

package gitutil

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}
