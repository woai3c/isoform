//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func redirectProcessStderr(file *os.File) error {
	// os.Stderr alone does not redirect Go's runtime or cgo writes to fd 2.
	// Keep the original os.Stderr object alive: its finalizer owns fd 2.
	return unix.Dup2(int(file.Fd()), 2)
}
