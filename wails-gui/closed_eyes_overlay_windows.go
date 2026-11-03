//go:build windows

// Windows implementation of the closed-eyes overlay. Mirrors the macOS
// behaviour in closed_eyes_overlay_darwin.go using pure Win32 syscalls
// — no extra dependencies, same style as quick_chat_hotkey_windows.go
// and internal/platform/activitymon/capture_windows.go.
//
// Two visible pieces, both layered top-most tool windows that never
// activate or steal focus:
//
//   1. A click-through dashed-yellow border around every monitor.
//      Animated "marching ants" via a 280 ms tick that bumps a global
//      phase and invalidates each border window.
//
//   2. A small interactive capsule at the top center of the primary
//      monitor. Hovering expands it; clicking the button disables
//      closed-eyes mode via the same app.setClosedEyesModeEnabled(false)
//      path the macOS callback uses.
//
// All Win32 windows are owned by a dedicated goroutine that LockOSThreads
// itself and runs a GetMessage pump. Show/hide are PostThreadMessageW
// commands so the Go API stays trivially callable from any goroutine.

package main

import (
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"wails-gui/internal/platform/activitymon"
)

// ── Win32 bindings ───────────────────────────────────────────────────

var (
	overlayUser32   = syscall.NewLazyDLL("user32.dll")
	overlayGdi32    = syscall.NewLazyDLL("gdi32.dll")
	overlayKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOverlayRegisterClassExW      = overlayUser32.NewProc("RegisterClassExW")
	procOverlayCreateWindowExW       = overlayUser32.NewProc("CreateWindowExW")
	procOverlayDestroyWindow         = overlayUser32.NewProc("DestroyWindow")
	procOverlayDefWindowProcW        = overlayUser32.NewProc("DefWindowProcW")
	procOverlayShowWindow            = overlayUser32.NewProc("ShowWindow")
	procOverlaySetLayeredWindowAttrs = overlayUser32.NewProc("SetLayeredWindowAttributes")
	procOverlaySetWindowPos          = overlayUser32.NewProc("SetWindowPos")
	procOverlaySetWindowRgn          = overlayUser32.NewProc("SetWindowRgn")
	procOverlayGetClientRect         = overlayUser32.NewProc("GetClientRect")
	procOverlayInvalidateRect        = overlayUser32.NewProc("InvalidateRect")
	procOverlayBeginPaint            = overlayUser32.NewProc("BeginPaint")
	procOverlayEndPaint              = overlayUser32.NewProc("EndPaint")
	procOverlayFillRect              = overlayUser32.NewProc("FillRect")
	procOverlayGetSystemMetrics      = overlayUser32.NewProc("GetSystemMetrics")
	procOverlayEnumDisplayMonitors   = overlayUser32.NewProc("EnumDisplayMonitors")
	procOverlayGetMonitorInfoW       = overlayUser32.NewProc("GetMonitorInfoW")
	procOverlaySetTimer              = overlayUser32.NewProc("SetTimer")
	procOverlayKillTimer             = overlayUser32.NewProc("KillTimer")
	procOverlayGetMessageW           = overlayUser32.NewProc("GetMessageW")
	procOverlayPeekMessageW          = overlayUser32.NewProc("PeekMessageW")
	procOverlayTranslateMessage      = overlayUser32.NewProc("TranslateMessage")
	procOverlayDispatchMessageW      = overlayUser32.NewProc("DispatchMessageW")
	procOverlayPostThreadMessageW    = overlayUser32.NewProc("PostThreadMessageW")
	procOverlayLoadCursorW           = overlayUser32.NewProc("LoadCursorW")
	procOverlayTrackMouseEvent       = overlayUser32.NewProc("TrackMouseEvent")
	procOverlayUpdateWindow          = overlayUser32.NewProc("UpdateWindow")

	procOverlayGetCurrentThreadID = overlayKernel32.NewProc("GetCurrentThreadId")
	procOverlayGetModuleHandleW   = overlayKernel32.NewProc("GetModuleHandleW")

	procOverlayCreatePen          = overlayGdi32.NewProc("CreatePen")
	procOverlayCreateSolidBrush   = overlayGdi32.NewProc("CreateSolidBrush")
	procOverlayCreateFontW        = overlayGdi32.NewProc("CreateFontW")
	procOverlayCreateRoundRectRgn = overlayGdi32.NewProc("CreateRoundRectRgn")
	procOverlaySelectObject       = overlayGdi32.NewProc("SelectObject")
	procOverlayDeleteObject       = overlayGdi32.NewProc("DeleteObject")
	procOverlayMoveToEx           = overlayGdi32.NewProc("MoveToEx")
	procOverlayLineTo             = overlayGdi32.NewProc("LineTo")
	procOverlayRoundRect          = overlayGdi32.NewProc("RoundRect")
	procOverlaySetBkMode          = overlayGdi32.NewProc("SetBkMode")
	procOverlaySetTextColor       = overlayGdi32.NewProc("SetTextColor")
	procOverlayDrawTextW          = overlayUser32.NewProc("DrawTextW")
	procOverlayGetStockObject     = overlayGdi32.NewProc("GetStockObject")
)

