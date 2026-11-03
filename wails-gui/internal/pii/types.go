package pii

import "wails-gui/internal/platform/ocr"

// DetectionResult represents a detected PII region with its bounding box.
type DetectionResult struct {
	Text       string  // the matched text
	Category   string  // e.g. "credit_card", "password", "api_key"
	X          float64 // normalized [0,1]
	Y          float64 // normalized [0,1]
	Width      float64 // normalized [0,1]
	Height     float64 // normalized [0,1]
	Confidence float64
}

// Detector detects PII in OCR observations.
type Detector interface {
	// Detect scans OCR text observations for PII.
	Detect(observations []ocr.TextObservation) []DetectionResult

	// IsReady returns true if the detector is initialized and usable.
	IsReady() bool

	// Destroy releases resources held by the detector.
	Destroy()
}
