package pii

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"strings"
	"testing"

	"wails-gui/internal/platform/ocr"
)

func TestSpanBBoxCoherent(t *testing.T) {
	cases := []struct {
		name   string
		w, h   float64
		accept bool
	}{
		{"single-line credit card", 0.20, 0.02, true},
		{"two-line API key", 0.30, 0.05, true},
		{"three-line street address", 0.40, 0.07, true},
		{"too wide (full screen banner)", 0.65, 0.04, false},
		{"too tall (half screen vertical)", 0.30, 0.40, false},
		{"scattered observations (huge bbox)", 0.90, 0.80, false},
	}
	for _, c := range cases {
		if got := spanBBoxCoherent(c.w, c.h); got != c.accept {
			t.Errorf("%s: spanBBoxCoherent(%.2f, %.2f) = %v, want %v", c.name, c.w, c.h, got, c.accept)
		}
	}
}

func makeSolidJPEG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode test jpeg: %v", err)
	}
	return buf.Bytes()
}

func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()
	fn()
	return buf.String()
}

// A region over the cap is never skipped (that would leave its PII visible):
// MaskRegions fails so the caller drops the frame.
func TestMaskRegionsRefusesOversized(t *testing.T) {
	jpegData := makeSolidJPEG(t, 200, 200, color.RGBA{R: 200, G: 220, B: 255, A: 255})
	dets := []DetectionResult{
		{Category: "password", Text: "fullscreen-fake", X: 0, Y: 0, Width: 1.0, Height: 1.0},
	}
	out, err := MaskRegions(jpegData, dets, 85)
	if !errors.Is(err, ErrMaskRegionTooLarge) || out != nil {
		t.Fatalf("MaskRegions(full-image) = %d bytes, %v; want no image and ErrMaskRegionTooLarge", len(out), err)
	}
}

func TestMaskRegionsRefusesAboveAreaThreshold(t *testing.T) {
	// 0.6 * 0.6 = 36% > 25% threshold, next to a small region that alone
	// would mask fine.
	jpegData := makeSolidJPEG(t, 200, 200, color.RGBA{R: 200, G: 220, B: 255, A: 255})
	dets := []DetectionResult{
		{Category: "credit_card", Text: "small-fake", X: 0.1, Y: 0.1, Width: 0.1, Height: 0.1},
		{Category: "street_address", Text: "scattered-fake", X: 0.1, Y: 0.1, Width: 0.6, Height: 0.6},
	}
	var err error
	logged := captureLog(t, func() {
		_, err = MaskRegions(jpegData, dets, 85)
	})
	if !errors.Is(err, ErrMaskRegionTooLarge) {
		t.Fatalf("MaskRegions(36%%) err = %v, want ErrMaskRegionTooLarge", err)
	}
	if strings.Contains(logged, "scattered-fake") {
		t.Errorf("the log must not carry the detected text, got: %q", logged)
	}
}

func TestMaskRegionsAppliesSmall(t *testing.T) {
	// 0.1 * 0.1 = 1% — well under the 25% cap, should pixelate normally.
	jpegData := makeSolidJPEG(t, 200, 200, color.RGBA{R: 200, G: 220, B: 255, A: 255})
	dets := []DetectionResult{
		{Category: "credit_card", Text: "small-fake", X: 0.1, Y: 0.1, Width: 0.1, Height: 0.1},
	}
	out := captureLog(t, func() {
		if _, err := MaskRegions(jpegData, dets, 85); err != nil {
			t.Fatalf("MaskRegions: %v", err)
		}
	})
	if strings.Contains(out, "skipping oversized mask") {
		t.Errorf("did not expect oversized-mask log for a 1%% detection, got: %q", out)
	}
}

// A scattered span with one line over the mask cap must not leave that line
// visible: masking fails and the frame is dropped.
func TestSpanRegionsOversizedLineDropsFrame(t *testing.T) {
	obs := []ocr.TextObservation{
		{Text: "Jane", X: 0.05, Y: 0.10, Width: 0.10, Height: 0.02},
		{Text: "DOE", X: 0.10, Y: 0.40, Width: 0.80, Height: 0.40}, // giant heading
	}
	regions := spanRegions(obs, 0, 1, detectedSpan{text: "Jane DOE", entityType: "person", score: 0.9})
	jpegData := makeSolidJPEG(t, 200, 200, color.RGBA{R: 200, G: 220, B: 255, A: 255})
	if _, err := MaskRegions(jpegData, regions, 85); !errors.Is(err, ErrMaskRegionTooLarge) {
		t.Fatalf("MaskRegions(oversized line) err = %v, want ErrMaskRegionTooLarge", err)
	}
}

// A span whose OCR lines are scattered across the screen is masked line by
// line, not dropped and not masked as one huge rectangle.
func TestSpanRegionsScatteredSpanMasksEachLine(t *testing.T) {
	obs := []ocr.TextObservation{
		{Text: "Jane", X: 0.05, Y: 0.10, Width: 0.10, Height: 0.02},
		{Text: "Doe", X: 0.80, Y: 0.85, Width: 0.08, Height: 0.02},
	}
	sp := detectedSpan{text: "Jane Doe", entityType: "person", score: 0.9}

	got := spanRegions(obs, 0, 1, sp)
	if len(got) != 2 {
		t.Fatalf("regions = %d, want one per line: %+v", len(got), got)
	}
	for i, r := range got {
		if r.X != obs[i].X || r.Y != obs[i].Y || r.Width != obs[i].Width || r.Height != obs[i].Height || r.Category != "person" {
			t.Errorf("region %d = %+v, want line %d's box", i, r, i)
		}
	}

	near := []ocr.TextObservation{
		{Text: "Jane", X: 0.05, Y: 0.10, Width: 0.10, Height: 0.02},
		{Text: "Doe", X: 0.16, Y: 0.10, Width: 0.08, Height: 0.02},
	}
	if got := spanRegions(near, 0, 1, sp); len(got) != 1 || got[0].Width < 0.18 {
		t.Fatalf("coherent span regions = %+v, want one outer box", got)
	}
}