// ── Win32 constants ──────────────────────────────────────────────────

const (
	wmOverlayDestroy       = 0x0002
	wmOverlayPaint         = 0x000F
	wmOverlayTimer         = 0x0113
	wmOverlayMouseMove     = 0x0200
	wmOverlayLButtonUp     = 0x0202
	wmOverlayMouseLeave    = 0x02A3
	wmOverlayDisplayChange = 0x007E
	wmOverlaySetCursor     = 0x0020
	wmOverlayNcHitTest     = 0x0084
	wmOverlayMouseActivate = 0x0021

	wmOverlayUser        = 0x0400
	wmOverlayUserShow    = wmOverlayUser + 1
	wmOverlayUserHide    = wmOverlayUser + 2
	wmOverlayUserRebuild = wmOverlayUser + 3

	wsOverlayPopup   = 0x80000000
	wsOverlayVisible = 0x10000000

	wsExOverlayLayered     = 0x00080000
	wsExOverlayTransparent = 0x00000020
	wsExOverlayTopmost     = 0x00000008
	wsExOverlayToolwindow  = 0x00000080
	wsExOverlayNoactivate  = 0x08000000

	lwaOverlayColorKey = 0x00000001

	swpOverlayNoSize     = 0x0001
	swpOverlayNoMove     = 0x0002
	swpOverlayNoZorder   = 0x0004
	swpOverlayNoActivate = 0x0010
	swpOverlayShowWindow = 0x0040

	swOverlayHide   = 0
	swOverlayShowNA = 8

	psOverlaySolid = 0

	transparentMode = 1

	idcOverlayArrow = 32512
	idcOverlayHand  = 32649

	dtCenter     = 0x00000001
	dtVCenter    = 0x00000004
	dtSingleLine = 0x00000020
	dtWordBreak  = 0x00000010
	dtNoClip     = 0x00000100
	dtLeft       = 0x00000000

	tmeLeave = 0x00000002

	maOverlayNoActivate = 3

	hwndOverlayTopmost = ^uintptr(0) // (HWND)-1
)

// ── Win32 structs ────────────────────────────────────────────────────

type overlayRect struct {
	Left, Top, Right, Bottom int32
}

type overlayPoint struct {
	X, Y int32
}

type overlayMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      overlayPoint
}

type overlayWndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type overlayPaintStruct struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     overlayRect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type overlayMonitorInfo struct {
	CbSize    uint32
	RcMonitor overlayRect
	RcWork    overlayRect
	DwFlags   uint32
}

type overlayTrackMouseEvent struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
}

type overlayLogfont struct {
	LfHeight         int32
	LfWidth          int32
	LfEscapement     int32
	LfOrientation    int32
	LfWeight         int32
	LfItalic         byte
	LfUnderline      byte
	LfStrikeOut      byte
	LfCharSet        byte
	LfOutPrecision   byte
	LfClipPrecision  byte
	LfQuality        byte
	LfPitchAndFamily byte
	LfFaceName       [32]uint16
}

// ── Visual constants ─────────────────────────────────────────────────

const (
	overlayBorderInset    = 10
	overlayBorderStroke   = 4
	overlayDashOnLen      = 18
	overlayDashOffLen     = 12
	overlayDashCycle      = overlayDashOnLen + overlayDashOffLen
	overlayDashPhaseStep  = 4
	overlayTickIntervalMs = 280

	overlayCapsuleH         = 64
	overlayCapsuleExpandedH = 124
	overlayCapsuleEnColW    = 310
	overlayCapsuleEnExpW    = 620
	overlayCapsuleZhColW    = 246
	overlayCapsuleZhExpW    = 430
	overlayCapsuleRadius    = 24
	overlayCapsuleTopInset  = 2

	overlayClassBorder  = "YansuClosedEyesBorder"
	overlayClassCapsule = "YansuClosedEyesCapsule"
)

func overlayRGB(r, g, b uint32) uintptr {
	return uintptr(r | (g << 8) | (b << 16))
}

var (
	overlayKeyColor      = overlayRGB(0, 0, 0)
	overlayYellow        = overlayRGB(255, 210, 28)
	overlayCapsuleBg     = overlayRGB(28, 24, 18)
	overlayCapsuleBorder = overlayRGB(255, 210, 28)
	overlayTextWhite     = overlayRGB(255, 255, 255)
	overlayTextDim       = overlayRGB(216, 208, 196)
	overlayButtonText    = overlayRGB(27, 22, 6)
)

// ── State ────────────────────────────────────────────────────────────

