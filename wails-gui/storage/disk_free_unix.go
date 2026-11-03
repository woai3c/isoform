//go:build !windows

package storage

import "golang.org/x/sys/unix"

// freeDiskBytes reports bytes available to this user on the path's volume.
func freeDiskBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
