//go:build windows

package ocr

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// Tests in this file target the WinRT (Windows.Media.Ocr) backend directly via
// newWinRTEngine, independent of the YANSU_PADDLE_OCR env-driven dispatch in
// New(). Paddle has its own test file (ocr_windows_paddle_test.go) that runs
// only when YANSU_PADDLE_OCR_EXE points at a usable binary.

func makeTestJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	return buf.Bytes()
}

func TestWinRT_RecognizeJPEG(t *testing.T) {
	e := newWinRTEngine()
	if !e.IsSupported() {
		t.Skip("OCR not supported")
	}

	jpegData := makeTestJPEG(200, 100)

	result, err := e.RecognizeJPEG(jpegData, []string{"en-US"}, PriorityHigh)
	if err != nil {
		t.Fatalf("RecognizeJPEG failed: %v", err)
	}

	t.Logf("Text: %q, Confidence: %.2f", result.Text, result.Confidence)
	// Blank white image should return empty text.
	if result.Confidence != 0.0 {
		t.Logf("Note: expected 0 confidence for blank image, got %.2f", result.Confidence)
	}
}

func TestWinRT_RecognizeJPEGWithBoxes(t *testing.T) {
	e := newWinRTEngine()
	if !e.IsSupported() {
		t.Skip("OCR not supported")
	}

	jpegData := makeTestJPEG(200, 100)

	observations, err := e.RecognizeJPEGWithBoxes(jpegData, []string{"en-US"}, PriorityHigh)
	if err != nil {
		t.Fatalf("RecognizeJPEGWithBoxes failed: %v", err)
	}

	t.Logf("Observations: %d", len(observations))
	// Blank white image should have no observations.
}

func TestWinRT_EmptyJPEG(t *testing.T) {
	e := newWinRTEngine()
	if !e.IsSupported() {
		t.Skip("OCR not supported")
	}

	_, err := e.RecognizeJPEG(nil, nil, PriorityHigh)
	if err == nil {
		t.Fatal("expected error for empty JPEG data")
	}
}