var closedEyesOverlayApp atomic.Pointer[App]

type closedEyesOverlayState struct {
	mu sync.Mutex

	threadID     uint32
	pumpStarted  bool
	classesReady bool

	visible bool

	borderWindows []uintptr
	capsuleWindow uintptr
	timerID       uintptr
	phase         int32

	capsuleExpanded bool
	capsuleHovering bool
	capsuleTracking bool
	capsuleCenterX  int32
	capsuleTopY     int32
	capsuleClipW    int32
	capsuleLang     string

	collected []overlayMonitorInfo
}

var overlayInst = &closedEyesOverlayState{}

// ── Public Go API (called from closed_eyes_mode.go) ──────────────────

func showClosedEyesOverlay(app *App) {
	if activitymon.IsScreenLocked() {
		log.Printf("[closed-eyes-win] screen locked, skipping overlay window creation")
		return
	}

	lang := "en"
	if app != nil {
		lang = app.currentUILanguage()
	}

	overlayInst.mu.Lock()
	overlayInst.capsuleLang = lang
	overlayInst.mu.Unlock()

	closedEyesOverlayApp.Store(app)
	overlayInst.ensurePumpStarted()
	overlayInst.post(wmOverlayUserShow)
}

func hideClosedEyesOverlay() {
	overlayInst.post(wmOverlayUserHide)
	closedEyesOverlayApp.Store(nil)
}

// ── Pump goroutine lifecycle ─────────────────────────────────────────

func (s *closedEyesOverlayState) ensurePumpStarted() {
	s.mu.Lock()
	if s.pumpStarted {
		s.mu.Unlock()
		return
	}
	s.pumpStarted = true
	s.mu.Unlock()

	ready := make(chan struct{})
	go s.pumpLoop(ready)
	<-ready
}

func (s *closedEyesOverlayState) post(message uint32) {
	// The pump records its thread ID before signaling ready, so by the time
	// this function is reachable after ensurePumpStarted, threadID is set.
	// hideClosedEyesOverlay may be called without a prior show — in that
	// case the pump may not have started, and there's nothing to hide.
	s.mu.Lock()
	tid := s.threadID
	s.mu.Unlock()
	if tid == 0 {
		return
	}
	procOverlayPostThreadMessageW.Call(uintptr(tid), uintptr(message), 0, 0)
}

func (s *closedEyesOverlayState) pumpLoop(ready chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tid, _, _ := procOverlayGetCurrentThreadID.Call()
	s.mu.Lock()
	s.threadID = uint32(tid)
	s.mu.Unlock()

	// Force allocation of the thread message queue before anyone posts.
	var probe overlayMsg
	procOverlayPeekMessageW.Call(uintptr(unsafe.Pointer(&probe)), 0, 0, 0, 0)

	s.registerClasses()
	close(ready)

	var msg overlayMsg
	for {
		ret, _, _ := procOverlayGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		if msg.Hwnd == 0 {
			switch msg.Message {
			case wmOverlayUserShow:
				s.handleShow()
			case wmOverlayUserHide:
				s.handleHide()
			case wmOverlayUserRebuild:
				s.handleRebuild()
			case wmOverlayTimer:
				s.handleTick()
			}
			continue
		}
		procOverlayTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procOverlayDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// ── Window class registration ────────────────────────────────────────
//
// The wndproc callbacks are populated lazily inside registerClasses
// rather than at package init. Initializing them at package level would
// pull the static analyzer through this graph:
//   overlayCapsuleWndProcGo → triggerDisableFromOverlay
//     → setClosedEyesModeEnabled → showClosedEyesOverlay
//     → ensurePumpStarted → pumpLoop → registerClasses
//     → reads overlayCapsuleWndProc
// which is a self-referential init cycle. Lazy assignment breaks it
// and matches the once-per-process behaviour we actually want anyway
// (syscall.NewCallback consumes a slot in a small fixed table).

var (
	overlayBorderWndProc  uintptr
	overlayCapsuleWndProc uintptr
	overlayEnumMonProc    uintptr
)

func (s *closedEyesOverlayState) registerClasses() {
	if s.classesReady {
		return
	}
	overlayBorderWndProc = syscall.NewCallback(overlayBorderWndProcGo)
	overlayCapsuleWndProc = syscall.NewCallback(overlayCapsuleWndProcGo)
	overlayEnumMonProc = syscall.NewCallback(overlayEnumMonitorsProc)

	hInst, _, _ := procOverlayGetModuleHandleW.Call(0)
	arrowCursor, _, _ := procOverlayLoadCursorW.Call(0, idcOverlayArrow)
	handCursor, _, _ := procOverlayLoadCursorW.Call(0, idcOverlayHand)
	_ = handCursor

	registerOne := func(name string, proc uintptr, cursor uintptr) {
		wname, _ := syscall.UTF16PtrFromString(name)
		cls := overlayWndClassExW{
			CbSize:        uint32(unsafe.Sizeof(overlayWndClassExW{})),
			Style:         0,
			LpfnWndProc:   proc,
			HInstance:     hInst,
			HCursor:       cursor,
			HbrBackground: 0,
			LpszClassName: wname,
		}
		cls.CbSize = uint32(unsafe.Sizeof(cls))
		procOverlayRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls)))
	}

	registerOne(overlayClassBorder, overlayBorderWndProc, arrowCursor)
	registerOne(overlayClassCapsule, overlayCapsuleWndProc, handCursor)

	s.classesReady = true
}

