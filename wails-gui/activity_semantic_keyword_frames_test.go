package main

import (
	"testing"
	"time"
)

// TestGetSemanticEventKeywordFrames locks the relevance-scan semantics: a
// frame matches when ANY stored text source (capture-time OCR rows, ax_text,
// parsed_json) contains a keyword, matching is case-insensitive and
// CJK-space-insensitive (mirroring GetOCRHighlightBoxes), frames without any
// stored text never match, and there is no sibling-OCR fallback.
func TestGetSemanticEventKeywordFrames(t *testing.T) {
	s := newTestStore(t)

	mustInsertKwSession(t, s, "sess-1")
	// snapshot 1: keyword only in a stored OCR frame row.
	mustInsertKwSnapshot(t, s, 1, 1000, "/f1.jpg", "cmux", "", "")
	mustInsertOCRFrame(t, s, "sess-1", 1, "pinged yetone about the rollout")
	// snapshot 2: keyword only in ax_text, different case.
	mustInsertKwSnapshot(t, s, 2, 2000, "/f2.jpg", "Slack", "Message YeTone here", "")
	// snapshot 3: keyword only inside parsed_json.
	mustInsertKwSnapshot(t, s, 3, 3000, "/f3.jpg", "Slack", "", `{"messages":[{"sender":"yetone"}]}`)
	// snapshot 4: has OCR text but no keyword — must NOT match, and must NOT
	// borrow snapshot 1's text the way GetOCRTextBySnapshot's fallback would.
	mustInsertKwSnapshot(t, s, 4, 4000, "/f4.jpg", "cmux", "", "")
	mustInsertOCRFrame(t, s, "sess-1", 4, "completely unrelated text")
	// snapshot 5: CJK text with an OCR-style space; keyword has none.
	mustInsertKwSnapshot(t, s, 5, 5000, "/f5.jpg", "WeChat", "讨论 深度 学习 模型", "")
	// snapshot 6: LATE visual (OCR) match — must sort ahead of the earlier
	// ax/parsed-only matches despite its timestamp.
	mustInsertKwSnapshot(t, s, 6, 6000, "/f6.jpg", "cmux", "", "")
	mustInsertOCRFrame(t, s, "sess-1", 6, "review from yetone")
	// snapshot 7: keyword appears ONLY in non-content JSON (a metadata value),
	// not in any user-visible field — must NOT match, or structural JSON would
	// mark frames the keyword never visibly touched.
	mustInsertKwSnapshot(t, s, 7, 7000, "/f7.jpg", "Slack", "",
		`{"title":"unrelated","metadata":{"bundleId":"com.yetone.app"}}`)
	// Poison row: an OCR frame in ANOTHER session whose segment_id (a legacy
	// activity_segments.id) numerically collides with snapshot 4. The
	// (session_id, segment_id) join must keep it from marking snapshot 4.
	mustInsertKwSession(t, s, "sess-other")
	mustInsertOCRFrame(t, s, "sess-other", 4, "yetone mentioned elsewhere")

	mustInsertEvent(t, s, "ev-kw",
		`{"snapshot_ids":[1,2,3,4,5,6,7],"session_ids":["sess-1"],"snapshot_apps":["cmux","Slack","Slack","cmux","WeChat","cmux","Slack"]}`)

	frames, err := s.GetSemanticEventKeywordFrames("ev-kw", []string{"yetone"})
	if err != nil {
		t.Fatalf("GetSemanticEventKeywordFrames: %v", err)
	}
	// Visual (OCR-text) matches lead regardless of timestamp; ax/parsed-only
	// matches follow in time order.
	wantPaths := []string{"/f1.jpg", "/f6.jpg", "/f2.jpg", "/f3.jpg"}
	wantVisual := []bool{true, true, false, false}
	if len(frames) != len(wantPaths) {
		t.Fatalf("got %d frames %+v, want %d", len(frames), frames, len(wantPaths))
	}
	for i, want := range wantPaths {
		if frames[i].Path != want {
			t.Errorf("frame %d path = %q, want %q (visual-first, then time)", i, frames[i].Path, want)
		}
		if frames[i].Visual != wantVisual[i] {
			t.Errorf("frame %d (%s) visual = %v, want %v", i, frames[i].Path, frames[i].Visual, wantVisual[i])
		}
	}
	if frames[0].App != "cmux" || frames[2].App != "Slack" {
		t.Errorf("snapshot_apps not applied: got %q/%q", frames[0].App, frames[2].App)
	}

	// CJK-space-insensitive match, and multi-keyword = ANY semantics.
	frames, err = s.GetSemanticEventKeywordFrames("ev-kw", []string{"深度学习", "no-such-term"})
	if err != nil {
		t.Fatalf("GetSemanticEventKeywordFrames(cjk): %v", err)
	}
	if len(frames) != 1 || frames[0].Path != "/f5.jpg" {
		t.Fatalf("cjk: got %+v, want only /f5.jpg", frames)
	}

	// Blank keywords are dropped; all-blank input returns empty without error.
	frames, err = s.GetSemanticEventKeywordFrames("ev-kw", []string{"  ", ""})
	if err != nil {
		t.Fatalf("GetSemanticEventKeywordFrames(blank): %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("blank keywords: got %+v, want none", frames)
	}
}

func mustInsertKwSession(t *testing.T, s *ActivityStore, id string) {
	t.Helper()
	if _, err := s.db.Exec(`
		INSERT INTO activity_sessions (
			id, start_time, end_time, trigger_kind, app_names,
			event_count, segment_count, snapshot_count, created_at
		) VALUES (?, 0, 1, 'test', '[]', 0, 0, 0, ?)`,
		id, time.Now().UnixMilli(),
	); err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
}

func mustInsertKwSnapshot(t *testing.T, s *ActivityStore, id, ts int64, path, app, axText, parsedJSON string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO activity_snapshots (id, session_id, timestamp, file_path, app, ax_text, parsed_json)
		 VALUES (?, 'sess-1', ?, ?, ?, ?, ?)`,
		id, ts, path, app, axText, parsedJSON,
	); err != nil {
		t.Fatalf("insert snapshot %d: %v", id, err)
	}
}

func mustInsertOCRFrame(t *testing.T, s *ActivityStore, sessionID string, snapshotID int64, text string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO activity_ocr_frames (session_id, segment_id, frame_time, text, confidence)
		 VALUES (?, ?, 0, ?, 1.0)`,
		sessionID, snapshotID, text,
	); err != nil {
		t.Fatalf("insert ocr frame for snapshot %d: %v", snapshotID, err)
	}
}
