package main

import (
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStorageCleanupOnDataCopy runs the storage cleanup against a copy of a
// real data folder. It never runs by default, and it refuses the real
// folders: point YANSU_STORAGE_COPY_DIR at a copy.
//
//	YANSU_STORAGE_COPY_PHASE=init  creates the copy's database schema
//	YANSU_STORAGE_COPY_PHASE=plan  prints the cleanup report, changes nothing
//	YANSU_STORAGE_COPY_PHASE=run   prints the report, runs the cleanup and a
//	                               VACUUM, and checks the result
//
// YANSU_STORAGE_COPY_LIMIT_GB (default 50) and YANSU_STORAGE_COPY_SCREENSHOT_DAYS
// (default 0) set the limits; YANSU_TEST_FFMPEG names ffmpeg.
func TestStorageCleanupOnDataCopy(t *testing.T) {
	dir := os.Getenv("YANSU_STORAGE_COPY_DIR")
	phase := os.Getenv("YANSU_STORAGE_COPY_PHASE")
	if dir == "" || phase == "" {
		t.Skip("set YANSU_STORAGE_COPY_DIR and YANSU_STORAGE_COPY_PHASE")
	}
	abs, _ := filepath.Abs(dir)
	for _, real := range []string{".yansu-agent", ".isoform-agent"} {
		if strings.Contains(abs, string(filepath.Separator)+real) {
			t.Fatalf("refusing to run on a real data folder: %s", abs)
		}
	}
	store, err := NewActivityStore(abs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if phase == "init" {
		return
	}

	limitGB := 50.0
	if v, err := strconv.ParseFloat(os.Getenv("YANSU_STORAGE_COPY_LIMIT_GB"), 64); err == nil {
		limitGB = v
	}
	days, _ := strconv.Atoi(os.Getenv("YANSU_STORAGE_COPY_SCREENSHOT_DAYS"))
	cfg := activityRotatorConfigFor(0, 50)
	cfg.MaxTotalSize = int64(limitGB * float64(1<<30))
	cfg.TargetFreeSize = cfg.MaxTotalSize * 9 / 10
	if days > 0 {
		cfg.ScreenshotRetention = time.Duration(days) * 24 * time.Hour
	}
	r := NewStorageRotator(store, cfg)
	ffmpeg := os.Getenv("YANSU_TEST_FFMPEG")
	r.ffmpegPath = func() string { return ffmpeg }
	r.screenshotRetention = func() time.Duration { return 0 }
	r.vacuumMoment = func() bool { return true }

	plan, err := r.PlanCleanup()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Print(formatCleanupPlan(plan))
	if phase != "run" {
		return
	}

	start := time.Now()
	if err := r.StartCleanup(); err != nil {
		t.Fatal(err)
	}
	for r.cleanupStatus().Running {
		time.Sleep(200 * time.Millisecond)
	}
	st := r.cleanupStatus()
	fmt.Printf("\nCleanup finished in %s: %s -> %s (freed %s)\n", time.Since(start).Round(time.Second),
		gb(st.BeforeBytes), gb(st.AfterBytes), gb(st.FreedBytes))

	saved := processStartedAt
	processStartedAt = time.Now().Add(-time.Hour)
	defer func() { processStartedAt = saved }()
	vacStart := time.Now()
	ran := r.maybeVacuum(time.Now())
	fmt.Printf("VACUUM ran=%v in %s\n", ran, time.Since(vacStart).Round(time.Millisecond))
	after := r.measure()
	fmt.Printf("After: total %s (screenshots %s in %d files, recordings %s in %d files, database %s)\n",
		gb(after.TotalBytes), gb(after.ScreenshotBytes), after.ScreenshotFiles,
		gb(after.AudioBytes), after.AudioFiles, gb(after.DatabaseBytes))

	// Every screenshot row with an image decodes, and its size matches.
	page, err := store.snapshotsWithImageBefore(time.Now().UnixMilli(), -1, 0, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	bad := 0
	for _, m := range page {
		f, err := os.Open(filepath.Join(store.SnapshotsDir(), m.RelPath))
		if err != nil {
			bad++
			continue
		}
		c, err := jpeg.DecodeConfig(f)
		f.Close()
		if err != nil || c.Width != m.Width || c.Height != m.Height {
			bad++
		}
	}
	fmt.Printf("Screenshot rows with an image: %d, unreadable or mismatched: %d\n", len(page), bad)
	if bad > 0 {
		t.Errorf("%d screenshots do not decode or do not match their rows", bad)
	}
	recordings, _ := store.sessionsWithAudio(time.Now().UnixMilli())
	for _, m := range recordings {
		path := filepath.Join(store.AudioDir(), m.RelPath)
		wav, cleanup, err := decodeMeetingAudioToWAV(ffmpeg, path)
		if err != nil {
			t.Errorf("recording %s does not decode: %v", m.RelPath, err)
			continue
		}
		wf, _ := readWAVFormat(wav)
		cleanup()
		fmt.Printf("Recording %s: %s, decodes to %d channel(s), %.0f s\n", m.RelPath, gb(m.Size), wf.channels,
			float64(wf.dataBytes)/float64(max(1, wf.byteRate)))
	}
}

func gb(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(1<<30))
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	}
}

// formatCleanupPlan prints the report the Settings page shows.
func formatCleanupPlan(p StorageCleanupPlan) string {
	var b strings.Builder
	u := p.Usage
	fmt.Fprintf(&b, "Isoform uses %s: screenshots %s (%d files, %d days), call recordings %s (%d files), database %s, other %s\n",
		gb(u.TotalBytes), gb(u.ScreenshotBytes), u.ScreenshotFiles, u.ScreenshotDays,
		gb(u.AudioBytes), u.AudioFiles, gb(u.DatabaseBytes), gb(u.OtherBytes))
	fmt.Fprintf(&b, "Storage limit %s, screenshot time limit %d day(s) (0 = none)\n\n", gb(p.LimitBytes), p.ScreenshotDays)
	day := func(ms int64) string {
		if ms <= 0 {
			return ""
		}
		return time.UnixMilli(ms).Format("2006-01-02")
	}
	remove := func(label string, it StorageCleanupItem) {
		if it.Count == 0 {
			return
		}
		fmt.Fprintf(&b, "  Remove   %-46s %6d files  %10s  (%s to %s)\n", label, it.Count, gb(it.Bytes), day(it.OldestMs), day(it.NewestMs))
	}
	convert := func(label string, it StorageCleanupItem) {
		if it.Count == 0 {
			return
		}
		fmt.Fprintf(&b, "  Convert  %-46s %6d files  %10s -> about %s\n", label, it.Count, gb(it.Bytes), gb(it.AfterBytes))
	}
	remove("screenshots no activity refers to", p.UnlinkedScreenshots)
	remove("call recordings no activity refers to", p.UnlinkedRecordings)
	remove("screenshots past the time limit (text kept)", p.ExpiredScreenshots)
	remove("oldest screenshots over the limit (text kept)", p.OverLimitScreenshots)
	remove("oldest recordings over the limit (transcript kept)", p.OverLimitRecordings)
	convert("cold screenshots to at most 2 MP", p.ShrinkScreenshots)
	convert("transcribed call recordings to Opus", p.CompressRecordings)
	if p.DatabaseSpace.Bytes > 0 {
		fmt.Fprintf(&b, "  Database %-46s %6d tables %10s  (VACUUM at the next screen lock)\n",
			"free pages, retired tables, Agency runs", p.DatabaseSpace.Count, gb(p.DatabaseSpace.Bytes))
	}
	if p.RecordingsNeedFFmpeg {
		fmt.Fprintf(&b, "  (recordings cannot be compressed: ffmpeg missing)\n")
	}
	fmt.Fprintf(&b, "\nFrees about %s: from %s to about %s. Nothing has changed yet.\n", gb(p.FreedBytes), gb(u.TotalBytes), gb(p.AfterBytes))
	return b.String()
}
