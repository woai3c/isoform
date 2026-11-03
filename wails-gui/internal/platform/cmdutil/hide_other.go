//go:build !windows

package cmdutil

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}

func startWithJobObject(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}
	return func() {}, nil
}
