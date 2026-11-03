//go:build !windows

package main

import "os"

func cleanupRetiredRotatingLogFiles(path string) error {
	return nil
}

func retireRotatingLogFile(path string) error {
	return os.Remove(path)
}