// ── show / hide / rebuild handlers ───────────────────────────────────

func (s *closedEyesOverlayState) handleShow() {
	if s.visible {
		s.handleRebuild()
		return
	}
	s.visible = true
	s.createWindows()
	s.startTimer()
}

func (s *closedEyesOverlayState) handleHide() {
	if !s.visible {
		return
	}
	s.visible = false
	s.stopTimer()
	s.destroyAllWindows()
}

func (s *closedEyesOverlayState) handleRebuild() {
	if !s.visible {
		return
	}
	s.destroyAllWindows()
	s.createWindows()
}

func (s *closedEyesOverlayState) handleTick() {
	atomic.AddInt32(&s.phase, overlayDashPhaseStep)
	if atomic.LoadInt32(&s.phase) >= overlayDashCycle {
		atomic.StoreInt32(&s.phase, atomic.LoadInt32(&s.phase)%overlayDashCycle)
	}
	for _, hwnd := range s.borderWindows {
		procOverlayInvalidateRect.Call(hwnd, 0, 0)
	}
}

func (s *closedEyesOverlayState) startTimer() {
	if s.timerID != 0 {
		return
	}
	id, _, _ := procOverlaySetTimer.Call(0, 0, overlayTickIntervalMs, 0)
	s.timerID = id
}

func (s *closedEyesOverlayState) stopTimer() {
	if s.timerID == 0 {
		return
	}
	procOverlayKillTimer.Call(0, s.timerID)
	s.timerID = 0
}

func (s *closedEyesOverlayState) destroyAllWindows() {
	for _, hwnd := range s.borderWindows {
		procOverlayDestroyWindow.Call(hwnd)
	}
	s.borderWindows = s.borderWindows[:0]
	if s.capsuleWindow != 0 {
		procOverlayDestroyWindow.Call(s.capsuleWindow)
		s.capsuleWindow = 0
	}
	s.capsuleExpanded = false
	s.capsuleHovering = false
	s.capsuleTracking = false
}

// ── Monitor enumeration ──────────────────────────────────────────────

func overlayEnumMonitorsProc(hMonitor, hdc, lprc, dwData uintptr) uintptr {
	_ = hdc
	_ = lprc
	_ = dwData
	var mi overlayMonitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := procOverlayGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi))); r != 0 {
		overlayInst.collected = append(overlayInst.collected, mi)
	}
	return 1
}

func (s *closedEyesOverlayState) enumerateMonitors() []overlayMonitorInfo {
	s.collected = s.collected[:0]
	procOverlayEnumDisplayMonitors.Call(0, 0, overlayEnumMonProc, 0)
	out := make([]overlayMonitorInfo, len(s.collected))
	copy(out, s.collected)
	s.collected = s.collected[:0]
	return out
}

// ── Window creation ──────────────────────────────────────────────────

func (s *closedEyesOverlayState) createWindows() {
	monitors := s.enumerateMonitors()
	if len(monitors) == 0 {
		log.Printf("[closed-eyes-win] no monitors enumerated, skipping overlay")
		return
	}

	hInst, _, _ := procOverlayGetModuleHandleW.Call(0)
	classBorderW, _ := syscall.UTF16PtrFromString(overlayClassBorder)
	emptyW, _ := syscall.UTF16PtrFromString("")

	var primary *overlayMonitorInfo
	for i := range monitors {
		mi := &monitors[i]
		rc := mi.RcMonitor
		w := rc.Right - rc.Left
		h := rc.Bottom - rc.Top

		hwnd, _, _ := procOverlayCreateWindowExW.Call(
			wsExOverlayLayered|wsExOverlayTransparent|wsExOverlayTopmost|wsExOverlayToolwindow|wsExOverlayNoactivate,
			uintptr(unsafe.Pointer(classBorderW)),
			uintptr(unsafe.Pointer(emptyW)),
			wsOverlayPopup,
			uintptr(rc.Left), uintptr(rc.Top),
			uintptr(w), uintptr(h),
			0, 0, hInst, 0,
		)
		if hwnd == 0 {
			log.Printf("[closed-eyes-win] CreateWindowExW for border failed")
			continue
		}
		procOverlaySetLayeredWindowAttrs.Call(hwnd, overlayKeyColor, 0, lwaOverlayColorKey)
		procOverlaySetWindowPos.Call(
			hwnd, hwndOverlayTopmost, 0, 0, 0, 0,
			swpOverlayNoMove|swpOverlayNoSize|swpOverlayNoActivate|swpOverlayShowWindow,
		)
		procOverlayShowWindow.Call(hwnd, swOverlayShowNA)
		s.borderWindows = append(s.borderWindows, hwnd)

		if mi.DwFlags&0x1 != 0 || primary == nil { // MONITORINFOF_PRIMARY = 0x1
			primary = mi
		}
	}

	if primary != nil {
		s.createCapsule(primary)
	}
}

