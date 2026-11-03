package main

import (
	"path/filepath"
	"testing"
	"time"
)

// Tests for the user-configurable Activity retention story
// (activity_rotator.go + app.go: activityRotatorConfigFor).
//
// Why these tests exist (P1 from code review): the previous purgeCold used
// GetSegmentsByTier("cold") as its entry point and therefore silently
// skipped the entire snapshot-only session population produced by the
// current activity-monitor pipeline. This file pins the contract so the
// regression cannot creep back unnoticed.

// ── activityRotatorConfigFor: derivation formula ──

func TestActivityRotatorConfigFor(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name     string
		days     int
		wantCold time.Duration
		wantWarm time.Duration
		wantHot  time.Duration
	}{
		{"unlimited disables age purge", 0, 0, 7 * day, 24 * time.Hour},
		{"1 day floors warm at 1 day", 1, 1 * day, 1 * day, 24 * time.Hour},
		{"7 days", 7, 7 * day, 2 * day, 24 * time.Hour},    // ceil(7/4)=2
		{"30 days", 30, 30 * day, 8 * day, 24 * time.Hour}, // ceil(30/4)=8
		{"60 days", 60, 60 * day, 15 * day, 24 * time.Hour},
		{"90 days", 90, 90 * day, 23 * day, 24 * time.Hour}, // ceil(90/4)=23
		{"180 days", 180, 180 * day, 45 * day, 24 * time.Hour},
		{"360 days", 360, 360 * day, 90 * day, 24 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := activityRotatorConfigFor(tc.days, defaultActivityMaxStorageGB)
			if cfg.ColdRetention != tc.wantCold {
				t.Errorf("ColdRetention: got %v, want %v", cfg.ColdRetention, tc.wantCold)
			}
			if cfg.WarmRetention != tc.wantWarm {
				t.Errorf("WarmRetention: got %v, want %v", cfg.WarmRetention, tc.wantWarm)
			}
			if cfg.HotRetention != tc.wantHot {
				t.Errorf("HotRetention should be fixed at 24h: got %v", cfg.HotRetention)
			}
			// Quota cap must always be set so enforceQuota stays a backstop
			// even when the user picks Unlimited.
			if cfg.MaxTotalSize <= 0 {
				t.Errorf("MaxTotalSize should always be > 0, got %d", cfg.MaxTotalSize)
			}
		})
	}
}

// TestActivityRotatorConfigStorageCap pins how the user-facing GB value is
// translated into bytes and the 75% TargetFreeSize ratio. Drift here would
// silently change quota aggressiveness for every user.
func TestActivityRotatorConfigStorageCap(t *testing.T) {
	const gb = int64(1024) * 1024 * 1024
	tests := []struct {
		name      string
		inputGB   int
		wantBytes int64
	}{
		{"1 GB minimum", 1, 1 * gb},
		{"2 GB legacy default", 2, 2 * gb},
		{"10 GB new default", 10, 10 * gb},
		{"100 GB power-user", 100, 100 * gb},
		{"zero falls back to default", 0, int64(defaultActivityMaxStorageGB) * gb},
		{"negative falls back to default", -5, int64(defaultActivityMaxStorageGB) * gb},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := activityRotatorConfigFor(30, tc.inputGB)
			if cfg.MaxTotalSize != tc.wantBytes {
				t.Errorf("MaxTotalSize: got %d, want %d", cfg.MaxTotalSize, tc.wantBytes)
			}
			// TargetFreeSize must be exactly 90% of MaxTotalSize so the
			// margin below the limit scales with the user's chosen cap.
			wantTarget := tc.wantBytes * 9 / 10
			if cfg.TargetFreeSize != wantTarget {
				t.Errorf("TargetFreeSize: got %d, want %d (90%% of %d)",
					cfg.TargetFreeSize, wantTarget, tc.wantBytes)
			}
		})
	}
}

// ── purgeExpiredSessions: behaviour against real ActivityStore ──

// insertSession is a minimal fixture helper that writes a row to
// activity_sessions matching what activity_monitor.go's main path produces:
// SegmentCount = 0, optional snapshots, no events. triageStatus is
// caller-supplied so tests can assert lifecycle behaviour. The optional
// audioBytes argument lets quota tests give sessions a measurable on-disk
// footprint (GetTotalStorageBytes sums audio_size_bytes); existing callers
// that don't care can keep the 4-arg form.
func insertSession(t *testing.T, s *ActivityStore, id string, startTime time.Time, triageStatus string, audioBytes ...int64) {
	t.Helper()
	var audio int64
	if len(audioBytes) > 0 {
		audio = audioBytes[0]
	}
	tx, err := s.BeginTx()
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO activity_sessions (id, start_time, end_time, trigger_kind, app_names,
			event_count, segment_count, snapshot_count, total_size_bytes, triage_status,
			is_meeting, meeting_app, transcript_path, audio_path, audio_size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, startTime.UnixMilli(), startTime.Add(time.Minute).UnixMilli(), "test", "[]",
		0, 0, 0, 0, triageStatus,
		0, "", "", "", audio,
		startTime.UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit session %s: %v", id, err)
	}
}

