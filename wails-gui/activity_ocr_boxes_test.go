package main

import (
	"encoding/json"
	"image"
	"math"
	"strings"
	"testing"

	"wails-gui/internal/platform/ocr"
)

func TestObservationsToFullFrameBoxes(t *testing.T) {
	obs := []ocr.TextObservation{
		{Text: "hello", X: 0.5, Y: 0.5, Width: 0.2, Height: 0.1},
	}

	// No crop: coordinates pass through unchanged.
	boxes := observationsToFullFrameBoxes(obs, image.Rectangle{}, 0, 0)
	if len(boxes) != 1 {
		t.Fatalf("got %d boxes, want 1", len(boxes))
	}
	if boxes[0].X != 0.5 || boxes[0].Y != 0.5 || boxes[0].Width != 0.2 || boxes[0].Height != 0.1 {
		t.Fatalf("uncropped box = %+v, want passthrough", boxes[0])
	}
	if boxes[0].Text != "hello" {
		t.Fatalf("text = %q, want hello", boxes[0].Text)
	}

	// Cropped: a box at the center of a crop occupying the right half of a
	// 1000x500 image lands at the center of that right half in full-frame
	// coordinates.
	crop := image.Rect(500, 0, 1000, 500)
	boxes = observationsToFullFrameBoxes(obs, crop, 1000, 500)
	wantX := (500.0 + 0.5*500.0) / 1000.0 // 0.75
	wantY := (0.0 + 0.5*500.0) / 500.0    // 0.5
	wantW := 0.2 * 500.0 / 1000.0         // 0.1
	wantH := 0.1 * 500.0 / 500.0          // 0.1
	const eps = 1e-9
	if math.Abs(boxes[0].X-wantX) > eps || math.Abs(boxes[0].Y-wantY) > eps ||
		math.Abs(boxes[0].Width-wantW) > eps || math.Abs(boxes[0].Height-wantH) > eps {
		t.Fatalf("cropped box = %+v, want x=%v y=%v w=%v h=%v", boxes[0], wantX, wantY, wantW, wantH)
	}
}

func TestFilterOCRBoxesByKeyword(t *testing.T) {
	boxes := []OCRHighlightBox{
		{Text: "pinged YeTone about rollout"},
		{Text: "completely unrelated"},
		{Text: "讨论 深度 学习 模型"}, // OCR-style CJK spaces
	}
	got := filterOCRBoxesByKeyword(boxes, "yetone")
	if len(got) != 1 || got[0].Text != "pinged YeTone about rollout" {
		t.Fatalf("case-insensitive match: got %+v", got)
	}
	got = filterOCRBoxesByKeyword(boxes, "深度学习")
	if len(got) != 1 || got[0].Text != "讨论 深度 学习 模型" {
		t.Fatalf("cjk-space-insensitive match: got %+v", got)
	}
	if got := filterOCRBoxesByKeyword(boxes, "  "); got != nil {
		t.Fatalf("blank keyword: got %+v, want nil", got)
	}
}

// TestGetOrComputeOCRBoxesRejectsNonLocalPaths locks the untrusted-input
// guard: traversal and absolute paths must be rejected BEFORE any database
// lookup or file read. The monitor state deliberately has no store — if the
// guard were removed, every case would panic on the nil store instead of
// failing via an incidental downstream error (DB miss, unreadable file, OCR
// failure), which is what the first version of this test couldn't tell
// apart. The error text is asserted for the same reason.
func TestGetOrComputeOCRBoxesRejectsNonLocalPaths(t *testing.T) {
	m := &activityMonitorState{}
	for _, p := range []string{"../../../etc/passwd", "/etc/passwd", "..", "a/../../b.jpg", ""} {
		_, err := m.getOrComputeOCRBoxes(p)
		if err == nil {
			t.Errorf("getOrComputeOCRBoxes(%q): want error, got nil", p)
			continue
		}
		if !strings.Contains(err.Error(), "invalid snapshot path") {
			t.Errorf("getOrComputeOCRBoxes(%q): error %q is not the path guard's", p, err)
		}
	}
}