func (s *closedEyesOverlayState) capsuleSizesForLang() (colW, colH, expW, expH int32) {
	if isChineseLanguage(s.capsuleLang) {
		return overlayCapsuleZhColW, overlayCapsuleH, overlayCapsuleZhExpW, overlayCapsuleExpandedH
	}
	return overlayCapsuleEnColW, overlayCapsuleH, overlayCapsuleEnExpW, overlayCapsuleExpandedH
}

func (s *closedEyesOverlayState) createCapsule(primary *overlayMonitorInfo) {
	hInst, _, _ := procOverlayGetModuleHandleW.Call(0)
	classCapsuleW, _ := syscall.UTF16PtrFromString(overlayClassCapsule)
	emptyW, _ := syscall.UTF16PtrFromString("")

	rc := primary.RcMonitor
	colW, colH, _, _ := s.capsuleSizesForLang()
	maxW := (rc.Right - rc.Left) - 48
	if colW > maxW {
		colW = maxW
	}
	s.capsuleClipW = (rc.Right - rc.Left) - 48
	s.capsuleCenterX = (rc.Left + rc.Right) / 2
	s.capsuleTopY = rc.Top + overlayCapsuleTopInset

	x := s.capsuleCenterX - colW/2
	y := s.capsuleTopY

	hwnd, _, _ := procOverlayCreateWindowExW.Call(
		wsExOverlayTopmost|wsExOverlayToolwindow|wsExOverlayNoactivate,
		uintptr(unsafe.Pointer(classCapsuleW)),
		uintptr(unsafe.Pointer(emptyW)),
		wsOverlayPopup,
		uintptr(x), uintptr(y),
		uintptr(colW), uintptr(colH),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		log.Printf("[closed-eyes-win] CreateWindowExW for capsule failed")
		return
	}
	s.capsuleWindow = hwnd
	s.applyCapsuleRgn(hwnd, colW, colH)
	procOverlaySetWindowPos.Call(
		hwnd, hwndOverlayTopmost, 0, 0, 0, 0,
		swpOverlayNoMove|swpOverlayNoSize|swpOverlayNoActivate|swpOverlayShowWindow,
	)
	procOverlayShowWindow.Call(hwnd, swOverlayShowNA)
}

func (s *closedEyesOverlayState) applyCapsuleRgn(hwnd uintptr, w, h int32) {
	rgn, _, _ := procOverlayCreateRoundRectRgn.Call(
		0, 0, uintptr(w), uintptr(h),
		overlayCapsuleRadius*2, overlayCapsuleRadius*2,
	)
	procOverlaySetWindowRgn.Call(hwnd, rgn, 1)
}

func (s *closedEyesOverlayState) setCapsuleExpanded(expanded bool) {
	if s.capsuleWindow == 0 || s.capsuleExpanded == expanded {
		return
	}
	s.capsuleExpanded = expanded
	colW, colH, expW, expH := s.capsuleSizesForLang()
	var w, h int32
	if expanded {
		w, h = expW, expH
	} else {
		w, h = colW, colH
	}
	if w > s.capsuleClipW {
		w = s.capsuleClipW
	}
	x := s.capsuleCenterX - w/2
	y := s.capsuleTopY
	procOverlaySetWindowPos.Call(
		s.capsuleWindow, 0,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		swpOverlayNoZorder|swpOverlayNoActivate,
	)
	s.applyCapsuleRgn(s.capsuleWindow, w, h)
	procOverlayInvalidateRect.Call(s.capsuleWindow, 0, 1)
}

// ── Border window proc ───────────────────────────────────────────────

func overlayBorderWndProcGo(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmOverlayPaint:
		paintBorder(hwnd)
		return 0
	case wmOverlayDisplayChange:
		overlayInst.post(wmOverlayUserRebuild)
		return 0
	case wmOverlayMouseActivate:
		return maOverlayNoActivate
	case wmOverlayDestroy:
		return 0
	}
	ret, _, _ := procOverlayDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return ret
}

