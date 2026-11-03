package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// TestStorageCleanupOnRealFolder runs the storage part of the cleanup once on
// a real data folder that an installed older build keeps using.
//
// What it changes, and nothing else:
//   - files: screenshot JPEGs no row refers to are removed (the startup
//     reconcile's rule, files older than a day); when the folder has a
//     screenshot time limit saved (screenshot_days), images older than it
//     are removed and their rows keep their text; cold screenshots larger
//     than 2 MP are rewritten in place, same name, smaller; call WAVs whose
//     transcript is final become <name>.opus and the WAV is removed;
//   - activity_snapshots rows: storage_tier moves hot -> warm -> cold by age
//     (the app's own tiering); rows whose JPEG is gone, or whose image the
//     screenshot time limit removed, get tier "missing" and size 0, keeping
//     their text; shrunk rows get their new size_bytes, width and height;
//   - activity_sessions rows: snapshot_count and total_size_bytes are
//     recounted for sessions with changed snapshots; audio_path and
//     audio_size_bytes of converted calls point at the .opus file;
//   - a VACUUM, only when it gives back 64 MB or more and the disk has room.
//
// What it never does: run this build's migrations (no new tables, no
// drop_removed_agency_tables, so the older build still finds its schema),
// touch retired tables or the Agency planning runs, remove recordings that
// have no row (the call pipeline's recovery may still save them), remove
// anything for the storage limit or the recording time limit, or change the
// user's settings.
//
// Before any change it refuses to start unless: the folder has its
// screenshot and audio folders; no other process has activity.db open and
// Yansu.app is not running (checked again after any wait for power, right
// before the backup, and before every phase, stopping if either appears);
// the Mac is on mains power and not in Low Power Mode (it waits and says
// so, and pauses the same way between files if power is lost during the
// run); the disk has room. Then it copies activity.db with its -wal and
// -shm files, plus a list of the recordings it converts, to the backup
// folder. It stops between files (and between phases) on SIGINT or
// SIGTERM; every replacement is atomic.
//
// It never runs by default: it needs YANSU_STORAGE_REAL_DIR (a ".yansu-agent"
// folder), YANSU_STORAGE_REAL_PHASE (plan or run), YANSU_STORAGE_REAL_CONSENT
// (the date the user said yes), YANSU_STORAGE_REAL_BACKUP (the backup folder,
// for run) and YANSU_TEST_FFMPEG. Run it with HOME pointing elsewhere so the
// test binary's own start-up writes nothing into the folder, and under
// `nice -n 19` (ffmpeg is started at nice 19 too). Unlike the app's
// background pass it does not use background QoS: on a busy Mac that made
// the shrinking pass take days instead of hours.
func TestStorageCleanupOnRealFolder(t *testing.T) {
	dir := os.Getenv("YANSU_STORAGE_REAL_DIR")
	phase := os.Getenv("YANSU_STORAGE_REAL_PHASE")
	if dir == "" || phase == "" || os.Getenv("YANSU_STORAGE_REAL_CONSENT") != "2026-10-16" {
		t.Skip("needs YANSU_STORAGE_REAL_DIR, YANSU_STORAGE_REAL_PHASE and the user's consent")
	}
	dir = filepath.Clean(dir)
	if filepath.Base(dir) != ".yansu-agent" {
		t.Fatalf("not a .yansu-agent folder: %s", dir)
	}
	for _, sub := range []string{"activity/snapshots", "activity/audio", "activity.db"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err != nil {
			t.Fatalf("refusing: %s is missing (%v)", sub, err)
		}
	}
	ffmpeg := os.Getenv("YANSU_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Fatal("refusing: YANSU_TEST_FFMPEG is not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stopped := func() bool { return ctx.Err() != nil }
	// Between files: pause while the Mac is on battery or in Low Power Mode
	// (checked at most every 10 s), then report whether to stop.
	var lastPowerCheck time.Time
	betweenFiles := func() bool {
		if time.Since(lastPowerCheck) >= 10*time.Second {
			lastPowerCheck = time.Now()
			if storageMediaDeferReason() != "" {
				waitForMainsPower(t, ctx)
			}
		}
		return stopped()
	}
	// Another holder of the database or a running Yansu.app ends the run.
	held := func(when string) bool {
		if holders := activityDatabaseHolders(dir); holders != "" {
			t.Errorf("stopping %s: activity.db is open elsewhere or Yansu.app is running:\n%s", when, holders)
			return true
		}
		return false
	}

	var backup string
	if phase == "run" {
		backup = filepath.Clean(os.Getenv("YANSU_STORAGE_REAL_BACKUP"))
		if backup == "." || backup == "" || strings.HasPrefix(backup, dir+string(filepath.Separator)) {
			t.Fatal("refusing: YANSU_STORAGE_REAL_BACKUP must name a folder outside the data folder")
		}
		if held("before starting") {
			t.FailNow()
		}
		waitForMainsPower(t, ctx)
		if stopped() {
			t.Fatal("stopped before any change")
		}
		if held("after waiting for power") {
			t.FailNow()
		}
	}

	// The user's saved settings, read from the folder, unchanged.
	var saved activityRetentionConfig
	if data, err := os.ReadFile(filepath.Join(dir, "activity-config.json")); err == nil {
		_ = json.Unmarshal(data, &saved)
	}
	gbLimit := saved.MaxStorageGB
	if gbLimit < minActivityMaxStorageGB {
		gbLimit = defaultActivityMaxStorageGB
	}
	cfg := activityRotatorConfigFor(saved.RetentionDays, gbLimit)
	if saved.ScreenshotDays != nil && *saved.ScreenshotDays > 0 {
		cfg.ScreenshotRetention = time.Duration(*saved.ScreenshotDays) * 24 * time.Hour
	}

	if phase == "run" {
		// Copy the database before this test opens it (no process holds it).
		if held("before the backup") {
			t.FailNow()
		}
		if err := backupActivityDatabase(dir, backup); err != nil {
			t.Fatalf("backup: %v", err)
		}
		fmt.Printf("Backed up activity.db, -wal and -shm to %s\n", backup)
	}

	store := openActivityStoreWithoutMigrations(t, dir)
	defer store.Close()
	r := NewStorageRotator(store, cfg)
	r.screenshotRetention = func() time.Duration { return cfg.ScreenshotRetention }
	r.ffmpegPath = func() string { return ffmpeg }
	// Final transcript, without the app's settings: the after-call engine
	// transcribed it (sidecar), or it was saved 7 days ago. Qwen3 never ran
	// for an older build's folder, so calls saved within the last 7 days
	// stay WAV.
	r.recordingFinal = func(m audioMedia) bool {
		if !m.Transcribed {
			return false
		}
		if readMeetingWAVMeta(store.AudioDir(), m.RelPath).TranscribedBy == meetingTranscribedByAfterCall {
			return true
		}
		savedAt := m.SavedAt
		if savedAt <= 0 {
			savedAt = m.StartTime
		}
		return time.Since(time.UnixMilli(savedAt)) >= meetingTrackMaxAge
	}
	heldBack := 0
	if recordings, err := store.sessionsWithAudio(math.MaxInt64); err == nil {
		for _, m := range recordings {
			if m.Transcribed && strings.EqualFold(filepath.Ext(m.RelPath), ".wav") && !r.recordingFinal(m) {
				heldBack++
			}
		}
	}
	fmt.Printf("Folder %s: retention %d day(s), limit %d GB, screenshot time limit %v; ffmpeg %s\n",
		dir, saved.RetentionDays, gbLimit, cfg.ScreenshotRetention, ffmpeg)

	plan, err := r.PlanCleanup()
	if err != nil {
		t.Fatal(err)
	}
	// The report lists only what this run does: no recordings without a row,
	// nothing for the storage limit or the recording time limit, and the
	// database only as free pages a VACUUM gives back (not in the total).
	free, _, _ := store.databaseFreeBytes()
	plan.DatabaseSpace = StorageCleanupItem{Bytes: free}
	plan.UnlinkedRecordings, plan.OverLimitScreenshots, plan.OverLimitRecordings = StorageCleanupItem{}, StorageCleanupItem{}, StorageCleanupItem{}
	plan.ExpiredSessions, plan.LimitUnreachable, plan.KeptBytes = StorageCleanupItem{}, false, 0
	plan.FreedBytes = plan.UnlinkedScreenshots.freed(false) + plan.ExpiredScreenshots.freed(false) +
		plan.ShrinkScreenshots.freed(true) + plan.CompressRecordings.freed(true)
	plan.AfterBytes = max(plan.Usage.TotalBytes-plan.FreedBytes, 0)
	fmt.Print(formatCleanupPlan(plan))
	fmt.Printf("Recordings held back as WAV (transcript not final yet): %d\n", heldBack)
	if phase != "run" {
		return
	}
	if err := writeConversionManifest(backup, store, r); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	start := time.Now()
	now := time.Now()
	before := r.measure()
	step := func(name string) { fmt.Printf("[%s] %s\n", time.Since(start).Round(time.Second), name) }

	// A phase starts only when the run should go on: no signal, no other
	// holder of the database, no Yansu.app.
	proceed := func(name string) bool {
		if betweenFiles() || held("before "+name) {
			return false
		}
		step(name)
		return true
	}

	if proceed("unreferenced screenshots (recordings without a row are kept)") {
		rec, err := store.ReconcileSnapshotFilesExcept(now.Add(-orphanSnapshotMinAge).UnixMilli(),
			func(string) bool { return betweenFiles() })
		if err != nil {
			t.Errorf("screenshot reconcile: %v", err)
		}
		fmt.Printf("  screenshots: %d removed, %d rows marked image-missing\n", rec.FilesRemoved, rec.Marked)
	}

	if proceed("tiers and screenshot time limit (nothing removed for the storage limit)") {
		r.rotateHotToWarm(cfg)
		r.rotateWarmToCold(cfg)
		r.rotateSnapshotTiers(cfg)
		n, b := r.expireScreenshotsUntil(cfg, betweenFiles)
		fmt.Printf("  past the screenshot time limit: %d (%s)\n", n, gb(b))
	}

	noDeadline := time.Now().Add(100 * 365 * 24 * time.Hour)
	toShrink, _, _, _ := store.compactableSnapshotTotals(time.Now().Add(-cfg.WarmRetention).UnixMilli())
	var compacted, compressed int
	var saved1, saved2 int64
	if proceed(fmt.Sprintf("shrink %d cold screenshots", toShrink)) {
		// The process runs at nice 19 (and ffmpeg too); no background QoS,
		// which on a busy Mac slows this pass from hours to days.
		compacted, saved1 = r.compactColdSnapshots(cfg, noDeadline, 0, betweenFiles, func(done int) {
			if done%2000 == 0 {
				fmt.Printf("[%s]   %d/%d\n", time.Since(start).Round(time.Second), done, toShrink)
			}
		})
		fmt.Printf("  shrunk %d, saved %s, skipped %d\n", compacted, gb(saved1), len(r.compactSkip))
	}
	if proceed("compress recordings with a final transcript to Opus") {
		compressed, saved2 = r.compressCallAudio(noDeadline, betweenFiles, func(done int) {
			if done%25 == 0 {
				fmt.Printf("[%s]   %d recordings\n", time.Since(start).Round(time.Second), done)
			}
		})
		fmt.Printf("  compressed %d, saved %s, skipped %d\n", compressed, gb(saved2), len(r.audioSkip))
	}
	r.checkpointWAL()

	if stopped() || t.Failed() {
		fmt.Println("Stopped between files; everything done so far is complete.")
	} else if !held("before VACUUM") {
		free, total, _ := store.databaseFreeBytes()
		dbBytes := databaseFileBytes(store)
		avail, _ := freeDiskBytes(dir)
		step(fmt.Sprintf("VACUUM check: %s free pages of %s; database files %s; disk free %s",
			gb(free), gb(total), gb(dbBytes), gb(int64(avail))))
		switch {
		case free < 64<<20:
			fmt.Println("  VACUUM skipped: under 64 MB of free pages, it would give back almost nothing")
		case avail < vacuumDiskNeed(free, total):
			fmt.Printf("  VACUUM skipped: needs %s free for the rewrite\n", gb(int64(vacuumDiskNeed(free, total))))
		default:
			vacStart := time.Now()
			if err := store.Vacuum(); err != nil {
				t.Errorf("VACUUM: %v", err)
			}
			r.checkpointWAL()
			fmt.Printf("  VACUUM done in %s: database %s -> %s\n", time.Since(vacStart).Round(time.Second),
				gb(dbBytes), gb(databaseFileBytes(store)))
		}
	}

	after := r.measure()
	fmt.Printf("\nDone in %s.\nBefore: total %s (screenshots %s in %d files, recordings %s in %d files, database %s, other %s)\nAfter:  total %s (screenshots %s in %d files, recordings %s in %d files, database %s, other %s)\n",
		time.Since(start).Round(time.Second),
		gb(before.TotalBytes), gb(before.ScreenshotBytes), before.ScreenshotFiles, gb(before.AudioBytes), before.AudioFiles, gb(before.DatabaseBytes), gb(before.OtherBytes),
		gb(after.TotalBytes), gb(after.ScreenshotBytes), after.ScreenshotFiles, gb(after.AudioBytes), after.AudioFiles, gb(after.DatabaseBytes), gb(after.OtherBytes))
}

// activityDatabaseHolders lists processes that have the folder's database
// open and running Yansu.app processes (simulator copies left out); empty
// when there are none.
func activityDatabaseHolders(dir string) string {
	var out []string
	db := filepath.Join(dir, "activity.db")
	// lsof exits 1 when one of the files is not open (or missing) even while
	// it lists a holder of another, so the listing decides, not the exit
	// code. This process is left out.
	lines, _ := exec.Command("lsof", "-F", "pc", "--", db, db+"-wal", db+"-shm").Output()
	self := fmt.Sprintf("p%d", os.Getpid())
	var holder []string
	for _, line := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		if strings.HasPrefix(line, "p") && line != self {
			holder = append(holder, line[1:])
		} else if strings.HasPrefix(line, "c") && len(holder) > 0 && !strings.HasSuffix(holder[len(holder)-1], ")") {
			holder[len(holder)-1] += " (" + line[1:] + ")"
		}
	}
	if len(holder) > 0 {
		out = append(out, "open by: "+strings.Join(holder, ", "))
	}
	if ps, err := exec.Command("ps", "-axo", "pid=,command=").Output(); err == nil {
		for _, line := range strings.Split(string(ps), "\n") {
			if strings.Contains(line, "/Yansu.app/Contents/MacOS/") && !strings.Contains(line, "CoreSimulator") {
				out = append(out, "running: "+strings.TrimSpace(line))
			}
		}
	}
	return strings.Join(out, "\n")
}