func TestMarshalOCRBoxes(t *testing.T) {
	if got := marshalOCRBoxes(nil); got != "[]" {
		t.Fatalf("nil boxes = %q, want []", got)
	}
	boxes := []OCRHighlightBox{{Text: "a", X: 0.1}}
	var roundTrip []OCRHighlightBox
	if err := json.Unmarshal([]byte(marshalOCRBoxes(boxes)), &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(roundTrip) != 1 || roundTrip[0].Text != "a" || roundTrip[0].X != 0.1 {
		t.Fatalf("round trip = %+v", roundTrip)
	}
}

// TestOCRBoxCacheStoreRoundTrip locks the store contract behind the highlight
// path: identity resolution by snapshot file path, stored-box lookup that
// treats empty boxes_json as missing, write-through updating an existing worker row in
// place, and write-through inserting a text-less box-cache row when the
// worker never sampled the frame.
func TestOCRBoxCacheStoreRoundTrip(t *testing.T) {
	s := newTestStore(t)
	mustInsertKwSession(t, s, "sess-1")
	mustInsertKwSnapshot(t, s, 1, 5000, "/f1.jpg", "Slack", "", "")
	mustInsertKwSnapshot(t, s, 2, 9000, "/f2.jpg", "cmux", "", "")
	// Snapshot 1 has a legacy worker OCR row (text, no boxes).
	mustInsertOCRFrame(t, s, "sess-1", 1, "pinged yetone about the rollout")

	// Identity resolution by path; FrameTime is session-relative seconds.
	id, found, err := s.GetSnapshotOCRIdentityByFilePath("/f1.jpg")
	if err != nil || !found {
		t.Fatalf("identity(/f1.jpg): found=%v err=%v", found, err)
	}
	if id.SnapshotID != 1 || id.SessionID != "sess-1" || id.FrameTime != 5.0 {
		t.Fatalf("identity = %+v", id)
	}
	if _, found, err := s.GetSnapshotOCRIdentityByFilePath("/nope.jpg"); err != nil || found {
		t.Fatalf("identity(missing): found=%v err=%v, want false,nil", found, err)
	}

	// Legacy row: text present, boxes '' — lookup must report missing.
	if _, ok, err := s.GetStoredOCRBoxes("sess-1", 1); err != nil || ok {
		t.Fatalf("stored boxes before write: ok=%v err=%v, want miss", ok, err)
	}

	// Write-through onto the legacy row updates it in place (no second row).
	boxesJSON := marshalOCRBoxes([]OCRHighlightBox{{Text: "yetone", X: 0.1, Y: 0.2, Width: 0.3, Height: 0.05}})
	if err := s.SaveOCRBoxesForSnapshot(id, boxesJSON); err != nil {
		t.Fatalf("SaveOCRBoxesForSnapshot(update): %v", err)
	}
	got, ok, err := s.GetStoredOCRBoxes("sess-1", 1)
	if err != nil || !ok || got != boxesJSON {
		t.Fatalf("stored boxes after update: ok=%v err=%v got=%q", ok, err, got)
	}
	var rowCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM activity_ocr_frames WHERE session_id='sess-1' AND segment_id=1`).Scan(&rowCount)
	if rowCount != 1 {
		t.Fatalf("rows for snapshot 1 = %d, want the legacy row updated in place", rowCount)
	}
	var text string
	s.db.QueryRow(`SELECT text FROM activity_ocr_frames WHERE session_id='sess-1' AND segment_id=1`).Scan(&text)
	if text != "pinged yetone about the rollout" {
		t.Fatalf("legacy row text changed to %q", text)
	}

	// Snapshot 2 has no OCR row: write-through inserts a text-less cache row.
	id2, found, err := s.GetSnapshotOCRIdentityByFilePath("/f2.jpg")
	if err != nil || !found {
		t.Fatalf("identity(/f2.jpg): found=%v err=%v", found, err)
	}
	if err := s.SaveOCRBoxesForSnapshot(id2, ""); err != nil {
		t.Fatalf("SaveOCRBoxesForSnapshot(insert): %v", err)
	}
	got, ok, err = s.GetStoredOCRBoxes("sess-1", 2)
	if err != nil || !ok || got != "[]" {
		t.Fatalf("stored boxes after insert: ok=%v err=%v got=%q, want []", ok, err, got)
	}
	s.db.QueryRow(`SELECT text FROM activity_ocr_frames WHERE session_id='sess-1' AND segment_id=2`).Scan(&text)
	if text != "" {
		t.Fatalf("box-cache row text = %q, want empty (full-frame text must not widen the focus-crop signal)", text)
	}

	// A text-less box-cache row must NOT shadow the OCR-text fallback: the
	// detail popup's GetOCRTextBySnapshot still resolves snapshot 2 (whose
	// only own row is the empty cache row at sibling-distance 0) to the
	// nearest sibling that has real text.
	text2, err := s.GetOCRTextBySnapshot("sess-1", 2)
	if err != nil {
		t.Fatalf("GetOCRTextBySnapshot: %v", err)
	}
	if text2 != "pinged yetone about the rollout" {
		t.Fatalf("ocr text fallback = %q, want sibling text (box-cache row must not shadow it)", text2)
	}

	// SaveOCRFrame persists boxes for new worker rows end to end.
	if err := s.SaveOCRFrame(OCRFrameRow{
		SessionID: "sess-1", SegmentID: 2, FrameTime: 9, Text: "fresh worker text", Confidence: 0.9,
		BoxesJSON: boxesJSON,
	}); err != nil {
		t.Fatalf("SaveOCRFrame: %v", err)
	}
	got, ok, err = s.GetStoredOCRBoxes("sess-1", 2)
	if err != nil || !ok || got != boxesJSON {
		t.Fatalf("stored boxes from worker row: ok=%v err=%v got=%q", ok, err, got)
	}
}
