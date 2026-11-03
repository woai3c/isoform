//go:build !windows

package daemon

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}
