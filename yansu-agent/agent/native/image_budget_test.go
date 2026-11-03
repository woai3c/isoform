package native

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// makeNoisyImageBase64 builds a high-entropy PNG (so it does NOT compress to
// near-nothing) and returns its base64 — used to exercise the resize path.
func makeNoisyImageBase64(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Deterministic LCG noise — high entropy so PNG cannot compress it to
	// near-nothing, making it a faithful proxy for a real screenshot where
	// PNG is large and a downscaled JPEG is much smaller.
	seed := uint32(0x9e3779b9)
	next := func() uint8 {
		seed = seed*1664525 + 1013904223
		return uint8(seed >> 24)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: next(), G: next(), B: next(), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestEnforceRequestByteBudget_NoopWhenSmall(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "hi there"}}},
	}
	stats := EnforceRequestByteBudget(messages)
	if stats.Touched() {
		t.Fatalf("expected no-op on small request, got %+v", stats)
	}
	if stats.BytesBefore != stats.BytesAfter {
		t.Fatalf("bytes changed on no-op: before=%d after=%d", stats.BytesBefore, stats.BytesAfter)
	}
}

func TestEnforceRequestByteBudget_DropsOldestStringToolResults(t *testing.T) {
	big := strings.Repeat("x", 800*1024) // 800 KB each → three exceed the 1.5 MB target
	messages := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "a", Content: big}}},
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "b", Content: big}}},
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "c", Content: big}}},
	}

	stats := EnforceRequestByteBudget(messages)
	if stats.DroppedCount == 0 {
		t.Fatalf("expected at least one drop, got %+v", stats)
	}
	if got := approxMessageBytes(messages); got > requestByteTarget {
		t.Fatalf("still over budget after enforcement: %d > %d", got, requestByteTarget)
	}

	// Newest result (c) must survive; pairing fields preserved on all.
	last := messages[2].Content.([]ContentBlock)[0]
	if last.Content.(string) != big {
		t.Fatalf("newest tool_result should be preserved verbatim")
	}
	for i, m := range messages {
		blk := m.Content.([]ContentBlock)[0]
		if blk.Type != "tool_result" {
			t.Fatalf("message %d: tool_result Type must be preserved, got %q", i, blk.Type)
		}
		if blk.ToolUseID == "" {
			t.Fatalf("message %d: ToolUseID must be preserved for pairing", i)
		}
	}
	// Oldest (a) should have been pruned to the placeholder.
	first := messages[0].Content.([]ContentBlock)[0]
	if first.Content.(string) != droppedResultPlaceholder {
		t.Fatalf("oldest tool_result should be the placeholder, got %d bytes", len(first.Content.(string)))
	}
}

func TestEnforceRequestByteBudget_DownsizesImageBlock(t *testing.T) {
	// One oversized image inside a tool_result (the Read-image shape) that
	// pushes the request over budget on its own.
	b64 := makeNoisyImageBase64(t, 2400, 1600)
	if len(b64) <= requestByteTarget {
		t.Skip("generated test image not large enough to trigger budget")
	}
	messages := []Message{
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "img", Content: []ContentBlock{
				{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png", Data: b64}},
			}},
		}},
	}

	stats := EnforceRequestByteBudget(messages)
	if stats.DownsizedCount == 0 {
		t.Fatalf("expected image to be downsized, got %+v", stats)
	}

	sub := messages[0].Content.([]ContentBlock)[0].Content.([]ContentBlock)[0]
	if sub.Type != "image" || sub.Source == nil {
		t.Fatalf("image block should remain an image (vision preserved), got %+v", sub)
	}
	if sub.Source.MediaType != "image/jpeg" {
		t.Fatalf("expected re-encode to jpeg, got %q", sub.Source.MediaType)
	}
	if len(sub.Source.Data) >= len(b64) {
		t.Fatalf("downsized image should be smaller: before=%d after=%d", len(b64), len(sub.Source.Data))
	}
	// Re-decode and confirm the longest edge was capped.
	raw, err := base64.StdEncoding.DecodeString(sub.Source.Data)
	if err != nil {
		t.Fatalf("decode downsized image: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Width > maxImageEdge || cfg.Height > maxImageEdge {
		t.Fatalf("longest edge not capped: %dx%d (max %d)", cfg.Width, cfg.Height, maxImageEdge)
	}
}

func TestShrinkImageData(t *testing.T) {
	b64 := makeNoisyImageBase64(t, 2000, 1500)
	mt, data, ok := shrinkImageData(b64)
	if !ok {
		t.Fatalf("expected shrink to succeed on oversized png")
	}
	if mt != "image/jpeg" {
		t.Fatalf("expected jpeg, got %q", mt)
	}
	if len(data) >= len(b64) {
		t.Fatalf("expected smaller output: before=%d after=%d", len(b64), len(data))
	}

	// Garbage / non-image input is left untouched.
	if _, _, ok := shrinkImageData("not-base64-image-data"); ok {
		t.Fatalf("expected shrink to fail on non-image input")
	}
}

func TestScaledDims(t *testing.T) {
	cases := []struct {
		w, h, max, wantW, wantH int
	}{
		{2000, 1500, 1568, 1568, 1176},
		{1500, 2000, 1568, 1176, 1568},
		{3840, 2160, 1568, 1568, 882},
	}
	for _, c := range cases {
		gw, gh := scaledDims(c.w, c.h, c.max)
		if gw != c.wantW || gh != c.wantH {
			t.Errorf("scaledDims(%d,%d,%d) = (%d,%d), want (%d,%d)", c.w, c.h, c.max, gw, gh, c.wantW, c.wantH)
		}
	}
}
