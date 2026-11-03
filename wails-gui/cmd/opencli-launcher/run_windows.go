//go:build windows

package main

import "os/exec"

func run(cmd *exec.Cmd) error { return cmd.Run() }
