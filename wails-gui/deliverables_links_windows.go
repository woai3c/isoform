//go:build windows

package main

import (
	"os"
	"syscall"
)

// fileLinkCount is the number of hard links to the file behind info. Windows
// FileInfo does not carry it; deliverable uploads there rely on the folder
// and run-written checks.
func fileLinkCount(info os.FileInfo) uint64 {
	return 1
}

// Windows has no O_NOFOLLOW; openUploadDeliverable rechecks the resolved path
// and open descriptor identity before reading any bytes.
func openUploadFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
