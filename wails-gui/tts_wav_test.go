package main

import (
	"encoding/binary"
	"testing"
)

func TestNormalizePeak(t *testing.T) {
	samples := []float32{-0.25, 0.5}
	got := normalizePeak(samples, 0.95)

	if got[0] != -0.475 || got[1] != 0.95 {
		t.Fatalf("normalizePeak() = %#v, want [-0.475 0.95]", got)
	}
}

func TestApplyEdgeFades(t *testing.T) {
	// 1000 samples at 1kHz "sample rate" -> 12-sample fade on each edge.
	samples := make([]float32, 1000)
	for i := range samples {
		samples[i] = 1
	}
	got := applyEdgeFades(samples, 1000)

	if got[0] != 0 || got[len(got)-1] != 0 {
		t.Fatalf("edges = %v, %v, want 0, 0", got[0], got[len(got)-1])
	}
	if got[5] <= 0 || got[5] >= 1 {
		t.Fatalf("fade-in sample = %v, want strictly between 0 and 1", got[5])
	}
	if got[500] != 1 {
		t.Fatalf("middle sample = %v, want untouched 1", got[500])
	}
}

func TestApplyEdgeFadesShortClip(t *testing.T) {
	// Clip shorter than two fade windows: fade shrinks to half the clip and
	// must not panic or index out of range.
	samples := []float32{1, 1, 1}
	got := applyEdgeFades(samples, 48000)
	if got[0] != 0 || got[len(got)-1] != 0 {
		t.Fatalf("edges = %v, %v, want 0, 0", got[0], got[len(got)-1])
	}
}

func TestEncodeWAV(t *testing.T) {
	wav := encodeWAV([]float32{-2, 0, 2}, 24000)

	if got := string(wav[:4]); got != "RIFF" {
		t.Fatalf("RIFF header = %q", got)
	}
	if got := string(wav[8:12]); got != "WAVE" {
		t.Fatalf("WAVE header = %q", got)
	}
	if got := binary.LittleEndian.Uint32(wav[24:28]); got != 24000 {
		t.Fatalf("sample rate = %d, want 24000", got)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != 6 {
		t.Fatalf("data length = %d, want 6", got)
	}
	if got := int16(binary.LittleEndian.Uint16(wav[44:46])); got != -32767 {
		t.Fatalf("first sample = %d, want -32767", got)
	}
	if got := int16(binary.LittleEndian.Uint16(wav[48:50])); got != 32767 {
		t.Fatalf("last sample = %d, want 32767", got)
	}
}
