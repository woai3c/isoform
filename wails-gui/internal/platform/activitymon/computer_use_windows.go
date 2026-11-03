//go:build windows

package activitymon

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Stubs for the parts of the macOS "computer use" surface that don't have
// a Windows counterpart yet — these are kept in computer_use_stub.go via
// the !darwin build tag for non-Windows non-macOS platforms; Windows
// overrides only the helpers it can implement (ListRunningApps) and
// re-defines the rest as no-ops here.
func ComputerUseSupported() bool                    { return false }
func ComputerUsePermitted() bool                    { return false }
func CollectAppElements(appName string) []UIElement { _ = appName; return nil }
func OpenURL(url string) bool                       { _ = url; return false }
func ClickAt(x, y float64)                          { _, _ = x, y }
func TypeText(text string)                          { _ = text }
func PressKey(keyName string)                       { _ = keyName }
func Hotkey(combo string)                           { _ = combo }
func Scroll(deltaX, deltaY int)                     { _, _ = deltaX, deltaY }
func Drag(fromX, fromY, toX, toY float64)           { _, _, _, _ = fromX, fromY, toX, toY }
func PasteText(text string)                         { _ = text }

// ── Win32 process snapshot ────────────────────────────────────────────────
//
// CreateToolhelp32Snapshot + Process32FirstW/NextW enumerate every running
// process plus its PID and exe basename. We use it to expose ListRunningApps
// so the meeting watcher can answer "is this the same Zoom process as last
// time?" — necessary for the resume tie-breaker that prevents two genuinely
// distinct meetings on a long-lived Zoom process from collapsing into one.

const (
	th32csSnapprocess = 0x00000002
	// MAX_PATH for the szExeFile field on PROCESSENTRY32W.
	procEntryExeFileMaxLen = 260
)

var (
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	procProcess32NextW           = kernel32.NewProc("Process32NextW")
	procGetWindowLongW           = user32.NewProc("GetWindowLongW")
	// procEnumWindows / procIsWindowVisible / procGetWindow plus the
	// gwOwner / gwlExStyle / wsExToolWindow constants are declared in
	// axtree_tree_windows.go (same package). Reuse those.
)

// processEntry32W mirrors the Win32 PROCESSENTRY32W struct used by
// Process32FirstW / Process32NextW. The field order, sizes, and the
// trailing szExeFile WCHAR[MAX_PATH] are load-bearing — don't reorder.
type processEntry32W struct {
	DwSize              uint32
	CntUsage            uint32
	Th32ProcessID       uint32
	Th32DefaultHeapID   uintptr
	Th32ModuleID        uint32
	CntThreads          uint32
	Th32ParentProcessID uint32
	PcPriClassBase      int32
	DwFlags             uint32
	SzExeFile           [procEntryExeFileMaxLen]uint16
}

// ListRunningApps enumerates every running process and returns its exe
// basename (lowercased, matching how Windows bundle ids are stored
// elsewhere in this package; see monitor_windows.go's baseNameWithExt
// usage) plus its PID. Returns nil if the snapshot couldn't be opened.
//
// Caller's responsibility to match the returned BundleID against the
// meeting-app bundle ids (zoom.exe, ms-teams.exe, etc.); we don't do any
// filtering here — that's domain logic above this layer.
func ListRunningApps() []RunningApp {
	hSnap, _, _ := procCreateToolhelp32Snapshot.Call(uintptr(th32csSnapprocess), 0)
	// CreateToolhelp32Snapshot returns INVALID_HANDLE_VALUE (-1) on
	// failure; on most modern Windows that's the only error mode for this
	// flag set (PROCESS), but be defensive about 0 too.
	if hSnap == 0 || hSnap == ^uintptr(0) {
		return nil
	}
	defer procCloseHandle.Call(hSnap)

	var entry processEntry32W
	entry.DwSize = uint32(unsafe.Sizeof(entry))

	ok, _, _ := procProcess32FirstW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
	if ok == 0 {
		return nil
	}

	apps := make([]RunningApp, 0, 64)
	for {
		exe := syscall.UTF16ToString(entry.SzExeFile[:])
		if exe != "" {
			bundleID := strings.ToLower(exe)
			apps = append(apps, RunningApp{
				Name:     exe,
				BundleID: bundleID,
				PID:      int(entry.Th32ProcessID),
				IsActive: false,
			})
		}
		ok, _, _ := procProcess32NextW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
		if ok == 0 {
			break
		}
	}
	return apps
}

// listAppWindowsMaxWindows mirrors the macOS cap (computer_use_darwin.go:753).
// 50 covers an active Zoom meeting with screen-share toolbars and a handful
// of leftover background helpers without unbounded growth if some app spawns
// dozens of hidden windows.
const listAppWindowsMaxWindows = 50