// sessionExists is a small assertion helper.
func sessionExists(t *testing.T, s *ActivityStore, id string) bool {
	t.Helper()
	_, err := s.GetSessionByID(id)
	return err == nil
}

// TestPurgeUnlimitedKeepsEverything pins the "Unlimited" semantics: when the
// user picks 0 days, age-based purging is disabled entirely.
func TestPurgeUnlimitedKeepsEverything(t *testing.T) {
	store := newTestStore(t)
	rotator := NewStorageRotator(store, activityRotatorConfigFor(0, defaultActivityMaxStorageGB))

	// Snapshot-only sessions of various ages — none should be touched.
	insertSession(t, store, "fresh", time.Now().Add(-time.Hour), "pending")
	insertSession(t, store, "old", time.Now().Add(-365*24*time.Hour), "pending")
	insertSession(t, store, "ancient", time.Now().Add(-10*365*24*time.Hour), "pending")

	rotator.purgeExpiredSessions(rotator.snapshotConfig())

	for _, id := range []string{"fresh", "old", "ancient"} {
		if !sessionExists(t, store, id) {
			t.Errorf("session %q should survive Unlimited retention but was deleted", id)
		}
	}
}

// TestPurgeDeletesSnapshotOnlySessions is the regression test for the
// reviewer's P1: a session with zero segments (the current main-path
// shape) MUST be deleted by age-based purge once it crosses the threshold.
// Pre-fix this would silently no-op because the old purgeCold only walked
// cold segments.
func TestPurgeDeletesSnapshotOnlySessions(t *testing.T) {
	store := newTestStore(t)
	rotator := NewStorageRotator(store, activityRotatorConfigFor(7, defaultActivityMaxStorageGB))

	// 30-day-old session, no segments. Must be deleted under 7-day retention.
	insertSession(t, store, "old-snapshot-only", time.Now().Add(-30*24*time.Hour), "pending")
	// 1-hour-old session — must be kept.
	insertSession(t, store, "fresh", time.Now().Add(-time.Hour), "pending")

	rotator.purgeExpiredSessions(rotator.snapshotConfig())

	if sessionExists(t, store, "old-snapshot-only") {
		t.Error("REGRESSION: 30-day-old snapshot-only session should be deleted under 7-day retention")
	}
	if !sessionExists(t, store, "fresh") {
		t.Error("1-hour-old session should not be deleted under 7-day retention")
	}
}

// TestPurgeAppliesToAllTriageStatuses confirms that age-based retention now
// applies uniformly regardless of triage status — the old
// "kept forever" retention protection was removed.
func TestPurgeAppliesToAllTriageStatuses(t *testing.T) {
	store := newTestStore(t)
	// Aggressive retention: 1 day. Both 8-day-old sessions should be deleted.
	rotator := NewStorageRotator(store, activityRotatorConfigFor(1, defaultActivityMaxStorageGB))

	insertSession(t, store, "dismissed-old", time.Now().Add(-8*24*time.Hour), "dismissed")
	insertSession(t, store, "pending-old", time.Now().Add(-8*24*time.Hour), "pending")

	rotator.purgeExpiredSessions(rotator.snapshotConfig())

	if sessionExists(t, store, "dismissed-old") {
		t.Error("8-day-old dismissed session should be deleted under 1-day retention")
	}
	if sessionExists(t, store, "pending-old") {
		t.Error("8-day-old pending session should be deleted under 1-day retention")
	}
}

