//go:build windows

package activitymon

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// captureMu serializes GDI screen capture calls.
// Concurrent GetDC + BitBlt + GetDIBits from multiple goroutines
// (heartbeat, visual change, event-driven) causes intermittent
// GetDIBits failures on Windows due to GDI resource contention.
var captureMu sync.Mutex

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	procGetDesktopWindow = user32.NewProc("GetDesktopWindow")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procPrintWindow      = user32.NewProc("PrintWindow")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procStretchBlt             = gdi32.NewProc("StretchBlt")
	procSetStretchBltMode      = gdi32.NewProc("SetStretchBltMode")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

const (
	smCxScreen   = 0 // SM_CXSCREEN
	smCyScreen   = 1 // SM_CYSCREEN
	srccopy      = 0x00CC0020
	dibRGBColors = 0
	biRGB        = 0

	stretchHalftone     = 4 // STRETCH_HALFTONE — best quality, slowest
	stretchColorOnColor = 3 // STRETCH_COLORONCOLOR — fast, good for histogram dedup

	// PW_RENDERFULLCONTENT (Windows 8.1+) asks PrintWindow to render the
	// window's full content including DirectX/Direct3D surfaces. Used as one
	// fallback when BitBlt+GetDIBits fails during DWM transitions.
	pwRenderFullContent = 0x00000002

	// CAPTUREBLT (BitBlt ROP modifier) tells DWM to include layered windows
	// on top of the source before copying — forces a compositor flatten
	// which can succeed when plain SRCCOPY fails during DWM activity.
	captureblt = 0x40000000

	// getDIBitsRetrySleep: short pause before retrying GetDIBits as-is. Many
	// GDI failures in the field are sub-frame transients that clear within
	// tens of milliseconds. Cheap to attempt first; runs under captureMu but
	// concurrent capture is already serialized so this just delays the next
	// caller by the same amount.
	getDIBitsRetrySleep = 50 * time.Millisecond
)

// Per-fallback success counters. Tracks which recovery path actually saved
// a frame so we can tell at a glance which mechanism is worth keeping vs
// dropping. Exposed via FallbackCounts() for diagnostics surfaces.
var (
	fallbackSleepRetryCount  atomic.Uint64
	fallbackCaptureBltCount  atomic.Uint64
	fallbackPrintWindowCount atomic.Uint64
)

// FallbackCounts returns the running tally of full-size captures recovered
// by each fallback path (sleep+retry, BitBlt|CAPTUREBLT, PrintWindow).
func FallbackCounts() (sleepRetry, captureBlt, printWindow uint64) {
	return fallbackSleepRetryCount.Load(),
		fallbackCaptureBltCount.Load(),
		fallbackPrintWindowCount.Load()
}

