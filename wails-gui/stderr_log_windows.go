//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func redirectProcessStderr(file *os.File) error {
	// The Go runtime obtains this handle directly for panic/fatal output.
	// A native library's separate C runtime stderr descriptor is unaffected.
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(file.Fd())); err != nil {
		return err
	}
	os.Stderr = file
	return nil
}
