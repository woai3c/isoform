//go:build windows

package ocr

import (
	"os"
	"testing"
)

// Paddle tests run only when YANSU_PADDLE_OCR_EXE points at a usable
// PaddleOCR-json.exe. Locally that's typically the bundle extracted to
// C:\Users\bin\paddleocr-json\PaddleOCR-json_v1.4.1\PaddleOCR-json.exe.
//
// CI doesn't have the binary available, so these are skipped by default.

func skipIfNoPaddleBinary(t *testing.T) {
	t.Helper()
	if v := os.Getenv(paddleOCRExeEnvVar); v == "" {
		t.Skipf("paddle integration test skipped: set %s to enable", paddleOCRExeEnvVar)
	}
}

func TestPaddle_RecognizeJPEG_BlankImage(t *testing.T) {
	skipIfNoPaddleBinary(t)

	e, err := newPaddleEngine()
	if err != nil {
		t.Fatalf("newPaddleEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	jpegData := makeTestJPEG(400, 200)
	result, err := e.RecognizeJPEG(jpegData, []string{"en-US", "zh-Hans"}, PriorityHigh)
	if err != nil {
		t.Fatalf("RecognizeJPEG failed: %v", err)
	}
	if result == nil {
		t.Fatal("nil result on blank image")
	}
	t.Logf("blank-image text=%q conf=%.2f", result.Text, result.Confidence)
}

func TestPaddle_RecognizeJPEGWithBoxes_BlankImage(t *testing.T) {
	skipIfNoPaddleBinary(t)

	e, err := newPaddleEngine()
	if err != nil {
		t.Fatalf("newPaddleEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	jpegData := makeTestJPEG(400, 200)
	obs, err := e.RecognizeJPEGWithBoxes(jpegData, []string{"en-US", "zh-Hans"}, PriorityHigh)
	if err != nil {
		t.Fatalf("RecognizeJPEGWithBoxes failed: %v", err)
	}
	t.Logf("blank-image observations=%d", len(obs))
}

func TestPaddle_EmptyJPEG(t *testing.T) {
	skipIfNoPaddleBinary(t)

	e, err := newPaddleEngine()
	if err != nil {
		t.Fatalf("newPaddleEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	if _, err := e.RecognizeJPEG(nil, nil, PriorityHigh); err == nil {
		t.Fatal("expected error for empty JPEG data")
	}
	if _, err := e.RecognizeJPEGWithBoxes(nil, nil, PriorityHigh); err == nil {
		t.Fatal("expected error for empty JPEG data")
	}
}

// TestQuadToAABB doesn't need the paddle binary — pure unit test for the
// coordinate-conversion helper that takes paddle's 4-point quads to the
// axis-aligned boxes the Engine interface promises.
func TestQuadToAABB(t *testing.T) {
	cases := []struct {
		name   string
		quad   [][]int
		wantOK bool
		want   aabb
	}{
		{
			name:   "axis-aligned rect",
			quad:   [][]int{{10, 20}, {110, 20}, {110, 50}, {10, 50}},
			wantOK: true,
			want:   aabb{minX: 10, minY: 20, maxX: 110, maxY: 50},
		},
		{
			name:   "rotated quad uses min/max",
			quad:   [][]int{{10, 25}, {100, 10}, {110, 40}, {20, 55}},
			wantOK: true,
			want:   aabb{minX: 10, minY: 10, maxX: 110, maxY: 55},
		},
		{
			name:   "empty quad rejected",
			quad:   nil,
			wantOK: false,
		},
		{
			name:   "single point yields zero-size box",
			quad:   [][]int{{42, 42}},
			wantOK: true,
			want:   aabb{minX: 42, minY: 42, maxX: 42, maxY: 42},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := quadToAABB(c.quad)
			if ok != c.wantOK {
				t.Fatalf("ok=%v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestClamp01(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-0.5, 0},
		{0, 0},
		{0.3, 0.3},
		{1, 1},
		{1.7, 1},
	}
	for _, c := range cases {
		got := clamp01(c.in)
		if got != c.want {
			t.Errorf("clamp01(%v)=%v, want %v", c.in, got, c.want)
		}
	}
}

func TestBoundedBuffer(t *testing.T) {
	t.Run("under cap retains everything", func(t *testing.T) {
		b := &boundedBuffer{cap: 100}
		n, err := b.Write([]byte("hello world"))
		if err != nil || n != 11 {
			t.Fatalf("Write returned n=%d err=%v", n, err)
		}
		if got := b.tail(100); got != "hello world" {
			t.Errorf("tail=%q want %q", got, "hello world")
		}
	})

	t.Run("over cap keeps only last cap bytes", func(t *testing.T) {
		b := &boundedBuffer{cap: 8}
		// Write 20 bytes; only the last 8 should survive.
		if _, err := b.Write([]byte("0123456789abcdefghij")); err != nil {
			t.Fatal(err)
		}
		if got := b.tail(100); got != "cdefghij" {
			t.Errorf("tail=%q want %q", got, "cdefghij")
		}
	})

	t.Run("multiple writes accumulate correctly with trimming", func(t *testing.T) {
		b := &boundedBuffer{cap: 10}
		for _, chunk := range []string{"AAAA", "BBBB", "CCCC", "DDDD"} {
			if _, err := b.Write([]byte(chunk)); err != nil {
				t.Fatal(err)
			}
		}
		// Total written = 16 bytes, cap = 10, so last 10 of "AAAABBBBCCCCDDDD"
		// → "BBCCCCDDDD".
		if got := b.tail(100); got != "BBCCCCDDDD" {
			t.Errorf("tail=%q want %q", got, "BBCCCCDDDD")
		}
	})

	t.Run("tail with smaller n returns suffix", func(t *testing.T) {
		b := &boundedBuffer{cap: 100}
		_, _ = b.Write([]byte("abcdefghij"))
		if got := b.tail(3); got != "hij" {
			t.Errorf("tail(3)=%q want %q", got, "hij")
		}
	})

	t.Run("empty buffer tails to empty string", func(t *testing.T) {
		b := &boundedBuffer{cap: 100}
		if got := b.tail(50); got != "" {
			t.Errorf("tail of empty=%q want \"\"", got)
		}
	})
}