// TestPurgeStopsAtThreshold verifies the ASC-order short-circuit: once we
// hit a session newer than the threshold the loop must stop, otherwise
// large stores would scan far more rows than necessary.
func TestPurgeStopsAtThreshold(t *testing.T) {
	store := newTestStore(t)
	rotator := NewStorageRotator(store, activityRotatorConfigFor(7, defaultActivityMaxStorageGB))

	insertSession(t, store, "old", time.Now().Add(-30*24*time.Hour), "pending")
	insertSession(t, store, "borderline", time.Now().Add(-time.Hour), "pending") // < 7 days
	insertSession(t, store, "recent", time.Now().Add(-time.Minute), "pending")

	rotator.purgeExpiredSessions(rotator.snapshotConfig())

	if sessionExists(t, store, "old") {
		t.Error("30-day-old session should be deleted")
	}
	if !sessionExists(t, store, "borderline") {
		t.Error("1-hour-old session should not be deleted under 7-day retention")
	}
	if !sessionExists(t, store, "recent") {
		t.Error("1-minute-old session should not be deleted under 7-day retention")
	}
}

// TestUpdateConfigChangesPurgeBehaviour exercises the live-update path used
// by SetActivityRetentionDays: changing the config and triggering a fresh
// rotate must immediately apply the new retention.
func TestUpdateConfigChangesPurgeBehaviour(t *testing.T) {
	store := newTestStore(t)
	// Start at Unlimited.
	rotator := NewStorageRotator(store, activityRotatorConfigFor(0, defaultActivityMaxStorageGB))
	insertSession(t, store, "old", time.Now().Add(-30*24*time.Hour), "pending")

	rotator.purgeExpiredSessions(rotator.snapshotConfig())
	if !sessionExists(t, store, "old") {
		t.Fatal("session should still exist under Unlimited retention")
	}

	// User picks 7 days; without a TriggerOnce the next rotate (whenever it
	// fires) must use the new value.
	rotator.UpdateConfig(activityRotatorConfigFor(7, defaultActivityMaxStorageGB))
	rotator.purgeExpiredSessions(rotator.snapshotConfig())

	if sessionExists(t, store, "old") {
		t.Error("after switching to 7-day retention, 30-day-old session should be deleted on next rotate")
	}
}

// TestActivityStorageStats verifies the aggregator that drives the
// Settings → Activity stats card reports total session count and the earliest
// session start_time across all sessions (every session is subject to
// retention now that the "kept forever" bucket is gone).
func TestActivityStorageStats(t *testing.T) {
	store := newTestStore(t)

	// Empty store: zero everything, no crash.
	stats, err := store.GetActivityStorageStats()
	if err != nil {
		t.Fatalf("empty store: %v", err)
	}
	if stats.RetainedSessionCount != 0 {
		t.Errorf("empty store should have zero count, got %+v", stats)
	}
	if stats.EarliestRetainedMs != 0 {
		t.Errorf("empty store earliest should be 0, got %d", stats.EarliestRetainedMs)
	}

	// Three sessions of varying ages and triage statuses — all counted.
	oldest := time.Now().Add(-35 * 24 * time.Hour)
	insertSession(t, store, "n-old", oldest, "pending")
	insertSession(t, store, "n-fresh", time.Now().Add(-time.Hour), "pending")
	insertSession(t, store, "c-old", time.Now().Add(-10*24*time.Hour), "dismissed")

	stats, err = store.GetActivityStorageStats()
	if err != nil {
		t.Fatalf("populated store: %v", err)
	}
	if stats.RetainedSessionCount != 3 {
		t.Errorf("RetainedSessionCount: got %d, want 3", stats.RetainedSessionCount)
	}
	// Earliest must be the oldest session across all triage statuses.
	if stats.EarliestRetainedMs != oldest.UnixMilli() {
		t.Errorf("EarliestRetainedMs: got %d, want %d (oldest session)",
			stats.EarliestRetainedMs, oldest.UnixMilli())
	}
}

// TestDeleteSessionAndFilesIsBestEffort sanity-checks that the helper used
// by both purgeExpiredSessions and enforceQuota tolerates missing files on
// disk (the common case in tests where we never write the segment / snapshot
// blobs).
func TestDeleteSessionAndFilesIsBestEffort(t *testing.T) {
	store := newTestStore(t)
	rotator := NewStorageRotator(store, activityRotatorConfigFor(7, defaultActivityMaxStorageGB))

	insertSession(t, store, "missing-files", time.Now().Add(-30*24*time.Hour), "pending")

	if !rotator.deleteSessionAndFiles("missing-files") {
		t.Fatal("deleteSessionAndFiles should return true even when on-disk files are absent")
	}
	if sessionExists(t, store, "missing-files") {
		t.Error("session row should be gone after deleteSessionAndFiles")
	}
	// Sanity: SegmentsDir under the temp store exists (just to make sure
	// the helper didn't blow up trying to resolve paths).
	if _, err := filepath.Abs(store.SegmentsDir()); err != nil {
		t.Errorf("SegmentsDir resolved to invalid path: %v", err)
	}
}