func paintBorder(hwnd uintptr) {
	var ps overlayPaintStruct
	hdc, _, _ := procOverlayBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	defer procOverlayEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc overlayRect
	procOverlayGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))

	// Fill with the color-key (transparent) brush so everything outside the
	// border stays see-through.
	keyBrush, _, _ := procOverlayCreateSolidBrush.Call(overlayKeyColor)
	defer procOverlayDeleteObject.Call(keyBrush)
	procOverlayFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), keyBrush)

	pen, _, _ := procOverlayCreatePen.Call(psOverlaySolid, overlayBorderStroke, overlayYellow)
	defer procOverlayDeleteObject.Call(pen)
	prevPen, _, _ := procOverlaySelectObject.Call(hdc, pen)
	defer procOverlaySelectObject.Call(hdc, prevPen)

	inset := int32(overlayBorderInset)
	x0 := rc.Left + inset
	y0 := rc.Top + inset
	x1 := rc.Right - inset
	y1 := rc.Bottom - inset
	if x1 <= x0 || y1 <= y0 {
		return
	}

	phase := atomic.LoadInt32(&overlayInst.phase)
	drawDashedSide(hdc, x0, y0, x1, y0, phase) // top L→R
	drawDashedSide(hdc, x1, y0, x1, y1, phase) // right T→B
	drawDashedSide(hdc, x1, y1, x0, y1, phase) // bottom R→L
	drawDashedSide(hdc, x0, y1, x0, y0, phase) // left B→T
}

func drawDashedSide(hdc uintptr, sx, sy, ex, ey, phase int32) {
	dx := signOf(ex - sx)
	dy := signOf(ey - sy)
	length := absInt32(ex-sx) + absInt32(ey-sy)
	if length <= 0 {
		return
	}

	s := -phase
	for s < length {
		dashStart := s
		dashEnd := s + overlayDashOnLen
		if dashStart < 0 {
			dashStart = 0
		}
		if dashEnd > length {
			dashEnd = length
		}
		if dashStart < dashEnd {
			px0 := sx + dx*dashStart
			py0 := sy + dy*dashStart
			px1 := sx + dx*dashEnd
			py1 := sy + dy*dashEnd
			procOverlayMoveToEx.Call(hdc, uintptr(px0), uintptr(py0), 0)
			procOverlayLineTo.Call(hdc, uintptr(px1), uintptr(py1))
		}
		s += overlayDashCycle
	}
}

func signOf(v int32) int32 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// ── Capsule window proc ──────────────────────────────────────────────

func overlayCapsuleWndProcGo(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmOverlayPaint:
		paintCapsule(hwnd)
		return 0

	case wmOverlayMouseMove:
		if !overlayInst.capsuleTracking {
			tme := overlayTrackMouseEvent{
				CbSize:    uint32(unsafe.Sizeof(overlayTrackMouseEvent{})),
				DwFlags:   tmeLeave,
				HwndTrack: hwnd,
			}
			procOverlayTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			overlayInst.capsuleTracking = true
		}
		if !overlayInst.capsuleHovering {
			overlayInst.capsuleHovering = true
			overlayInst.setCapsuleExpanded(true)
		}
		return 0

	case wmOverlayMouseLeave:
		overlayInst.capsuleTracking = false
		overlayInst.capsuleHovering = false
		overlayInst.setCapsuleExpanded(false)
		return 0

	case wmOverlayLButtonUp:
		if overlayInst.capsuleExpanded && pointInCapsuleButton(hwnd, lparam) {
			triggerDisableFromOverlay()
		} else {
			// Click on the collapsed pill or anywhere outside the button:
			// expand to surface the button without dismissing.
			overlayInst.setCapsuleExpanded(true)
		}
		return 0

	case wmOverlayMouseActivate:
		return maOverlayNoActivate

	case wmOverlayDestroy:
		return 0
	}
	ret, _, _ := procOverlayDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return ret
}

// pointInCapsuleButton checks whether a click landed inside the
// "Resume observation" button. The button rect is recomputed
// here so it stays in sync with paintCapsule.
func pointInCapsuleButton(hwnd, lparam uintptr) bool {
	x := int32(int16(lparam & 0xFFFF))
	y := int32(int16((lparam >> 16) & 0xFFFF))
	var rc overlayRect
	procOverlayGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	br := capsuleButtonRect(rc)
	return x >= br.Left && x < br.Right && y >= br.Top && y < br.Bottom
}

func triggerDisableFromOverlay() {
	app := closedEyesOverlayApp.Load()
	if app == nil {
		return
	}
	go func() {
		if _, err := app.setClosedEyesModeEnabled(false); err != nil {
			log.Printf("[closed-eyes-win] disable from overlay failed: %v", err)
			return
		}
		log.Printf("[closed-eyes-win] disabled from overlay capsule click")
	}()
}

// ── Capsule paint ────────────────────────────────────────────────────

