//go:build !darwin

package main

import (
	"os/exec"
	"time"
)

func detachUpdateHelper(*exec.Cmd)                  {}
func updateProcessAlive(int) bool                   { return false }
func waitUpdateProcessExit(int, time.Duration) bool { return true }
