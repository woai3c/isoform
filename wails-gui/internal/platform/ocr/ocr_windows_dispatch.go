//go:build windows

package ocr

import (
	"log"
	"os"
	"strings"
	"sync"
)

// Windows OCR has two backends:
//
//   - winRTEngine     — wraps Windows.Media.Ocr (ocr_windows.go). Fast, zero
//                       footprint, but weak on Chinese — see the comparison
//                       report in the PR that introduced this dispatch for
//                       the data.
//   - paddleEngine    — wraps PaddleOCR-json (ocr_windows_paddle.go). ~5×
//                       slower per call but recognises Chinese correctly
//                       across the same screenshot distribution.
//
// New() returns a singleton selected via the YANSU_PADDLE_OCR env var,
// defaulting to Paddle. If Paddle init fails (missing binary, wrong DLL, etc.)
// we fall back to WinRT so the app stays functional.

const paddleOCREnvVar = "YANSU_PADDLE_OCR"

var (
	sharedEngineOnce sync.Once
	sharedEngine     Engine
)

// New returns the process-wide Windows OCR engine. Safe to call from any
// goroutine; the first caller bears the init cost (~1s for Paddle subprocess
// startup) and every subsequent caller reuses the same instance.
func New() Engine {
	sharedEngineOnce.Do(initSharedEngine)
	return sharedEngine
}

func initSharedEngine() {
	if !paddleBackendEnabled() {
		log.Printf("[ocr] Windows OCR backend: WinRT (Windows.Media.Ocr) — %s=0", paddleOCREnvVar)
		sharedEngine = newWinRTEngine()
		return
	}
	pe, err := newPaddleEngine()
	if err != nil {
		log.Printf("[ocr] Windows OCR backend: WinRT (Paddle unavailable: %v)", err)
		sharedEngine = newWinRTEngine()
		return
	}
	log.Printf("[ocr] Windows OCR backend: PaddleOCR")
	sharedEngine = pe
}

func paddleBackendEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(paddleOCREnvVar))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}