// BITMAPINFOHEADER for GetDIBits.
type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// CaptureScreen captures the primary screen as raw RGBA pixel data.
// Serialized via captureMu to prevent concurrent GDI failures.
//
// targetWidth/targetHeight, when both > 0 and smaller than the screen, request
// a downsampled capture done in GDI (StretchBlt) before the GPU→CPU copy.
// The visual-change detector exploits this to capture at ~1/6 resolution,
// cutting GetDIBits transfer + BGRA→RGBA conversion + GC pressure by ~36×.
// Pass (-1, -1) to capture at full resolution.
func CaptureScreen(targetWidth, targetHeight int) ([]byte, int, int, error) {
	captureMu.Lock()
	defer captureMu.Unlock()

	srcW, srcH := getScreenSize()
	if srcW == 0 || srcH == 0 {
		return nil, 0, 0, fmt.Errorf("screen dimensions are zero")
	}

	dstW, dstH := srcW, srcH
	if targetWidth > 0 && targetHeight > 0 && targetWidth < srcW && targetHeight < srcH {
		dstW, dstH = targetWidth, targetHeight
	}

	// Get desktop DC
	desktop, _, _ := procGetDesktopWindow.Call()
	hdcScreen, _, _ := procGetDC.Call(desktop)
	if hdcScreen == 0 {
		return nil, 0, 0, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(desktop, hdcScreen)

	// Create compatible DC and bitmap sized to the *destination* — we want
	// GetDIBits to read only the downsampled image, not the full screen.
	hdcMem, _, _ := procCreateCompatibleDC.Call(hdcScreen)
	if hdcMem == 0 {
		return nil, 0, 0, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(hdcMem)

	hBitmap, _, _ := procCreateCompatibleBitmap.Call(hdcScreen, uintptr(dstW), uintptr(dstH))
	if hBitmap == 0 {
		return nil, 0, 0, fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(hBitmap)

	procSelectObject.Call(hdcMem, hBitmap)

	// Track which render path landed pixels in hdcMem so a downstream
	// GetDIBits failure knows whether the PrintWindow fallback is still
	// available, and so we can attribute errors / log fallback hits.
	captureMethod := "bitblt"
	usedFallback := false

	if dstW == srcW && dstH == srcH {
		ret, _, _ := procBitBlt.Call(
			hdcMem, 0, 0, uintptr(dstW), uintptr(dstH),
			hdcScreen, 0, 0,
			srccopy,
		)
		if ret == 0 {
			// BitBlt failures on Windows are usually transient DWM-side
			// glitches (compositor mode switches, fullscreen handoff,
			// protected-content overlays). PrintWindow goes through
			// WM_PRINT messaging and can succeed when BitBlt returns 0.
			pwRet, _, _ := procPrintWindow.Call(desktop, hdcMem, pwRenderFullContent)
			if pwRet == 0 {
				return nil, 0, 0, fmt.Errorf("BitBlt failed and PrintWindow fallback failed")
			}
			captureMethod = "printwindow"
			usedFallback = true
		}
	} else {
		// COLORONCOLOR is sufficient for grayscale histogram dedup and is
		// roughly an order of magnitude faster than HALFTONE.
		procSetStretchBltMode.Call(hdcMem, stretchColorOnColor)
		ret, _, _ := procStretchBlt.Call(
			hdcMem, 0, 0, uintptr(dstW), uintptr(dstH),
			hdcScreen, 0, 0, uintptr(srcW), uintptr(srcH),
			srccopy,
		)
		if ret == 0 {
			return nil, 0, 0, fmt.Errorf("StretchBlt failed")
		}
	}

	// Extract pixel data via GetDIBits
	bmi := bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       int32(dstW),
		BiHeight:      -int32(dstH), // negative = top-down
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: biRGB,
	}

	dataSize := dstW * dstH * 4
	pixels := AcquireCaptureBuffer(dataSize)

	dibits := func() (uintptr, error) {
		r, _, e := procGetDIBits.Call(
			hdcMem,
			hBitmap,
			0,
			uintptr(dstH),
			uintptr(unsafe.Pointer(&pixels[0])),
			uintptr(unsafe.Pointer(&bmi)),
			dibRGBColors,
		)
		return r, e
	}

	ret, lastErr := dibits()

	// GetDIBits recovery chain — only relevant for full-size captures via
	// BitBlt; downsampled (StretchBlt) and PrintWindow-first paths fall
	// through to the error return. The empirical failure mode is GetDIBits
	// returning 0 with `GetLastError == 0` ("operation completed
	// successfully") during DWM transitions. Try a few independent recovery
	// paths in order of increasing cost; first one that yields a non-zero
	// GetDIBits wins.
	if ret == 0 && captureMethod == "bitblt" && dstW == srcW && dstH == srcH {
		// Path A: short sleep + retry as-is. Many GDI wedges are sub-frame
		// transients and clear by themselves within tens of ms.
		time.Sleep(getDIBitsRetrySleep)
		ret, lastErr = dibits()
		if ret != 0 {
			captureMethod = "sleep-retry"
			usedFallback = true
			fallbackSleepRetryCount.Add(1)
		}

		// Path B: re-BitBlt with the CAPTUREBLT ROP modifier. This forces
		// DWM to flatten any layered windows on top of the source before
		// copying — slower than plain SRCCOPY but tolerates DWM activity
		// that plain BitBlt does not.
		if ret == 0 {
			bltRet, _, _ := procBitBlt.Call(
				hdcMem, 0, 0, uintptr(dstW), uintptr(dstH),
				hdcScreen, 0, 0,
				srccopy|captureblt,
			)
			if bltRet == 0 {
				log.Printf("[activity-capture] fallback: BitBlt|CAPTUREBLT returned 0")
			} else {
				ret, lastErr = dibits()
				if ret != 0 {
					captureMethod = "captureblt"
					usedFallback = true
					fallbackCaptureBltCount.Add(1)
				} else {
					log.Printf("[activity-capture] fallback: GetDIBits still failed after BitBlt|CAPTUREBLT (win32 err=%v)", lastErr)
				}
			}
		}

		// Path C: PrintWindow refresh, then GetDIBits. PrintWindow on the
		// desktop window is unreliable in practice (the desktop window
		// doesn't respond to WM_PRINT in the usual sense), so it's the
		// last resort. Logged distinct failure modes so we can tell
		// whether to drop this path in a future iteration.
		if ret == 0 {
			pwRet, _, _ := procPrintWindow.Call(desktop, hdcMem, pwRenderFullContent)
			if pwRet == 0 {
				log.Printf("[activity-capture] fallback: PrintWindow returned 0")
			} else {
				ret, lastErr = dibits()
				if ret != 0 {
					captureMethod = "printwindow"
					usedFallback = true
					fallbackPrintWindowCount.Add(1)
				} else {
					log.Printf("[activity-capture] fallback: GetDIBits still failed after PrintWindow (win32 err=%v)", lastErr)
				}
			}
		}
	}
	if ret == 0 {
		return nil, 0, 0, fmt.Errorf("GetDIBits failed (method=%s, win32 err=%v)", captureMethod, lastErr)
	}
	if usedFallback {
		sr, cb, pw := FallbackCounts()
		log.Printf("[activity-capture] captured via %s fallback (sleep_retry=%d captureblt=%d printwindow=%d)",
			captureMethod, sr, cb, pw)
	}

	// Convert BGRA → RGBA in-place
	for i := 0; i < dataSize; i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
	}

	return pixels, dstW, dstH, nil
}

func getScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	return int(w), int(h)
}

