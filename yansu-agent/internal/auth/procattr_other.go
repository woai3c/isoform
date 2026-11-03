//go:build !windows

package auth

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}