// listAppWindowsState is mutated from inside listAppWindowsEnumProc (called by the
// kernel during EnumWindows). The mutex serialises ListAppWindows calls so
// the single shared callback can safely close over per-call state without
// blowing out Go's process-wide syscall.NewCallback table (hard-capped at
// ~2000 — calling NewCallback per ListAppWindows invocation would exhaust
// it within a few hours of normal 15s meeting-poll cadence).
var (
	listAppWindowsMu    sync.Mutex
	listAppWindowsState struct {
		wantedPIDs map[uint32]struct{}
		windows    []AppWindow
	}
	listAppWindowsCallback = syscall.NewCallback(listAppWindowsEnumProc)
)

// isTaskbarWorthyWindow approximates the "user-facing top-level window"
// test the Windows taskbar applies. macOS's CGWindowList already returns
// only this kind of window; EnumWindows on Windows returns IME hosts,
// invisible tray helpers, MSCTFIME UI, popup tooltips, and dozens of
// other per-process noise windows. Without this filter, IsMeetingAppActive's
// idleMaxWindows=2 count heuristic falsely fires for idle Zoom (many
// helper windows) and the meeting watcher stays stuck in inMeeting=true
// after the user hangs up, so MEETING_ENDED never triggers and no
// transcript ever lands.
//
// The filter:
//   - WS_EX_TOOLWINDOW set → tool / palette window, never in the taskbar.
//   - GW_OWNER != 0 → owned popup (dialog child), not a top-level app window.
//   - Empty title → almost always a helper / IME / hidden host.
//
// What's still kept: visible top-level windows with a non-empty title
// (the meeting view, the controls bar, the share-screen toolbar — all of
// which Zoom titles as "Zoom Meeting", "Meeting Controls", "Share screen",
// etc.). The window-count heuristic still works for those.
func isTaskbarWorthyWindow(hwnd uintptr) bool {
	exStyleRaw, _, _ := procGetWindowLongW.Call(hwnd, uintptr(uint32(gwlExStyle)))
	if uint32(exStyleRaw)&wsExToolWindow != 0 {
		return false
	}
	owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
	if owner != 0 {
		return false
	}
	return true
}

func listAppWindowsEnumProc(hwnd uintptr, _ uintptr) uintptr {
	if len(listAppWindowsState.windows) >= listAppWindowsMaxWindows {
		return 0 // stop enumerating
	}
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return 1
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if _, ok := listAppWindowsState.wantedPIDs[pid]; !ok {
		return 1
	}
	if !isTaskbarWorthyWindow(hwnd) {
		return 1
	}
	buf := make([]uint16, 512)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	title := syscall.UTF16ToString(buf)
	if title == "" {
		// At this point we've already filtered out tool/owned helper
		// windows; what's left is a top-level unowned visible window
		// with no title — Electron/Chromium splash placeholders and
		// similar transient host windows. They aren't taskbar entries
		// and would still inflate the idleMaxWindows count, so skip.
		return 1
	}
	listAppWindowsState.windows = append(listAppWindowsState.windows, AppWindow{Title: title, IsMain: false})
	return 1
}

// ListAppWindows returns every visible top-level window owned by a process
// whose exe basename (or bundle id) matches appName. Powers the meeting
// watcher's background-scan path: when Zoom isn't the foreground app the
// watcher still needs to see "is there a window titled 'Zoom Meeting'?"
// to recognise the call as ongoing. Without a real implementation
// detectActiveMeeting only worked while Zoom was frontmost, so switching
// away to take notes silently ended every meeting.
//
// Matches by PID rather than by re-querying each window's exe path —
// ListRunningApps already enumerated processes, so we use that to build
// a {target PID → exe} map first, then EnumWindows filters on PID.
func ListAppWindows(appName string) []AppWindow {
	if appName == "" {
		return nil
	}
	wantedPIDs := make(map[uint32]struct{})
	for _, ra := range ListRunningApps() {
		if strings.EqualFold(ra.Name, appName) || strings.EqualFold(ra.BundleID, appName) {
			wantedPIDs[uint32(ra.PID)] = struct{}{}
		}
	}
	if len(wantedPIDs) == 0 {
		return nil
	}

	listAppWindowsMu.Lock()
	defer listAppWindowsMu.Unlock()

	listAppWindowsState.wantedPIDs = wantedPIDs
	listAppWindowsState.windows = listAppWindowsState.windows[:0]
	procEnumWindows.Call(listAppWindowsCallback, 0)
	// Copy out before releasing the lock so the next caller can't
	// truncate the slice we return.
	out := make([]AppWindow, len(listAppWindowsState.windows))
	copy(out, listAppWindowsState.windows)
	listAppWindowsState.wantedPIDs = nil
	return out
}
