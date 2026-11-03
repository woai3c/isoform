package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ActivityStorageUsage is what the activity data takes on disk, measured from
// the files themselves rather than summed from rows, so files no row refers
// to, temporary files and the database all count. This is the number the
// storage limit is held to.
type ActivityStorageUsage struct {
	ScreenshotBytes int64 `json:"screenshotBytes"`
	ScreenshotFiles int   `json:"screenshotFiles"`
	// ScreenshotDays counts the date folders that hold at least one file.
	ScreenshotDays int `json:"screenshotDays"`
	// OldestScreenshotMs is local midnight of the oldest such folder, 0 when
	// there are no screenshots.
	OldestScreenshotMs int64 `json:"oldestScreenshotMs"`
	AudioBytes         int64 `json:"audioBytes"`
	AudioFiles         int   `json:"audioFiles"`
	// DatabaseBytes is what the database holds: its file minus the free
	// pages a VACUUM gives back, plus its WAL. DatabaseFreeBytes is those
	// free pages, which count toward nothing.
	DatabaseBytes     int64 `json:"databaseBytes"`
	DatabaseFreeBytes int64 `json:"databaseFreeBytes"`
	// OtherBytes covers legacy video segments, event spill files and the
	// video cache.
	OtherBytes   int64 `json:"otherBytes"`
	TotalBytes   int64 `json:"totalBytes"`
	MeasuredAtMs int64 `json:"measuredAtMs"`
}

// measureActivityStorage walks the activity folders and stats the database
// files (counting the database's live pages only). Unreadable entries are
// skipped.
func measureActivityStorage(store *ActivityStore) ActivityStorageUsage {
	var u ActivityStorageUsage

	oldestDay := ""
	if entries, err := os.ReadDir(store.SnapshotsDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
					u.ScreenshotBytes += info.Size()
				}
				continue
			}
			bytes, files := folderUsage(filepath.Join(store.SnapshotsDir(), e.Name()))
			u.ScreenshotBytes += bytes
			u.ScreenshotFiles += files
			if files > 0 && isSnapshotDateDir(e.Name()) {
				u.ScreenshotDays++
				if oldestDay == "" || e.Name() < oldestDay {
					oldestDay = e.Name()
				}
			}
		}
	}
	if oldestDay != "" {
		if day, err := time.ParseInLocation("2006-01-02", oldestDay, time.Local); err == nil {
			u.OldestScreenshotMs = day.UnixMilli()
		}
	}

	u.AudioBytes, u.AudioFiles = folderUsage(store.AudioDir())

	for _, p := range store.databaseFiles() {
		if info, err := os.Stat(p); err == nil {
			u.DatabaseBytes += info.Size()
		}
	}
	// Free pages are not used space: SQLite reuses them, and the next VACUUM
	// gives them back to the disk. Counting them would make the storage
	// limit remove screenshots to cover space no screenshot takes.
	if free, _, err := store.databaseFreeBytes(); err == nil && free > 0 && free <= u.DatabaseBytes {
		u.DatabaseBytes -= free
		u.DatabaseFreeBytes = free
	}

	for _, dir := range []string{
		store.SegmentsDir(),
		store.EventsDir(),
		filepath.Join(store.baseDir(), "cache", "mp4"),
	} {
		bytes, _ := folderUsage(dir)
		u.OtherBytes += bytes
	}

	u.TotalBytes = u.ScreenshotBytes + u.AudioBytes + u.DatabaseBytes + u.OtherBytes
	u.MeasuredAtMs = time.Now().UnixMilli()
	return u
}

// folderUsage returns the bytes and count of regular files under dir.
func folderUsage(dir string) (bytes int64, files int) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files
}
