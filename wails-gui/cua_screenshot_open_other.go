//go:build !darwin && !linux

package main

import "os"

func openCuaScreenshotFile(path string) (*os.File, error) {
	return os.Open(path)
}
