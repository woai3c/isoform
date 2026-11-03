//go:build windows

package main

import (
	"log"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// forceWindowFocus brings the window with the given exact title to
// the foreground and gives it keyboard focus. Wails' WindowShow only
// makes the window visible — Windows' foreground lock keeps keyboard
// focus on the previous app unless we explicitly steal it. Without
// this, the user has to click the spotlight before they can type.
//
// On a cold start Wails' window-show is queued asynchronously to the
// UI thread, so the HWND can exist (FindWindowW finds it) for a few
// frames before Windows actually paints it. SetForegroundWindow
// against an invisible window silently no-ops. We poll IsWindowVisible
// up to ~250ms before stealing focus.
//
// Uses the AttachThreadInput trick: temporarily attaches our input
// queue to the foreground thread's so SetForegroundWindow succeeds
// even though we'd otherwise be blocked by the foreground lock.
func forceWindowFocus(title string) {
	hwnd, err := findWindowByTitle(title)
	if err != nil || hwnd == 0 {
		log.Printf("[spotlight] forceWindowFocus: window %q not found (err=%v)", title, err)
		return
	}

	user32 := windows.NewLazySystemDLL("user32.dll")
	getForegroundWindow := user32.NewProc("GetForegroundWindow")
	getWindowThreadProcessId := user32.NewProc("GetWindowThreadProcessId")
	attachThreadInput := user32.NewProc("AttachThreadInput")
	setForegroundWindow := user32.NewProc("SetForegroundWindow")
	setFocus := user32.NewProc("SetFocus")
	bringWindowToTop := user32.NewProc("BringWindowToTop")
	showWindow := user32.NewProc("ShowWindow")
	isWindowVisible := user32.NewProc("IsWindowVisible")

	const swShow = 5
	const swRestore = 9

	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getCurrentThreadId := kernel32.NewProc("GetCurrentThreadId")

	// Wait for Wails' async WindowShow to actually paint the HWND.
	// 25ms × 10 attempts = 250ms upper bound — well above what a
	// non-cold start needs, still imperceptible to the user.
	visible := false
	for i := 0; i < 10; i++ {
		r, _, _ := isWindowVisible.Call(uintptr(hwnd))
		if r != 0 {
			visible = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !visible {
		log.Printf("[spotlight] forceWindowFocus: window %q never became visible after 250ms; foregrounding anyway", title)
	}

	currentThread, _, _ := getCurrentThreadId.Call()

	fgHwnd, _, _ := getForegroundWindow.Call()
	var fgThread uintptr
	if fgHwnd != 0 {
		fgThread, _, _ = getWindowThreadProcessId.Call(fgHwnd, 0)
	}

	// Attach our input queue to the foreground thread's so the OS
	// considers us a sibling of the focused app — bypasses the
	// foreground-lock protection.
	attached := false
	if fgThread != 0 && fgThread != currentThread {
		r, _, _ := attachThreadInput.Call(currentThread, fgThread, 1)
		attached = r != 0
		if attached {
			defer attachThreadInput.Call(currentThread, fgThread, 0)
		}
	}

	showWindow.Call(uintptr(hwnd), swRestore)
	showWindow.Call(uintptr(hwnd), swShow)
	bringWindowToTop.Call(uintptr(hwnd))
	r, _, sfwErr := setForegroundWindow.Call(uintptr(hwnd))
	setFocus.Call(uintptr(hwnd))

	if r == 0 {
		log.Printf("[spotlight] forceWindowFocus: SetForegroundWindow failed (visible=%v attached=%v fg_hwnd=0x%x err=%v)",
			visible, attached, fgHwnd, sfwErr)
	} else {
		log.Printf("[spotlight] forceWindowFocus: stole focus (visible=%v attached=%v)", visible, attached)
	}
}

// findWindowByTitle returns the HWND of the top-level window whose
// exact title matches `title`. Returns 0 when no match exists.
func findWindowByTitle(title string) (windows.Handle, error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	findWindowW := user32.NewProc("FindWindowW")

	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}
	hwnd, _, _ := findWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	return windows.Handle(hwnd), nil
}

// Win32 monitor / DPI helpers used by setSpotlightFrameWin32. The
// MONITORINFO layout matches the C struct: cbSize must be filled in
// by the caller before GetMonitorInfoW will populate the rest.

const monitorDefaultToNearest = 0x2

type win32Rect struct {
	left, top, right, bottom int32
}

type win32MonitorInfo struct {
	cbSize    uint32
	rcMonitor win32Rect
	rcWork    win32Rect
	dwFlags   uint32
}

// setSpotlightFrameWin32 applies the spotlight window's outer size and
// position in a single SetWindowPos call. Wails' WindowSetSize +
// WindowSetPosition runs as two separate OS operations — with the
// bottom-anchored layout (where pill→conversation requires both
// growing the window AND jumping its top edge up by ~560px on 1080p),
// the two-step path is visible as a flicker: the window first extends
// past the screen bottom (from the resize, which keeps the old top),
// then snaps upward to its final position. SetWindowPos updates both
// fields in one OS message so the only visible state is the final
// geometry.
//
// Inputs are LOGICAL pixels (matching Wails' WindowSetSize); the
// function handles per-monitor DPI scaling internally so the position
// is correct on 125%/150% displays too. Returns true on success;
// caller falls back to the Wails two-step path on false.
func setSpotlightFrameWin32(title string, w, h int) bool {
	hwnd, err := findWindowByTitle(title)
	if err != nil || hwnd == 0 {
		return false
	}

	user32 := windows.NewLazySystemDLL("user32.dll")
	monitorFromWindow := user32.NewProc("MonitorFromWindow")
	getMonitorInfoW := user32.NewProc("GetMonitorInfoW")
	getDpiForWindow := user32.NewProc("GetDpiForWindow")
	setWindowPos := user32.NewProc("SetWindowPos")

	mon, _, _ := monitorFromWindow.Call(uintptr(hwnd), monitorDefaultToNearest)
	if mon == 0 {
		return false
	}
	var info win32MonitorInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, _ := getMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&info))); r == 0 {
		return false
	}

	// GetDpiForWindow is Win 10 1607+ (the spotlight already targets
	// modern Windows, see dwm_rounded_windows.go). 96 DPI = 100%.
	dpiR, _, _ := getDpiForWindow.Call(uintptr(hwnd))
	dpi := uint32(dpiR)
	if dpi == 0 {
		dpi = 96
	}
	scale := float64(dpi) / 96.0

	workL := int(info.rcWork.left)
	workT := int(info.rcWork.top)
	workW := int(info.rcWork.right - info.rcWork.left)
	workH := int(info.rcWork.bottom - info.rcWork.top)

	pw := int(float64(w) * scale)
	ph := int(float64(h) * scale)
	px := workL + max((workW-pw)/2, 0)
	py := workT + max(int(float64(workH)*spotlightBottomFraction)-ph, 0)

	r, _, _ := setWindowPos.Call(
		uintptr(hwnd),
		0, // hwndInsertAfter — ignored with SWP_NOZORDER
		uintptr(px),
		uintptr(py),
		uintptr(pw),
		uintptr(ph),
		uintptr(swpNoZOrder|swpNoActivate),
	)
	return r != 0
}
