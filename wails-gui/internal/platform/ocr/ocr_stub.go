//go:build !darwin && !windows

package ocr

import "fmt"

type stubEngine struct{}

// New creates a no-op OCR Engine on non-darwin platforms.
func New() Engine {
	return &stubEngine{}
}

func (e *stubEngine) IsSupported() bool {
	return false
}

func (e *stubEngine) RecognizeJPEG(_ []byte, _ []string, _ Priority) (*Result, error) {
	return nil, fmt.Errorf("ocr: not supported on this platform")
}

func (e *stubEngine) RecognizeJPEGWithBoxes(_ []byte, _ []string, _ Priority) ([]TextObservation, error) {
	return nil, fmt.Errorf("ocr: not supported on this platform")
}

func (e *stubEngine) SupportedLanguages() []string {
	return nil
}