// ScreenDimensions returns the primary screen dimensions on Windows.
func ScreenDimensions() (int, int) {
	return getScreenSize()
}

// SupportsWindowCapture reports whether per-window capture is available. It is
// false on Windows, so callers treat a 0 window ID as "no per-window support" and
// fall back to full-screen capture instead of skipping the frame.
func SupportsWindowCapture() bool {
	return false
}

// IsScreenLocked detects if the workstation is locked by checking
// if the foreground window belongs to LogonUI.exe or if there is none.
func IsScreenLocked() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		// No foreground window often means locked or secure desktop
		return true
	}

	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}

	hProc, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if hProc == 0 {
		return false
	}
	defer procCloseHandle.Call(hProc)

	exePath := queryProcessImageName(hProc)
	baseName := baseNameWithoutExt(exePath)

	return baseName == "LogonUI" || baseName == "LockApp"
}

// FrontmostWindowInfo returns the foreground window's HWND (as ID), the
// owning application's friendly name and its pid. ID is 0 when there is no
// foreground window (locked screen, secure desktop). BundleID stays empty:
// per-window capture, the only user of it, is macOS-only.
func FrontmostWindowInfo() FrontmostWindow {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return FrontmostWindow{}
	}

	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	win := FrontmostWindow{ID: uint32(hwnd), PID: int(pid)}
	if pid == 0 {
		return win
	}

	hProc, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if hProc == 0 {
		return win
	}
	defer procCloseHandle.Call(hProc)

	exePath := queryProcessImageName(hProc)
	if exePath == "" {
		return win
	}
	win.OwnerName = cachedAppName(exePath)
	return win
}

// CaptureWindowJPEG is a no-op on Windows — per-window ScreenCaptureKit is
// macOS-only. Returning nil signals the caller to fall back to full-screen
// JPEG encoding.
func CaptureWindowJPEG(_ uint32, _ int) ([]byte, int, int) {
	return nil, 0, 0
}

// windowsScreenCapture implements ScreenCapture using BitBlt.
type windowsScreenCapture struct{}

// NewScreenCapture creates a Windows ScreenCapture.
func NewScreenCapture() ScreenCapture {
	return &windowsScreenCapture{}
}

func (c *windowsScreenCapture) CaptureRaw() ([]byte, int, int, error) {
	return CaptureScreen(-1, -1)
}

func (c *windowsScreenCapture) EncodeJPEG(rgbaPixels []byte, w, h, quality int) ([]byte, error) {
	if quality <= 0 || quality > 100 {
		quality = 80
	}

	img := &image.NRGBA{
		Pix:    rgbaPixels,
		Stride: w * 4,
		Rect:   image.Rect(0, 0, w, h),
	}

	var buf bytes.Buffer
	buf.Grow(w * h / 4)
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
