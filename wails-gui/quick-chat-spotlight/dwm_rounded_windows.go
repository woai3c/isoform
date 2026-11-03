//go:build windows

package main

import (
	"log"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 / DWM constants used by the rounded-frameless trick. The
// GWL_* indices are negative offsets from the per-window storage base;
// uintptr can't hold a negative compile-time constant, so we keep
// them as `var int32` — that defers the (sign-extending) conversion
// to uintptr until runtime, which is fine.
var (
	gwlStyle    int32 = -16
	gwlExStyle  int32 = -20
	gwlpWndProc int32 = -4
)

const (
	wsThickFrame   = 0x00040000
	wsExToolWindow = 0x00000080 // hides window from taskbar + Alt+Tab
	wsExAppWindow  = 0x00040000 // forces taskbar inclusion — must be cleared

	wmNcCalcSize = 0x0083
	wmNcHitTest  = 0x0084
	wmActivate   = 0x0006
	waInactive   = 0
	htClient     = 1

	swSetHide   = 0
	swSetShowNa = 8 // SW_SHOWNA — show without activating

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	// Win11+ DwmSetWindowAttribute keys.
	dwmwaWindowCornerPreference = 33
	dwmwcpRound                 = 2 // standard ~8px corners

	// DWMWA_USE_IMMERSIVE_DARK_MODE — tells DWM to paint the window's
	// title bar / border in the dark palette. Without this, even a
	// dark-themed window keeps a light 1px top border (visible as the
	// "white edge" dark-mode users complain about). Constant value 20
	// works on Win10 build 19041 (20H1) and later, including all Win11
	// builds. Older Win10 used 19; we target 20 since the rest of this
	// file already requires modern Windows.
	dwmwaUseImmersiveDarkMode = 20
)

var (
	dwmUser32 = windows.NewLazySystemDLL("user32.dll")
	dwmDwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procGetWindowLongW        = dwmUser32.NewProc("GetWindowLongW")
	procSetWindowLongW        = dwmUser32.NewProc("SetWindowLongW")
	procSetWindowLongPtrW     = dwmUser32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW       = dwmUser32.NewProc("CallWindowProcW")
	procSetWindowPos          = dwmUser32.NewProc("SetWindowPos")
	procShowWindow            = dwmUser32.NewProc("ShowWindow")
	procDwmSetWindowAttribute = dwmDwmapi.NewProc("DwmSetWindowAttribute")
)

var (
	// originalWndProc is the pointer Wails set up before we subclassed.
	// We delegate every message we don't handle ourselves back to it.
	// Stored in a package var so the subclass survives across hide/show
	// cycles — installing it once is enough.
	originalWndProc uintptr

	// customWndProcCallback keeps the syscall.NewCallback trampoline
	// reachable for the lifetime of the process. The OS invokes it on
	// every Windows message; Go GC mustn't move it around.
	customWndProcCallback uintptr

	// onWindowDeactivated runs whenever the spotlight window receives
	// WM_ACTIVATE with WA_INACTIVE — i.e. another top-level window
	// (typically the main Yansu app, but any external app) has taken
	// focus. main.go sets this to call wailsruntime.WindowHide so the
	// spotlight auto-dismisses when the user clicks anywhere else,
	// matching Raycast / Alfred / PowerToys Run behaviour.
	onWindowDeactivated func()
)

// wndProcOverride is a thin window-procedure subclass that ONLY
// listens for WM_ACTIVATE / WA_INACTIVE so we can fire the
// auto-hide-on-blur callback. Every other message passes straight
// through to Wails' original proc. We previously also handled
// WM_NCCALCSIZE / WM_NCHITTEST as part of a WS_THICKFRAME-based
// "Win11 native rounded corners" hack — that turned out to leave a
// 1px sizing-border sliver visible at the top of the window
// (Windows' famous WS_THICKFRAME 1-pixel issue). CSS border-radius
// already paints clean rounded corners on the card surface, so we
// abandoned the OS-level rounding and the sliver disappears.
//
// MUST NOT panic — Go panics in OS callbacks crash the WebView.
func wndProcOverride(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == wmActivate {
		// Low word of wParam is the activation state; WA_INACTIVE
		// means another top-level window has taken focus. Fire the
		// hide callback in a goroutine so we don't block the message
		// loop (and so Wails' WindowHide, which may post messages of
		// its own, doesn't recurse into us synchronously).
		if (wParam & 0xFFFF) == waInactive {
			cb := onWindowDeactivated
			if cb != nil {
				go cb()
			}
		}
	}
	ret, _, _ := procCallWindowProcW.Call(originalWndProc, hwnd, uintptr(msg), wParam, lParam)
	return ret
}

// applyDwmRoundedFrameless USED to add WS_THICKFRAME + a WndProc
// subclass swallowing WM_NCCALCSIZE / WM_NCHITTEST so DWM would draw
// native Win11 ~8px rounded corners. That approach left a 1px white
// sizing-border sliver across the top of the window in dark mode
// (a long-standing WS_THICKFRAME quirk Windows exposes regardless of
// WM_NCCALCSIZE returning 0), and DWM's CORNER_PREFERENCE only works
// on Win11 anyway. CSS border-radius:16px on .pill / 20px on .conv
// already paints the rounded card visually on every supported
// Windows build, so we now just install the WndProc subclass for
// blur detection and skip the frame manipulation entirely.
func applyDwmRoundedFrameless(title string) {
	hwnd, err := findWindowByTitle(title)
	if err != nil || hwnd == 0 {
		return
	}

	// Install the WndProc subclass once so WM_ACTIVATE WA_INACTIVE
	// reaches onWindowDeactivated (auto-hide-on-blur for the pill).
	if originalWndProc == 0 {
		customWndProcCallback = syscall.NewCallback(wndProcOverride)
		prev, _, _ := procSetWindowLongPtrW.Call(
			uintptr(hwnd),
			uintptr(gwlpWndProc),
			customWndProcCallback,
		)
		if prev == 0 {
			log.Printf("[spotlight] WndProc subclass install failed; auto-hide-on-blur disabled")
			return
		}
		originalWndProc = prev
	}

	// SWP_FRAMECHANGED is no longer strictly required since we don't
	// flip GWL_STYLE, but it's cheap and harmless and keeps the
	// trailing call site the same shape across Wails versions.
	procSetWindowPos.Call(
		uintptr(hwnd), 0,
		0, 0, 0, 0,
		uintptr(swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged),
	)
}

// hideFromTaskbar gives the window the WS_EX_TOOLWINDOW extended
// style (and clears WS_EX_APPWINDOW just in case) so it doesn't show
// in the Windows taskbar or Alt+Tab list. This matches the standard
// spotlight UX (Raycast, Alfred, PowerToys Run, Flow Launcher).
//
// WS_EX_TOOLWINDOW must be set BEFORE the window's WS_VISIBLE flag
// flips on — otherwise the taskbar listener already has an entry and
// the next hide/show cycle is needed to make explorer drop it. We
// call this:
//  1. Once at startup (window still hidden via StartHidden: true) —
//     the cheap path that should cover ~all cases.
//  2. Again on every /show, just before WindowShow, in case the
//     startup call missed (Wails hadn't created the HWND yet) or
//     something reset the style. Idempotent — checking the current
//     style first avoids redundant style flips.
//
// Polls for the HWND for up to 250ms to handle the cold-start race
// where Wails hasn't finished creating the Win32 window yet.
func hideFromTaskbar(title string) {
	var hwnd windows.Handle
	for i := 0; i < 10; i++ {
		h, err := findWindowByTitle(title)
		if err == nil && h != 0 {
			hwnd = h
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if hwnd == 0 {
		log.Printf("[spotlight] hideFromTaskbar: window %q not found", title)
		return
	}
	exStyle, _, _ := procGetWindowLongW.Call(uintptr(hwnd), uintptr(gwlExStyle))
	desired := exStyle | uintptr(wsExToolWindow)
	desired &^= uintptr(wsExAppWindow)
	if desired == exStyle {
		// Already correctly styled.
		return
	}
	// Style flip only takes effect once the window is hidden then
	// shown again (taskbar listener notices on WS_VISIBLE transition).
	// Sequence: hide → set style → show-without-activate → hide.
	// The trailing hide keeps the window invisible until the real
	// /show triggers WindowShow.
	procShowWindow.Call(uintptr(hwnd), uintptr(swSetHide))
	procSetWindowLongW.Call(uintptr(hwnd), uintptr(gwlExStyle), desired)
	// SWP_FRAMECHANGED nudges the OS to re-evaluate the style.
	procSetWindowPos.Call(
		uintptr(hwnd), 0,
		0, 0, 0, 0,
		uintptr(swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged),
	)
	procShowWindow.Call(uintptr(hwnd), uintptr(swSetShowNa))
	procShowWindow.Call(uintptr(hwnd), uintptr(swSetHide))
	log.Printf("[spotlight] hidden from taskbar (WS_EX_TOOLWINDOW set, WS_EX_APPWINDOW cleared)")
}

// setOnWindowDeactivated registers a callback that fires when the
// spotlight window loses activation to another top-level window. Used
// by main.go to wire up auto-hide-on-blur.
func setOnWindowDeactivated(fn func()) {
	onWindowDeactivated = fn
}

// applyDwmDarkMode tells DWM whether the window's frame decorations
// (the 1px top border that Win10/11 paints around our WS_THICKFRAME
// window, and the rounded-corner border ring on Win11) should use the
// dark palette. Without this, dark-mode users see a light strip at the
// top of the spotlight pill that looks out of place against the dark
// surface — that's the "white edge" symptom.
//
// Idempotent and safe to call on every show; the OS reapplies the
// decoration on the next compositor frame.
func applyDwmDarkMode(title string, dark bool) {
	hwnd, err := findWindowByTitle(title)
	if err != nil || hwnd == 0 {
		return
	}
	var v int32
	if dark {
		v = 1
	}
	r, _, _ := procDwmSetWindowAttribute.Call(
		uintptr(hwnd),
		uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&v)),
		unsafe.Sizeof(v),
	)
	if r != 0 {
		log.Printf("[spotlight] DwmSetWindowAttribute(USE_IMMERSIVE_DARK_MODE=%v) hr=0x%x", dark, r)
	}
}