func capsuleButtonRect(rc overlayRect) overlayRect {
	// Bottom-right area when expanded. We position the button on the
	// lower row, aligned to the right with a 14px inset.
	w := rc.Right - rc.Left
	h := rc.Bottom - rc.Top
	if h <= overlayCapsuleH {
		return overlayRect{Left: 0, Top: 0, Right: 0, Bottom: 0}
	}
	buttonW := int32(220)
	if isChineseLanguage(overlayInst.capsuleLang) {
		buttonW = 140
	}
	if buttonW > w-32 {
		buttonW = w - 32
	}
	buttonH := int32(34)
	buttonRight := rc.Right - 14
	buttonLeft := buttonRight - buttonW
	buttonTop := rc.Bottom - buttonH - 14
	buttonBottom := rc.Bottom - 14
	return overlayRect{Left: buttonLeft, Top: buttonTop, Right: buttonRight, Bottom: buttonBottom}
}

func paintCapsule(hwnd uintptr) {
	var ps overlayPaintStruct
	hdc, _, _ := procOverlayBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	defer procOverlayEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc overlayRect
	procOverlayGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))

	// Solid dark fill (the rounded-region clip handles the corners).
	bg, _, _ := procOverlayCreateSolidBrush.Call(overlayCapsuleBg)
	defer procOverlayDeleteObject.Call(bg)
	procOverlayFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), bg)

	// Yellow 1px-ish ring around the capsule edge.
	ringPen, _, _ := procOverlayCreatePen.Call(psOverlaySolid, 2, overlayCapsuleBorder)
	defer procOverlayDeleteObject.Call(ringPen)
	prevPen, _, _ := procOverlaySelectObject.Call(hdc, ringPen)
	// We need a NULL brush so RoundRect strokes the outline without filling
	// over our dark background.
	nullBrush, _, _ := procOverlayGetStockObject.Call(5) // NULL_BRUSH = 5
	prevBrush, _, _ := procOverlaySelectObject.Call(hdc, nullBrush)
	procOverlayRoundRect.Call(
		hdc,
		uintptr(rc.Left), uintptr(rc.Top), uintptr(rc.Right), uintptr(rc.Bottom),
		overlayCapsuleRadius*2, overlayCapsuleRadius*2,
	)
	procOverlaySelectObject.Call(hdc, prevPen)
	procOverlaySelectObject.Call(hdc, prevBrush)

	procOverlaySetBkMode.Call(hdc, transparentMode)

	zh := isChineseLanguage(overlayInst.capsuleLang)
	titleStr := capsuleText("title", zh)
	messageStr := capsuleText("message", zh)
	buttonStr := capsuleText("button", zh)

	// ── Status row (always visible) ───────────────────────────────
	// Layout: [yellow dot 12x12] [title text] ............ [pulse dot]
	dotSize := int32(10)
	dotInsetL := int32(16)
	dotInsetR := int32(16)
	dotY := int32(overlayCapsuleH/2) - dotSize/2

	dotBrush, _, _ := procOverlayCreateSolidBrush.Call(overlayYellow)
	defer procOverlayDeleteObject.Call(dotBrush)

	// Left mascot dot (stands in for the macOS Lottie 🙈).
	drawFilledCircle(hdc, dotInsetL+dotSize/2, dotY+dotSize/2, dotSize/2+4, dotBrush)

	// Right pulse dot.
	pulseR := dotSize / 2
	drawFilledCircle(hdc, rc.Right-dotInsetR-pulseR, dotY+dotSize/2, pulseR, dotBrush)

	// Title text between the two dots.
	titleFont := makeFont(15, 600)
	defer procOverlayDeleteObject.Call(titleFont)
	prevTitleFont, _, _ := procOverlaySelectObject.Call(hdc, titleFont)
	procOverlaySetTextColor.Call(hdc, overlayTextWhite)

	titleRect := overlayRect{
		Left:   dotInsetL + dotSize + 12,
		Top:    0,
		Right:  rc.Right - dotInsetR - dotSize - 12,
		Bottom: overlayCapsuleH,
	}
	wTitle, _ := syscall.UTF16PtrFromString(titleStr)
	procOverlayDrawTextW.Call(
		hdc, uintptr(unsafe.Pointer(wTitle)), ^uintptr(0),
		uintptr(unsafe.Pointer(&titleRect)),
		dtSingleLine|dtVCenter|dtLeft|dtNoClip,
	)
	procOverlaySelectObject.Call(hdc, prevTitleFont)

	// ── Expanded extras ───────────────────────────────────────────
	if rc.Bottom-rc.Top > overlayCapsuleH {
		// Message line, two-row layout below the status row.
		msgFont := makeFont(13, 400)
		defer procOverlayDeleteObject.Call(msgFont)
		prevMsgFont, _, _ := procOverlaySelectObject.Call(hdc, msgFont)
		procOverlaySetTextColor.Call(hdc, overlayTextDim)

		msgRect := overlayRect{
			Left:   16,
			Top:    overlayCapsuleH,
			Right:  capsuleButtonRect(rc).Left - 12,
			Bottom: rc.Bottom - 8,
		}
		wMsg, _ := syscall.UTF16PtrFromString(messageStr)
		procOverlayDrawTextW.Call(
			hdc, uintptr(unsafe.Pointer(wMsg)), ^uintptr(0),
			uintptr(unsafe.Pointer(&msgRect)),
			dtWordBreak|dtLeft|dtVCenter,
		)
		procOverlaySelectObject.Call(hdc, prevMsgFont)

		// Button.
		btn := capsuleButtonRect(rc)
		btnBg, _, _ := procOverlayCreateSolidBrush.Call(overlayYellow)
		defer procOverlayDeleteObject.Call(btnBg)
		btnPen, _, _ := procOverlayCreatePen.Call(psOverlaySolid, 1, overlayYellow)
		defer procOverlayDeleteObject.Call(btnPen)
		prevBtnPen, _, _ := procOverlaySelectObject.Call(hdc, btnPen)
		prevBtnBrush, _, _ := procOverlaySelectObject.Call(hdc, btnBg)
		procOverlayRoundRect.Call(
			hdc,
			uintptr(btn.Left), uintptr(btn.Top),
			uintptr(btn.Right), uintptr(btn.Bottom),
			28, 28,
		)
		procOverlaySelectObject.Call(hdc, prevBtnPen)
		procOverlaySelectObject.Call(hdc, prevBtnBrush)

		btnFont := makeFont(13, 700)
		defer procOverlayDeleteObject.Call(btnFont)
		prevBtnFont, _, _ := procOverlaySelectObject.Call(hdc, btnFont)
		procOverlaySetTextColor.Call(hdc, overlayButtonText)
		wBtn, _ := syscall.UTF16PtrFromString(buttonStr)
		procOverlayDrawTextW.Call(
			hdc, uintptr(unsafe.Pointer(wBtn)), ^uintptr(0),
			uintptr(unsafe.Pointer(&btn)),
			dtSingleLine|dtCenter|dtVCenter,
		)
		procOverlaySelectObject.Call(hdc, prevBtnFont)
	}
}

