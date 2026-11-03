package main

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"wails-gui/internal/platform/ocr"
)

// ocrDefaultLanguagePrefs is the preferred language order when dynamic
// detection is unavailable (macOS < 12 or non-Darwin). zh-Hans must come
// first: with en-US first, Vision silently drops all Chinese text from
// pure-CJK images (100% CER). normalizeFullwidth corrects the minor
// fullwidth-punctuation side-effect on English text.
var ocrDefaultLanguagePrefs = []string{"zh-Hans", "zh-Hant", "en-US"}

var (
	ocrLanguagePrefsOnce sync.Once
	ocrLanguagePrefs     []string
)

// resolveOCRLanguagePrefs queries the OCR engine for supported languages
// and builds the optimal list. Called once on first use.
func resolveOCRLanguagePrefs(engine ocr.Engine) []string {
	supported := engine.SupportedLanguages()
	if len(supported) == 0 {
		return ocrDefaultLanguagePrefs
	}

	log.Printf("[ocr] platform supported languages: %s", strings.Join(supported, ", "))
	resolved := ocr.ResolveLanguages(ocrDefaultLanguagePrefs, supported)
	log.Printf("[ocr] resolved language prefs: %s", strings.Join(resolved, ", "))
	return resolved
}

// getOCRLanguagePrefs returns the language list, resolving it lazily on
// first call using the provided engine. Safe for concurrent use.
func getOCRLanguagePrefs(engine ocr.Engine) []string {
	ocrLanguagePrefsOnce.Do(func() {
		ocrLanguagePrefs = resolveOCRLanguagePrefs(engine)
	})
	return ocrLanguagePrefs
}

var ocrBreadcrumbSeq atomic.Uint64

func nextOCRBreadcrumbID() uint64 {
	return ocrBreadcrumbSeq.Add(1)
}

func ocrPriorityLabel(prio ocr.Priority) string {
	switch prio {
	case ocr.PriorityHigh:
		return "high"
	case ocr.PriorityMid:
		return "mid"
	case ocr.PriorityLow:
		return "low"
	default:
		return "unknown"
	}
}
