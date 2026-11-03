package main

import (
	"encoding/json"
	"testing"

	"wails-gui/internal/platform/activitymon/appparser"
)

// TestGetSemanticEventTimeline_AppResolution locks both app-badge paths after the
// perf optimization: new rows read the app from source_refs.SnapshotApps (no
// parsed_json), while legacy rows lacking SnapshotApps must still parse
// parsed_json so a wrong/blank raw app is corrected via its bundleId.
func TestGetSemanticEventTimeline_AppResolution(t *testing.T) {
	s := newTestStore(t)

	// snapshot 1: legacy-style — the raw owner is "Discord" (wrong), but the
	// parsed content carries Slack's bundleId. The old code resolved this to
	// "Slack"; the fast path must not regress it.
	pj, err := json.Marshal(appparser.ParsedContent{
		Metadata: map[string]string{"bundleId": "com.tinyspeck.slackmacgap"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustInsertSnapshot(t, s, 1, 1000, "/p1.jpg", "Discord", string(pj))
	// snapshot 2: no parsed content, plain raw owner.
	mustInsertSnapshot(t, s, 2, 2000, "/p2.jpg", "cmux", "")
	// snapshot 3: raw owner "Discord", no bundleId — used to prove SnapshotApps
	// wins over any raw/parsed value on the fast path.
	mustInsertSnapshot(t, s, 3, 3000, "/p3.jpg", "Discord", "")

	// Legacy event: source_refs WITHOUT snapshot_apps -> must parse parsed_json,
	// so snapshot 1 resolves to "Slack" (from bundleId), not the raw "Discord".
	mustInsertEvent(t, s, "ev-legacy", `{"snapshot_ids":[1,2]}`)
	frames, err := s.GetSemanticEventTimeline("ev-legacy")
	if err != nil {
		t.Fatalf("GetSemanticEventTimeline(legacy): %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("legacy: got %d frames, want 2", len(frames))
	}
	if frames[0].App != "Slack" {
		t.Errorf("legacy frame 1 app = %q, want %q (bundleId correction must survive)", frames[0].App, "Slack")
	}
	if frames[1].App != "cmux" {
		t.Errorf("legacy frame 2 app = %q, want %q", frames[1].App, "cmux")
	}

	// New event: source_refs WITH snapshot_apps -> fast path uses them verbatim,
	// even though snapshot 3's raw owner is "Discord".
	mustInsertEvent(t, s, "ev-new", `{"snapshot_ids":[3],"snapshot_apps":["Telegram"]}`)
	frames2, err := s.GetSemanticEventTimeline("ev-new")
	if err != nil {
		t.Fatalf("GetSemanticEventTimeline(new): %v", err)
	}
	if len(frames2) != 1 {
		t.Fatalf("new: got %d frames, want 1", len(frames2))
	}
	if frames2[0].App != "Telegram" {
		t.Errorf("new frame app = %q, want %q (SnapshotApps must win)", frames2[0].App, "Telegram")
	}
}

func mustInsertSnapshot(t *testing.T, s *ActivityStore, id, ts int64, path, app, parsedJSON string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO activity_snapshots (id, timestamp, file_path, app, parsed_json) VALUES (?, ?, ?, ?, ?)`,
		id, ts, path, app, parsedJSON,
	); err != nil {
		t.Fatalf("insert snapshot %d: %v", id, err)
	}
}

func mustInsertEvent(t *testing.T, s *ActivityStore, id, sourceRefs string) {
	t.Helper()
	if err := s.InsertSemanticEvent(SemanticEventRow{
		ID:         id,
		Title:      "test event",
		Category:   "connect",
		StartMs:    1000,
		EndMs:      3000,
		SourceRefs: sourceRefs,
		CreatedAt:  1,
		UpdatedAt:  1,
	}); err != nil {
		t.Fatalf("insert event %s: %v", id, err)
	}
}
