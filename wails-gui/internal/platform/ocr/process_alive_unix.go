//go:build !windows

package ocr

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with pid exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
