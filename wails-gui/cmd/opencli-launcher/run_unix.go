//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// Keep the caller's cancellation and signals attached to Bun itself. A
// wrapper child would survive exec.CommandContext killing only its parent.
func run(cmd *exec.Cmd) error {
	return syscall.Exec(cmd.Path, cmd.Args, cmd.Env)
}
