//go:build !windows

package main

import "os"

const bunExeName = "bun"

// isExecutableFile reports whether path is a regular file with execute bits.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}