func drawFilledCircle(hdc uintptr, cx, cy, r int32, brush uintptr) {
	// GDI has Ellipse but it strokes with the current pen. Use a transparent
	// pen of the brush color so the outline matches.
	pen, _, _ := procOverlayCreatePen.Call(psOverlaySolid, 1, overlayYellow)
	defer procOverlayDeleteObject.Call(pen)
	prevPen, _, _ := procOverlaySelectObject.Call(hdc, pen)
	prevBrush, _, _ := procOverlaySelectObject.Call(hdc, brush)
	procOverlayRoundRect.Call(
		hdc,
		uintptr(cx-r), uintptr(cy-r), uintptr(cx+r), uintptr(cy+r),
		uintptr(2*r), uintptr(2*r),
	)
	procOverlaySelectObject.Call(hdc, prevPen)
	procOverlaySelectObject.Call(hdc, prevBrush)
}

func makeFont(pxHeight, weight int32) uintptr {
	lf := overlayLogfont{
		LfHeight:  -pxHeight,
		LfWeight:  weight,
		LfQuality: 5, // CLEARTYPE_QUALITY
	}
	face := "Segoe UI"
	for i, r := range face {
		if i >= 31 {
			break
		}
		lf.LfFaceName[i] = uint16(r)
	}
	font, _, _ := procOverlayCreateFontW.Call(
		uintptr(lf.LfHeight), uintptr(lf.LfWidth),
		uintptr(lf.LfEscapement), uintptr(lf.LfOrientation),
		uintptr(lf.LfWeight),
		uintptr(lf.LfItalic), uintptr(lf.LfUnderline), uintptr(lf.LfStrikeOut),
		uintptr(lf.LfCharSet),
		uintptr(lf.LfOutPrecision), uintptr(lf.LfClipPrecision),
		uintptr(lf.LfQuality), uintptr(lf.LfPitchAndFamily),
		uintptr(unsafe.Pointer(&lf.LfFaceName[0])),
	)
	return font
}

// ── Localized strings ────────────────────────────────────────────────

func isChineseLanguage(lang string) bool {
	if len(lang) < 2 {
		return false
	}
	first := lang[0]
	second := lang[1]
	if (first == 'z' || first == 'Z') && (second == 'h' || second == 'H') {
		return true
	}
	return false
}

func capsuleText(key string, zh bool) string {
	switch key {
	case "title":
		if zh {
			return "观察已暂停"
		}
		return "Observation is paused"
	case "message":
		if zh {
			return "Isoform 不会录制你的屏幕或音频。"
		}
		return "Isoform is not recording your screen or audio."
	case "button":
		if zh {
			return "恢复观察"
		}
		return "Resume observation"
	}
	return ""
}
