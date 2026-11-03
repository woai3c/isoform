//go:build windows

package main

import "os"

const bunExeName = "bun.exe"

// isExecutableFile reports whether path is a regular file (Windows has no
// Unix-style permission bits, so existence is sufficient).
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return true
}
