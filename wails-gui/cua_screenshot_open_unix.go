//go:build darwin || linux

package main

import (
	"os"
	"syscall"
)

func openCuaScreenshotFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, os.NewSyscallError("open", err)
	}
	return os.NewFile(uintptr(fd), path), nil
}
