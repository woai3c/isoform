//go:build windows

package main

import "golang.org/x/sys/windows"

// freeDiskBytes is the space available to this user on the volume holding
// path.
func freeDiskBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
