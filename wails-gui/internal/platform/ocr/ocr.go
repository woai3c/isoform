package ocr

import (
	"fmt"
	"time"
)

// Priority determines dispatch order when multiple OCR requests are queued
// against the same process-wide Vision serialization gate.
type Priority int

const (
	// PriorityLow is for bulk background work (e.g. post-session OCR worker).
	PriorityLow Priority = iota
	// PriorityMid is for realtime capture-path work (e.g. PII mask).
	PriorityMid
	// PriorityHigh is for user-interactive work (e.g. highlight-on-click).
	PriorityHigh
)

// Result holds the recognized text and confidence from OCR.
type Result struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// TextObservation represents a single recognized text region with its bounding box.
// Coordinates are normalized [0,1]. Origin is top-left (converted from Vision's bottom-left).
type TextObservation struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	X          float64 `json:"x"`      // left edge, normalized [0,1]
	Y          float64 `json:"y"`      // top edge, normalized [0,1]
	Width      float64 `json:"width"`  // normalized [0,1]
	Height     float64 `json:"height"` // normalized [0,1]
}

// Engine performs optical character recognition on image data.
//
// Implementations MUST serialize concurrent recognition calls across the
// process (Vision's VNCRImageReaderDetector fully saturates a core per
// in-flight request). Higher-priority callers are dispatched before
// lower-priority ones when multiple are queued.
type Engine interface {
	// RecognizeJPEG runs OCR on JPEG-encoded image data.
	// langs specifies recognition languages (e.g. ["en-US", "zh-Hans"]).
	RecognizeJPEG(jpegData []byte, langs []string, prio Priority) (*Result, error)

	// RecognizeJPEGWithBoxes runs OCR and returns per-line text observations with bounding boxes.
	RecognizeJPEGWithBoxes(jpegData []byte, langs []string, prio Priority) ([]TextObservation, error)

	// SupportedLanguages returns the recognition languages available on this
	// platform, queried from the OS at runtime. Returns nil if the query is
	// not supported (e.g. macOS < 12, non-Darwin platforms).
	SupportedLanguages() []string

	// IsSupported returns true if OCR is available on this platform.
	IsSupported() bool
}

// NormalizeFullwidth converts fullwidth ASCII variants (U+FF01..U+FF5E) to
// their halfwidth equivalents. Vision returns these when zh-Hans is the
// primary recognition language and the image contains ASCII punctuation.
func NormalizeFullwidth(s string) string {
	for _, r := range s {
		if (r >= 0xFF01 && r <= 0xFF5E) || r == 0x3000 {
			return normalizeFullwidthSlow(s)
		}
	}
	return s
}

func normalizeFullwidthSlow(s string) string {
	runes := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			runes = append(runes, r-0xFEE0)
		case r == 0x3000:
			runes = append(runes, ' ')
		default:
			runes = append(runes, r)
		}
	}
	return string(runes)
}

// ResolveLanguages builds an optimal recognition language list by filtering
// preferred languages against what the platform actually supports, then
// appending any additional CJK languages the user has installed.
// If supported is nil (query unavailable), returns preferred as-is.
func ResolveLanguages(preferred, supported []string) []string {
	if len(supported) == 0 {
		return preferred
	}

	supportedSet := make(map[string]bool, len(supported))
	for _, l := range supported {
		supportedSet[l] = true
	}

	seen := make(map[string]bool, len(preferred)+len(supported))
	var result []string

	for _, l := range preferred {
		if supportedSet[l] && !seen[l] {
			result = append(result, l)
			seen[l] = true
		}
	}

	// Append CJK languages the user has installed but we didn't list.
	cjkPrefixes := []string{"zh-", "ja-", "ko-"}
	for _, l := range supported {
		if seen[l] {
			continue
		}
		for _, prefix := range cjkPrefixes {
			if len(l) >= len(prefix) && l[:len(prefix)] == prefix {
				result = append(result, l)
				seen[l] = true
				break
			}
		}
	}

	// Append en-US if it was in supported but not yet included.
	if supportedSet["en-US"] && !seen["en-US"] {
		result = append(result, "en-US")
		seen["en-US"] = true
	}

	if len(result) == 0 {
		return preferred
	}
	return result
}

// HelperTimeoutError reports an OCR request that exceeded its time budget.
// The persistent helper is restarted only after two consecutive timeouts.
// Cold is true when the run already had the long cold-start budget, so the
// helper is more likely wedged than still compiling its model.
type HelperTimeoutError struct {
	Timeout time.Duration
	Cold    bool
}

func (e *HelperTimeoutError) Error() string {
	return fmt.Sprintf("ocr helper timed out after %s", e.Timeout)
}
