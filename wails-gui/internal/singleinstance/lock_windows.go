//go:build windows

package singleinstance

import (
	"fmt"
	"syscall"
	"unsafe"
)

// errorAlreadyExists is the Windows error code Win32 SetLastError returns
// from CreateMutex when a named mutex with the same name already exists.
const errorAlreadyExists syscall.Errno = 183

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
)

type windowsLock struct {
	handle uintptr
}

func (l *windowsLock) Release() error {
	if l.handle != 0 {
		procCloseHandle.Call(l.handle)
		l.handle = 0
	}
	return nil
}

// acquireLock takes a Windows named-mutex single-instance lock scoped to
// the current user session. Different users on the same machine each get
// their own lock.
func acquireLock(appID string) (instanceLock, error) {
	// Local\ is the per-session namespace. Multiple users on the same
	// Windows host can each run their own copy of the app, but a single
	// user can't double-launch.
	fullName := `Local\` + appID
	namePtr, err := syscall.UTF16PtrFromString(fullName)
	if err != nil {
		return nil, fmt.Errorf("encode mutex name: %w", err)
	}

	handle, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return nil, fmt.Errorf("CreateMutex failed: %v", lastErr)
	}

	// CreateMutex returns a valid handle even when the mutex already exists;
	// the duplicate-handle case is signalled via GetLastError. We must close
	// our duplicate so the original holder's handle stays the only reference.
	if errno, ok := lastErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		procCloseHandle.Call(handle)
		return nil, ErrAlreadyRunning
	}

	return &windowsLock{handle: handle}, nil
}
