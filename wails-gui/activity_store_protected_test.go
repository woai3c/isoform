package main

import (
	"testing"
)

// insertTestSession writes a minimal session row plus one event, snapshot,
// and segment, so the read-policy tests can observe child-row filtering.
func insertTestSession(t *testing.T, s *ActivityStore, id, triageStatus string, startMs int64) {
	t.Helper()
	if _, err := s.db.Exec(`
		INSERT INTO activity_sessions
			(id, start_time, end_time, trigger_kind, app_names, event_count,
			 segment_count, snapshot_count, total_size_bytes, triage_status,
			 ocr_status, created_at)
		VALUES (?, ?, ?, 'preview', '["TestApp"]', 1, 1, 1, 0, ?, 'pending', ?)`,
		id, startMs, startMs+60_000, triageStatus, startMs); err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO activity_events (session_id, timestamp, kind, data, sort_order)
		VALUES (?, ?, 'app_switch', '{"appName":"TestApp"}', 0)`,
		id, startMs); err != nil {
		t.Fatalf("insert event for %s: %v", id, err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO activity_snapshots (session_id, timestamp, file_path, width, height, size_bytes, storage_tier)
		VALUES (?, ?, 'x.jpg', 100, 100, 1, 'hot')`,
		id, startMs); err != nil {
		t.Fatalf("insert snapshot for %s: %v", id, err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO activity_segments (session_id, start_time, end_time, file_path, width, height, fps, codec, size_bytes, storage_tier)
		VALUES (?, ?, ?, 'x.mp4', 100, 100, 1, 'h264', 1, 'hot')`,
		id, startMs, startMs+60_000); err != nil {
		t.Fatalf("insert segment for %s: %v", id, err)
	}
}

// TestProtectedSessionReadPolicy verifies the store-layer invariant: default
// getters never return protected sessions' data; IncludingProtected variants
// do. This is the enforcement point every AI/index consumer relies on.
func TestProtectedSessionReadPolicy(t *testing.T) {
	s := newTestStore(t)
	insertTestSession(t, s, "normal-1", "pending", 1_000)
	insertTestSession(t, s, "protected-1", "protected", 2_000)

	t.Run("GetSessions excludes protected", func(t *testing.T) {
		rows, err := s.GetSessions(10, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ID == "protected-1" {
				t.Error("protected session returned by default GetSessions")
			}
		}
		if len(rows) != 1 {
			t.Errorf("want 1 session, got %d", len(rows))
		}
	})

	t.Run("GetFilteredSessions excludes protected, IncludingProtected returns it", func(t *testing.T) {
		def, err := s.GetFilteredSessions(10, 0, 0, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(def) != 1 || def[0].ID != "normal-1" {
			t.Errorf("default: want only normal-1, got %d rows", len(def))
		}
		all, err := s.GetFilteredSessionsIncludingProtected(10, 0, 0, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 {
			t.Errorf("IncludingProtected: want 2 rows, got %d", len(all))
		}
	})

	t.Run("GetSessionsByIDs map-gap for protected", func(t *testing.T) {
		m, err := s.GetSessionsByIDs([]string{"normal-1", "protected-1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m["protected-1"]; ok {
			t.Error("protected session present in default GetSessionsByIDs")
		}
		if _, ok := m["normal-1"]; !ok {
			t.Error("normal session missing")
		}
		all, err := s.GetSessionsByIDsIncludingProtected([]string{"protected-1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := all["protected-1"]; !ok {
			t.Error("IncludingProtected variant should return the protected session")
		}
	})

	t.Run("per-session child getters refuse protected", func(t *testing.T) {
		if evts, _ := s.GetSessionEvents("protected-1"); len(evts) != 0 {
			t.Error("GetSessionEvents leaked protected events")
		}
		if snaps, _ := s.GetSessionSnapshots("protected-1"); len(snaps) != 0 {
			t.Error("GetSessionSnapshots leaked protected snapshots")
		}
		if segs, _ := s.GetSessionSegments("protected-1"); len(segs) != 0 {
			t.Error("GetSessionSegments leaked protected segments")
		}
		// Normal session still readable.
		if evts, _ := s.GetSessionEvents("normal-1"); len(evts) != 1 {
			t.Error("normal session events should be readable")
		}
		// IncludingProtected variants return the data (UI / retention).
		if evts, _ := s.GetSessionEventsIncludingProtected("protected-1"); len(evts) != 1 {
			t.Error("IncludingProtected events variant should return data")
		}
		if snaps, _ := s.GetSessionSnapshotsIncludingProtected("protected-1"); len(snaps) != 1 {
			t.Error("IncludingProtected snapshots variant should return data")
		}
		if segs, _ := s.GetSessionSegmentsIncludingProtected("protected-1"); len(segs) != 1 {
			t.Error("IncludingProtected segments variant should return data")
		}
	})

	t.Run("onboarding preview getter excludes protected", func(t *testing.T) {
		rows, err := s.GetSessionsByTriggerCreatedAfter("preview", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ID == "protected-1" {
				t.Error("protected session returned to onboarding preview")
			}
		}
	})

	t.Run("semantic pending excludes protected", func(t *testing.T) {
		rows, err := s.GetSemanticPendingSessions()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ID == "protected-1" {
				t.Error("protected session returned to semantic loop")
			}
		}
	})

	t.Run("retention sweeps still see protected", func(t *testing.T) {
		rows, err := s.GetSessionsByAge()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range rows {
			if r.ID == "protected-1" {
				found = true
			}
		}
		if !found {
			t.Error("retention sweep must see protected sessions to purge their files")
		}
	})
}
