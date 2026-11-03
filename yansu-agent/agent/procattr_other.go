//go:build !windows

package agent

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}
