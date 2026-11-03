package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// buildTestJPEG returns a JPEG-encoded image of (w,h) filled with a solid
// color. Used so tests don't depend on a fixture file.
func buildTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := color.RGBA{R: 200, G: 100, B: 50, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode test jpeg: %v", err)
	}
	return buf.Bytes()
}

// TestCropForOCRWeChatDropsSidebar locks the WeChat-only sidebar crop: WeChat OCR
// is scoped to the main chat area (left conversation list removed), so it comes
// out narrower than the plain focus-rect crop every other app gets.
func TestCropForOCRWeChatDropsSidebar(t *testing.T) {
	src := buildTestJPEG(t, 200, 100)
	full := ActivitySnapshotRow{FocusRectX: 0, FocusRectY: 0, FocusRectW: 1, FocusRectH: 1}

	wx := full
	wx.App = "tencent_chat"
	wxData, _, err := cropForOCR(wx, src, 90)
	if err != nil {
		t.Fatalf("wechat crop: %v", err)
	}
	other := full
	other.App = "Chrome"
	otherData, _, err := cropForOCR(other, src, 90)
	if err != nil {
		t.Fatalf("other crop: %v", err)
	}

	wxImg, _, err := image.Decode(bytes.NewReader(wxData))
	if err != nil {
		t.Fatalf("decode wechat: %v", err)
	}
	otherImg, _, err := image.Decode(bytes.NewReader(otherData))
	if err != nil {
		t.Fatalf("decode other: %v", err)
	}
	if wxImg.Bounds().Dx() >= otherImg.Bounds().Dx() {
		t.Errorf("WeChat OCR crop must drop the left sidebar (be narrower): wechat=%d other=%d",
			wxImg.Bounds().Dx(), otherImg.Bounds().Dx())
	}
}

func TestCropJPEGToFocusRect_ZeroRectReturnsOriginal(t *testing.T) {
	src := buildTestJPEG(t, 800, 600)
	out, rect, err := cropJPEGToFocusRect(src, 0, 0, 0, 0, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rect.Empty() {
		t.Errorf("expected empty crop rect, got %v", rect)
	}
	if &out[0] != &src[0] {
		t.Errorf("expected to return the original byte slice without re-encoding")
	}
}

func TestCropJPEGToFocusRect_NegativeDimensionsReturnsOriginal(t *testing.T) {
	src := buildTestJPEG(t, 800, 600)
	out, rect, err := cropJPEGToFocusRect(src, 0.5, 0.5, -0.1, 0.2, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rect.Empty() {
		t.Errorf("expected empty crop rect, got %v", rect)
	}
	if len(out) != len(src) {
		t.Errorf("expected unchanged data, got len=%d want=%d", len(out), len(src))
	}
}

func TestCropJPEGToFocusRect_PartialRectProducesExpectedDimensions(t *testing.T) {
	src := buildTestJPEG(t, 800, 600)
	out, rect, err := cropJPEGToFocusRect(src, 0.25, 0.25, 0.5, 0.5, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rect.Empty() {
		t.Fatalf("expected non-empty crop rect")
	}
	if rect.Dx() != 400 || rect.Dy() != 300 {
		t.Errorf("expected 400x300 crop rect, got %dx%d", rect.Dx(), rect.Dy())
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode cropped jpeg: %v", err)
	}
	if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 300 {
		t.Errorf("cropped image dims = %dx%d, want 400x300", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestCropJPEGToFocusRect_OutOfBoundsClampedToImage(t *testing.T) {
	src := buildTestJPEG(t, 800, 600)
	// Rect extends past the right/bottom edges; clamp should keep us inside.
	out, rect, err := cropJPEGToFocusRect(src, 0.5, 0.5, 0.8, 0.8, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rect.Empty() {
		t.Fatalf("expected non-empty crop rect")
	}
	if rect.Max.X > 800 || rect.Max.Y > 600 {
		t.Errorf("crop rect %v exceeds source bounds", rect)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode cropped jpeg: %v", err)
	}
	if img.Bounds().Dx() > 800 || img.Bounds().Dy() > 600 {
		t.Errorf("cropped image %dx%d exceeds source bounds", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestCropJPEGToFocusRect_TooSmallReturnsOriginal(t *testing.T) {
	src := buildTestJPEG(t, 800, 600)
	// Tiny window: 0.01 * 800 = 8 pixels, well below minOCRCropPixels.
	out, rect, err := cropJPEGToFocusRect(src, 0.4, 0.4, 0.01, 0.01, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rect.Empty() {
		t.Errorf("expected empty crop rect for sub-min dimensions, got %v", rect)
	}
	if len(out) != len(src) {
		t.Errorf("expected unchanged data, got len=%d want=%d", len(out), len(src))
	}
}

func TestCropJPEGToFocusRect_FullRectStillCrops(t *testing.T) {
	src := buildTestJPEG(t, 800, 600)
	out, rect, err := cropJPEGToFocusRect(src, 0, 0, 1, 1, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rect.Empty() {
		t.Fatalf("expected non-empty crop rect for full-screen window")
	}
	if rect.Dx() != 800 || rect.Dy() != 600 {
		t.Errorf("full crop rect = %dx%d, want 800x600", rect.Dx(), rect.Dy())
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode cropped jpeg: %v", err)
	}
	if img.Bounds().Dx() != 800 || img.Bounds().Dy() != 600 {
		t.Errorf("re-encoded full image dims = %dx%d, want 800x600", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestCropJPEGToFocusRect_CorruptInputReturnsError(t *testing.T) {
	corrupt := []byte("this is not a jpeg")
	out, rect, err := cropJPEGToFocusRect(corrupt, 0.1, 0.1, 0.5, 0.5, 80)
	if err == nil {
		t.Fatalf("expected error for corrupt input, got nil")
	}
	if !rect.Empty() {
		t.Errorf("expected empty rect on error, got %v", rect)
	}
	if out != nil {
		t.Errorf("expected nil output on error, got len=%d", len(out))
	}
}
