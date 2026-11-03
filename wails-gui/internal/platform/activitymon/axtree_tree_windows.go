//go:build windows

package activitymon

import (
	"encoding/json"
	"log"
	"sync"
	"syscall"
	"unsafe"
)

// CollectFocusedWindowTree returns the structured UI Automation tree of the
// foreground window. Mirrors the macOS implementation so the appparser
// dispatch (Slack/Notion/Linear/etc.) can run on Windows without changes.
//
// Returns nil when the UIA subprocess fails or the foreground window has
// no UIA content (e.g. fullscreen DirectX games, secure desktop).
func CollectFocusedWindowTree() *AXNode {
	raw, _, _ := getUIA().CollectTree(0)
	if len(raw) == 0 {
		return nil
	}
	var node AXNode
	if err := json.Unmarshal(raw, &node); err != nil {
		log.Printf("[uia] unmarshal focused-window tree failed: %v", err)
		return nil
	}
	if node.Role == "" {
		return nil
	}
	return &node
}

// CollectFocusedWindowTreeJSON returns the raw JSON serialization of the
// focused window's UIA tree without round-tripping through Go structs.
// Used by ax-tree-dump so debug output preserves every field the walker
// emitted.
func CollectFocusedWindowTreeJSON() string {
	raw, _, _ := getUIA().CollectTree(0)
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

// CollectWindowTreeByBundleJSON looks up the running process whose lowercase
// exe basename matches `bundleID` (Windows uses exe basenames as the
// "bundle ID" stand-in — see monitor_windows.go FrontmostAppBundleID), then
// walks the UIA tree of its main top-level window. Returns "" when no
// matching process is running or the process has no top-level window.
//
// On macOS this is used by ax-tree-dump to bypass Universal Control's
// frontmost-app hijacking. On Windows there is no equivalent hazard, so
// callers normally use CollectFocusedWindowTreeJSON — but the dump tool
// also accepts --bundle, so we provide a matching entry point.
func CollectWindowTreeByBundleJSON(bundleID string) string {
	hwnd := findTopLevelWindowForBundle(bundleID, 0)
	if hwnd == 0 {
		return ""
	}
	raw, _, _ := getUIA().CollectTree(int64(hwnd))
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

// CollectWindowTreeByBundle is the structured-Go counterpart of
// CollectWindowTreeByBundleJSON.
func CollectWindowTreeByBundle(bundleID string) *AXNode {
	raw := CollectWindowTreeByBundleJSON(bundleID)
	if raw == "" {
		return nil
	}
	var node AXNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return nil
	}
	if node.Role == "" {
		return nil
	}
	return &node
}

// CollectWindowTreeByBundleIndexJSON returns the UIA tree of the Nth
// top-level window of the process matching `bundleID`.
func CollectWindowTreeByBundleIndexJSON(bundleID string, windowIndex int) string {
	if windowIndex < 0 {
		return ""
	}
	hwnd := findTopLevelWindowForBundle(bundleID, windowIndex)
	if hwnd == 0 {
		return ""
	}
	raw, _, _ := getUIA().CollectTree(int64(hwnd))
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

// CollectWindowTreeByBundleIndex is the structured-Go counterpart of
// CollectWindowTreeByBundleIndexJSON.
func CollectWindowTreeByBundleIndex(bundleID string, windowIndex int) *AXNode {
	raw := CollectWindowTreeByBundleIndexJSON(bundleID, windowIndex)
	if raw == "" {
		return nil
	}
	var node AXNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return nil
	}
	if node.Role == "" {
		return nil
	}
	return &node
}

// ListWindowsByBundleJSON returns a JSON array describing every top-level
// window owned by the process matching `bundleID`. Each entry carries the
// window's HWND, title, and visibility flag — enough for ax-tree-dump to
// let an operator pick which window to walk.
func ListWindowsByBundleJSON(bundleID string) string {
	wins := enumerateTopLevelWindowsForBundle(bundleID)
	if len(wins) == 0 {
		return ""
	}
	data, err := json.Marshal(wins)
	if err != nil {
		return ""
	}
	return string(data)
}

// windowListing matches the JSON shape ax-tree-dump expects from
// ListWindowsByBundleJSON. Kept minimal: macOS emits role/frame/treeBytes
// too, but on Windows we ship only what is cheap to derive without a UIA
// walk (one walk per window would be expensive).
type windowListing struct {
	Index   int    `json:"index"`
	HWND    int64  `json:"hwnd"`
	Title   string `json:"title"`
	Visible bool   `json:"visible"`
}

// --- HWND enumeration helpers (Win32) -----------------------------------

var (
	procEnumWindows      = user32.NewProc("EnumWindows")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procGetWindow        = user32.NewProc("GetWindow")
	procGetWindowLongPtr = func() *syscall.LazyProc {
		// GetWindowLongPtrW exists only on 64-bit; on 32-bit it aliases to
		// GetWindowLongW. wails-gui ships 64-bit only, so we resolve the
		// 64-bit name directly.
		return user32.NewProc("GetWindowLongPtrW")
	}()
)

const (
	gwOwner = 4
	// gwlExStyle is GWL_EXSTYLE = -20 expressed as the two's-complement bit
	// pattern of int32. We pass it via uintptr to GetWindowLongPtrW; Win32
	// truncates the parameter to its `int nIndex` width, so the high bits
	// are discarded and the call sees -20 as documented. Go forbids
	// converting negative constants directly to uintptr.
	gwlExStyle     uint32 = 0xFFFFFFEC
	wsExAppWin            = 0x00040000
	wsExNoActivate        = 0x08000000
	wsExToolWindow        = 0x00000080
)

// findTopLevelWindowForBundle walks all top-level windows and returns the
// HWND of the `index`-th visible, app-style window whose owning process's
// exe basename matches `bundleID` (case-insensitive). Returns 0 when no
// match is found.
func findTopLevelWindowForBundle(bundleID string, index int) uintptr {
	wins := enumerateTopLevelWindowsForBundle(bundleID)
	if index < 0 || index >= len(wins) {
		return 0
	}
	return uintptr(wins[index].HWND)
}

// enumWindowsState carries the per-call query context through the
// EnumWindows callback's lParam. We cannot close over locals in the
// callback because the callback itself MUST be allocated once at
// package level (see enumWindowsCallbackOnce below).
type enumWindowsState struct {
	target string
	out    []windowListing
}

// enumWindowsCallback is the single, persistent EnumWindowsProc we
// hand to user32!EnumWindows. Go's syscall.NewCallback allocates from a
// fixed-size pool (~2000 slots on 64-bit Windows) and NEVER releases —
// creating a fresh callback per call would crash the process with
// "runtime: callbacks limit reached" within a few thousand invocations
// of the bundle-targeted dump path (CollectWindowTreeByBundle /
// ListWindowsByBundleJSON). The per-call state is threaded through the
// lParam argument as a pointer to an enumWindowsState.
var (
	enumWindowsCallback     uintptr
	enumWindowsCallbackOnce sync.Once
)

func enumWindowsProc(hwnd uintptr, lparam uintptr) uintptr {
	state := (*enumWindowsState)(unsafe.Pointer(lparam))
	if !isAppLikeWindow(hwnd) {
		return 1
	}
	ownerBundle := bundleIDForWindow(hwnd)
	if ownerBundle == "" || ownerBundle != state.target {
		return 1
	}
	buf := make([]uint16, 512)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	title := syscall.UTF16ToString(buf)
	state.out = append(state.out, windowListing{
		Index:   len(state.out),
		HWND:    int64(hwnd),
		Title:   title,
		Visible: true,
	})
	return 1
}

func ensureEnumWindowsCallback() uintptr {
	enumWindowsCallbackOnce.Do(func() {
		enumWindowsCallback = syscall.NewCallback(enumWindowsProc)
	})
	return enumWindowsCallback
}

func enumerateTopLevelWindowsForBundle(bundleID string) []windowListing {
	if bundleID == "" {
		return nil
	}
	state := enumWindowsState{target: normalizeBundle(bundleID)}
	procEnumWindows.Call(ensureEnumWindowsCallback(), uintptr(unsafe.Pointer(&state)))
	return state.out
}

func bundleIDForWindow(hwnd uintptr) string {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	hProc, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if hProc == 0 {
		return ""
	}
	defer procCloseHandle.Call(hProc)
	exePath := queryProcessImageName(hProc)
	if exePath == "" {
		return ""
	}
	return normalizeBundle(baseNameWithExt(exePath))
}

func normalizeBundle(s string) string {
	// Match the lowercase normalization FrontmostAppBundleID does.
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func isAppLikeWindow(hwnd uintptr) bool {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return false
	}
	// Skip windows owned by another window (tooltips, transient dialogs).
	owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
	if owner != 0 {
		// Owned windows can still be primary (Explorer's quick-access
		// host) — accept them when they're explicitly tagged WS_EX_APPWINDOW.
		ex, _, _ := procGetWindowLongPtr.Call(hwnd, uintptr(gwlExStyle))
		if int64(ex)&int64(wsExAppWin) == 0 {
			return false
		}
	}
	ex, _, _ := procGetWindowLongPtr.Call(hwnd, uintptr(gwlExStyle))
	if int64(ex)&int64(wsExToolWindow) != 0 {
		return false
	}
	return true
}