// waitForMainsPower waits while the Mac is on battery or in Low Power Mode,
// saying so once a minute, and refuses when the power state is unknown.
func waitForMainsPower(t *testing.T, ctx context.Context) {
	t.Helper()
	if power := activitymon.PowerSource(); !power.Known {
		t.Fatal("refusing: the power state is unknown")
	}
	for {
		reason := storageMediaDeferReason()
		if reason == "" {
			return
		}
		fmt.Printf("Waiting for mains power: %s\n", reason)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

// backupActivityDatabase copies activity.db and its -wal and -shm files into
// backup, after checking the disk has room for them and the run.
func backupActivityDatabase(dir, backup string) error {
	var need int64
	files := []string{"activity.db", "activity.db-wal", "activity.db-shm"}
	for _, name := range files {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
			need += info.Size()
		}
	}
	avail, err := freeDiskBytes(dir)
	if err != nil {
		return err
	}
	const margin = 2 << 30 // temporary files while shrinking and converting
	if avail < uint64(need+margin) {
		return fmt.Errorf("needs %s free for the backup and the run, have %s", gb(need+margin), gb(int64(avail)))
	}
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return err
	}
	for _, name := range files {
		src := filepath.Join(dir, name)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}
		if err := copyFileExact(src, filepath.Join(backup, name)); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return nil
}

