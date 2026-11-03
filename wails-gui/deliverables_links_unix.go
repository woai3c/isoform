//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// fileLinkCount is the number of hard links to the file behind info.
func fileLinkCount(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}

// openUploadFile walks an already resolved absolute path using directory
// descriptors. O_NOFOLLOW on only the leaf would still follow a directory
// swapped for a symlink between validation and open.
func openUploadFile(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, &os.PathError{Op: "open", Path: path, Err: unix.EINVAL}
	}
	dir, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = unix.Close(dir) }()
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(dir, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		_ = unix.Close(dir)
		dir = next
	}
	fd, err := unix.Openat(dir, parts[len(parts)-1], unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
