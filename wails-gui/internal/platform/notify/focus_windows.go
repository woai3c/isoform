//go:build windows

package notify

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
)

// IsAppForeground returns true if the foreground window belongs to the
// current process. This correctly detects when the window is behind other
// windows (not just minimised), making it more reliable than
// runtime.WindowIsMinimised alone.
func IsAppForeground() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}

	var pid uint32
	procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid == uint32(os.Getpid())
}