func copyFileExact(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeConversionManifest lists, in the backup folder, the recordings the
// run converts (session, path, bytes), so each can be traced afterwards.
func writeConversionManifest(backup string, store *ActivityStore, r *StorageRotator) error {
	recordings, err := store.sessionsWithAudio(math.MaxInt64)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, m := range recordings {
		if r.audioCompressible(m) {
			fmt.Fprintf(&b, "%s\t%s\t%d\n", m.SessionID, m.RelPath, m.Size)
		}
	}
	return os.WriteFile(filepath.Join(backup, "recordings-to-opus.tsv"), []byte(b.String()), 0o600)
}

// openActivityStoreWithoutMigrations opens a data folder's activity database
// the way NewActivityStore does but runs no migrations and starts nothing in
// the background.
func openActivityStoreWithoutMigrations(t *testing.T, baseDir string) *ActivityStore {
	t.Helper()
	dbPath := filepath.Join(baseDir, "activity.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("no database at %s: %v", dbPath, err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			t.Fatalf("%s: %v", pragma, err)
		}
	}
	for _, table := range []string{"activity_sessions", "activity_snapshots", "migration_log"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n == 0 {
			db.Close()
			t.Fatalf("database has no %s table", table)
		}
	}
	activity := filepath.Join(baseDir, "activity")
	return &ActivityStore{
		db:           db,
		segmentsDir:  filepath.Join(activity, "segments"),
		snapshotsDir: filepath.Join(activity, "snapshots"),
		eventsDir:    filepath.Join(activity, "events"),
		audioDir:     filepath.Join(activity, "audio"),
	}
}
