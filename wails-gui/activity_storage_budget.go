package main

import (
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The storage limit is held against what the activity data really takes on
// disk (measureActivityStorage): screenshots, call recordings, the database
// and leftovers, including files no row refers to. The earlier quota summed
// size columns, so files without a row (13.5 GB of screenshots on one
// profile), the database (7.8 GB) and temporary files never counted, and a
// 50 GB limit let 65 GB pile up.
//
// Over the limit, the oldest images and recordings go first, one file at a
// time, until the total is back at the limit; the activity rows keep their
// text (window and page text, OCR, summaries, transcripts), the same way a
// lost screenshot is marked image-missing. Whole sessions are only removed by
// the "Keep recordings for" time limit. Shrinking the compactor will still do
// is counted as done, so the limit never deletes what shrinking would have
// made fit; database space counts only once a VACUUM has given it back.
//
// The limit never takes:
//   - anything from the last day (by capture time and by the time its
//     session was saved);
//   - images whose session still waits for OCR;
//   - call recordings without a final transcript (recordingFinal), which the
//     call pipeline may still need to transcribe.
//
// When even removing everything it may take would leave the total over the
// limit (the database and what it keeps are larger than the limit), it
// removes nothing and reports "unreachable". And on a profile that was over
// the limit when this real-disk count first ran, it removes nothing until the
// user confirms on the Storage page, lowers the limit, or runs Free up space
// (storage_limit_confirmed in activity-config.json); the old count would not
// have removed those files.

// budgetProtectRecent keeps the last day's images and recordings out of the
// storage limit's reach, so new captures survive long enough to be looked at.
const budgetProtectRecent = 24 * time.Hour

// orphanAudioMinAge is how old a recording file with no row must be before
// it is removed. The call pipeline gives a recording its row within minutes
// and recovers left-behind tracks for 7 days (meetingTrackMaxAge); a finished
// recording with a sidecar (<name>.json) is never treated as an orphan.
const orphanAudioMinAge = 14 * 24 * time.Hour

// orphanSnapshotMinAge is how old a screenshot file with no row must be
// before the periodic sweep removes it. Frames of a session that is still
// open (a call keeps its frames in memory until it ends, however long that
// is) are never removed: the sweep skips while a call is open and leaves out
// every frame the capture still holds (framesInUse).
const orphanSnapshotMinAge = 24 * time.Hour

// snapshotSweepInterval is how often the rotator repeats the startup
// screenshot reconcile while the app keeps running.
const snapshotSweepInterval = 24 * time.Hour

// stripBatch is how many rows are changed per transaction when images are
// removed.
const stripBatch = 500

// Budget states (StorageBudgetStatus.State).
const (
	budgetStateOK          = "ok"
	budgetStateConfirm     = "confirm"
	budgetStateUnreachable = "unreachable"
)

// StorageBudgetStatus says where the activity data stands against the
// storage limit, as of the last rotation pass.
type StorageBudgetStatus struct {
	// State is "ok" (within the limit, or the oldest files are being
	// removed), "confirm" (over the limit on a profile the old count let
	// grow; nothing is removed until the user says yes) or "unreachable"
	// (what the limit may not take is already over it; nothing is removed).
	State      string `json:"state"`
	LimitBytes int64  `json:"limitBytes"`
	// ProjectedBytes is the total once pending shrinking is done.
	ProjectedBytes int64 `json:"projectedBytes"`
	// KeptBytes is what would stay with every removable file gone: the
	// database, the last day, recordings without a final transcript and
	// images waiting for OCR. Set when State is "unreachable".
	KeptBytes int64 `json:"keptBytes"`
	// Screenshots and Recordings are what the limit removes, or would remove
	// once confirmed.
	Screenshots StorageCleanupItem `json:"screenshots"`
	Recordings  StorageCleanupItem `json:"recordings"`
	CheckedAtMs int64              `json:"checkedAtMs"`
}

// projectedUsage is the total the activity data will take once the pending
// compaction and audio compression are done. Database space a VACUUM would
// give back is not counted: it counts once a VACUUM has actually run and the
// file is smaller. Files the compactor or compressor could not handle in
// this process count at their full size.
func (r *StorageRotator) projectedUsage(cfg RotatorConfig, usage ActivityStorageUsage, now time.Time) int64 {
	projected := usage.TotalBytes
	coldBefore := now.Add(-cfg.WarmRetention).UnixMilli()
	if _, before, after, err := r.store.compactableSnapshotTotals(coldBefore); err == nil {
		projected -= before - after
		projected += r.skippedCompactionSavings(coldBefore)
	}
	if r.ffmpegPath() != "" {
		if sessions, err := r.store.sessionsWithAudio(math.MaxInt64); err == nil {
			for _, m := range sessions {
				projected -= m.Size - r.audioProjectedSize(m)
			}
		}
	}
	return max(projected, 0)
}

// skippedCompactionSavings is the shrinking projectedUsage counted for
// screenshots the compactor skipped in this process.
func (r *StorageRotator) skippedCompactionSavings(coldBeforeMs int64) int64 {
	if len(r.compactSkip) == 0 {
		return 0
	}
	ids := make([]int64, 0, len(r.compactSkip))
	for id := range r.compactSkip {
		ids = append(ids, id)
	}
	rows, err := r.store.snapshotMediaByIDs(ids)
	if err != nil {
		return 0
	}
	var saved int64
	for _, m := range rows {
		if snapshotIsCompactable(m, coldBeforeMs) {
			saved += m.Size - snapshotCompactedSize(m)
		}
	}
	return saved
}

// limitRemovable reports whether the storage limit may take the recording:
// only one whose transcript is final, saved and started more than
// budgetProtectRecent ago.
func (r *StorageRotator) limitRemovable(m audioMedia, protectFromMs int64) bool {
	return m.StartTime < protectFromMs && m.SavedAt < protectFromMs && r.recordingFinal(m)
}

// budgetVictims picks the oldest images and recordings to remove until the
// projected total is at or below cfg.MaxTotalSize. Screenshots are taken
// from fromMs on (older ones are handled by the screenshot time limit);
// recordings from the oldest. Nothing the limit may not take is picked (see
// the top of this file). When every candidate together is not enough,
// reachable is false and nothing is picked; reduced is then what all of them
// would have taken off. It only reads.
func (r *StorageRotator) budgetVictims(cfg RotatorConfig, projected, fromMs int64, now time.Time) (snaps []snapshotMedia, audio []audioMedia, reduced int64, reachable bool, err error) {
	return r.budgetVictimsAfter(cfg, projected, fromMs, 0, now)
}

// budgetVictimsAfter is budgetVictims leaving out everything captured
// before sessionsFromMs (sessions the recording time limit removes first).
func (r *StorageRotator) budgetVictimsAfter(cfg RotatorConfig, projected, fromMs, sessionsFromMs int64, now time.Time) (snaps []snapshotMedia, audio []audioMedia, reduced int64, reachable bool, err error) {
	fromMs = max(fromMs, sessionsFromMs)
	if cfg.MaxTotalSize <= 0 || projected <= cfg.MaxTotalSize {
		return nil, nil, 0, true, nil
	}
	protectFrom := now.Add(-budgetProtectRecent).UnixMilli()
	coldBefore := now.Add(-cfg.WarmRetention).UnixMilli()
	all, err := r.store.sessionsWithAudio(protectFrom)
	if err != nil {
		return nil, nil, 0, false, err
	}
	var recordings []audioMedia
	for _, m := range all {
		if m.StartTime >= sessionsFromMs && r.limitRemovable(m, protectFrom) {
			recordings = append(recordings, m)
		}
	}

	cursorTs, cursorID := fromMs-1, int64(math.MaxInt64)
	var page []snapshotMedia
	pi, ai := 0, 0
	snapsDone := false
	for projected-reduced > cfg.MaxTotalSize {
		if pi >= len(page) && !snapsDone {
			page, err = r.store.snapshotsRemovableBefore(protectFrom, cursorTs, cursorID, 1000)
			if err != nil {
				return nil, nil, 0, false, err
			}
			pi = 0
			snapsDone = len(page) == 0
		}
		haveSnap, haveAudio := pi < len(page), ai < len(recordings)
		switch {
		case haveSnap && (!haveAudio || page[pi].Timestamp <= recordings[ai].StartTime):
			m := page[pi]
			pi++
			cursorTs, cursorID = m.Timestamp, m.ID
			snaps = append(snaps, m)
			reduced += snapshotProjectedSize(m, coldBefore)
		case haveAudio:
			m := recordings[ai]
			ai++
			audio = append(audio, m)
			reduced += r.audioProjectedSize(m)
		default:
			// Everything removable is not enough: remove nothing.
			return nil, nil, reduced, false, nil
		}
	}
	return snaps, audio, reduced, true, nil
}

// summarizeVictims fills the status lines for what the limit removes.
func summarizeVictims(status *StorageBudgetStatus, snaps []snapshotMedia, audio []audioMedia) {
	for _, m := range snaps {
		status.Screenshots.add(m.Size, 0, m.Timestamp)
	}
	for _, m := range audio {
		status.Recordings.add(m.Size, 0, m.StartTime)
	}
}

// enforceBudget removes the oldest images and recordings while the projected
// total is above the limit, within the rules at the top of this file, and
// records the outcome for the Storage page. force is set when the user
// started the removal (Free up space), which counts as their yes. Returns
// how many files went and their bytes.
func (r *StorageRotator) enforceBudget(cfg RotatorConfig, usage ActivityStorageUsage, fromMs int64, force bool) (removed int, freed int64) {
	now := time.Now()
	projected := r.projectedUsage(cfg, usage, now)
	snaps, audio, reduced, reachable, err := r.budgetVictims(cfg, projected, fromMs, now)
	if err != nil {
		log.Printf("[activity-rotator] pick files over the storage limit: %v", err)
		return 0, 0
	}
	status := StorageBudgetStatus{
		State:          budgetStateOK,
		LimitBytes:     cfg.MaxTotalSize,
		ProjectedBytes: projected,
		CheckedAtMs:    now.UnixMilli(),
	}
	confirmed, decided := r.limitDecision()
	switch {
	case cfg.MaxTotalSize <= 0 || projected <= cfg.MaxTotalSize:
		// Within the limit: an undecided profile has nothing waiting, so
		// later growth is handled without asking.
		if !decided {
			r.confirmLimit()
		}
	case !reachable:
		status.State = budgetStateUnreachable
		status.KeptBytes = projected - reduced
	default:
		summarizeVictims(&status, snaps, audio)
		if !force && !confirmed {
			status.State = budgetStateConfirm
		}
	}
	r.setBudgetStatus(status)
	if status.State != budgetStateOK || (len(snaps) == 0 && len(audio) == 0) {
		return 0, 0
	}
	if force && !confirmed {
		r.confirmLimit()
	}
	n, b := r.stripSnapshots(snaps)
	removed, freed = removed+n, freed+b
	for _, m := range audio {
		if r.removeRecording(m) {
			removed++
			freed += m.Size
		}
	}
	log.Printf("[activity-rotator] storage limit: removed %d image(s) and recording(s), %d bytes; activity text kept", removed, freed)
	return removed, freed
}

// setBudgetStatus records the budget outcome and logs a change of state.
func (r *StorageRotator) setBudgetStatus(status StorageBudgetStatus) {
	r.mu.Lock()
	previous := r.budget.State
	r.budget = status
	r.mu.Unlock()
	if status.State == previous {
		return
	}
	switch status.State {
	case budgetStateUnreachable:
		log.Printf("[activity-rotator] storage limit %d bytes cannot be reached: %d bytes stay even with every removable file gone; nothing removed",
			status.LimitBytes, status.KeptBytes)
	case budgetStateConfirm:
		log.Printf("[activity-rotator] storage limit %d bytes: %d image(s) and %d recording(s) wait for the user's yes on the Storage page",
			status.LimitBytes, status.Screenshots.Count, status.Recordings.Count)
	default:
	}
}

// BudgetStatus returns the outcome of the last budget check, checking now
// when none ran yet. It removes nothing.
func (r *StorageRotator) BudgetStatus() StorageBudgetStatus {
	r.mu.RLock()
	status := r.budget
	r.mu.RUnlock()
	if status.CheckedAtMs > 0 {
		return status
	}
	r.lockForUser()
	defer r.runMu.Unlock()
	cfg := r.snapshotConfig()
	now := time.Now()
	usage := r.measure()
	projected := r.projectedUsage(cfg, usage, now)
	snaps, audio, reduced, reachable, err := r.budgetVictims(cfg, projected, screenshotCutoffMs(cfg, now), now)
	status = StorageBudgetStatus{State: budgetStateOK, LimitBytes: cfg.MaxTotalSize, ProjectedBytes: projected, CheckedAtMs: now.UnixMilli()}
	if err != nil || cfg.MaxTotalSize <= 0 || projected <= cfg.MaxTotalSize {
		return status
	}
	if !reachable {
		status.State = budgetStateUnreachable
		status.KeptBytes = projected - reduced
		return status
	}
	summarizeVictims(&status, snaps, audio)
	if confirmed, _ := r.limitDecision(); !confirmed {
		status.State = budgetStateConfirm
	}
	return status
}

// expireScreenshots removes the images of screenshots older than
// cfg.ScreenshotRetention; their rows and text stay. Images waiting for OCR,
// or in a session saved within the limit, wait. Returns how many images went
// and their bytes.
func (r *StorageRotator) expireScreenshots(cfg RotatorConfig) (removed int, freed int64) {
	return r.expireScreenshotsUntil(cfg, nil)
}

// expireScreenshotsUntil is expireScreenshots stopping between batches when
// stop (when set) says so.
func (r *StorageRotator) expireScreenshotsUntil(cfg RotatorConfig, stop func() bool) (removed int, freed int64) {
	if cfg.ScreenshotRetention <= 0 {
		return 0, 0
	}
	cutoff := time.Now().Add(-cfg.ScreenshotRetention).UnixMilli()
	cursorTs, cursorID := int64(-1), int64(0)
	for stop == nil || !stop() {
		page, err := r.store.snapshotsRemovableBefore(cutoff, cursorTs, cursorID, stripBatch)
		if err != nil {
			log.Printf("[activity-rotator] list expired screenshots: %v", err)
			return
		}
		if len(page) == 0 {
			return
		}
		last := page[len(page)-1]
		cursorTs, cursorID = last.Timestamp, last.ID
		n, b := r.stripSnapshots(page)
		removed, freed = removed+n, freed+b
	}
	return removed, freed
}

// stripSnapshots marks screenshot rows image-missing in batches and removes
// their files after each batch commits. Returns the images removed and
// their bytes.
func (r *StorageRotator) stripSnapshots(rows []snapshotMedia) (removed int, freed int64) {
	for start := 0; start < len(rows); start += stripBatch {
		batch := rows[start:min(start+stripBatch, len(rows))]
		if err := r.store.stripSnapshotImages(batch); err != nil {
			log.Printf("[activity-rotator] mark screenshots image-missing: %v", err)
			return
		}
		for _, m := range batch {
			path := filepath.Join(r.store.SnapshotsDir(), m.RelPath)
			info, statErr := os.Lstat(path)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("[activity-rotator] remove screenshot %s: %v", m.RelPath, err)
				continue
			}
			removed++
			if statErr == nil {
				freed += info.Size()
			}
		}
	}
	return removed, freed
}

// removeRecording clears a session's recording and removes the file.
func (r *StorageRotator) removeRecording(m audioMedia) bool {
	changed, err := r.store.stripSessionAudio(m.SessionID, m.RelPath)
	if err != nil {
		log.Printf("[activity-rotator] clear call audio of session %s: %v", m.SessionID, err)
		return false
	}
	if !changed {
		return false
	}
	// The recording goes with its sidecar.
	if err := removeMeetingRecording(r.store.AudioDir(), m.RelPath); err != nil {
		log.Printf("[activity-rotator] remove call audio %s: %v", m.RelPath, err)
	}
	return true
}

// orphanAudioFile reports whether name is a finished recording (or this
// layer's own temporary file) the orphan sweep may consider. The call
// pipeline's raw tracks and sidecars are never touched.
func orphanAudioFile(name string) (final, temp bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, meetingAudioOpusExt+".tmp"):
		return false, true
	case strings.HasSuffix(lower, ".wav"), strings.HasSuffix(lower, meetingAudioOpusExt):
		return true, false
	default:
		return false, false
	}
}

// orphanAudioFiles lists recording files under the audio folder that no
// session refers to: finished ones older than minAge and this layer's
// temporary files older than an hour.
func (r *StorageRotator) orphanAudioFiles(now time.Time, minAge time.Duration) (paths []string, bytes int64, err error) {
	referenced, err := r.store.referencedAudioPaths()
	if err != nil {
		return nil, 0, err
	}
	days, err := os.ReadDir(r.store.AudioDir())
	if err != nil {
		return nil, 0, nil
	}
	for _, d := range days {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(r.store.AudioDir(), d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			final, temp := orphanAudioFile(f.Name())
			if !final && !temp {
				continue
			}
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			rel := filepath.Join(d.Name(), f.Name())
			age := now.Sub(info.ModTime())
			if (final && (age < minAge || referenced[rel])) || (temp && age < time.Hour) {
				continue
			}
			if final && hasMeetingSidecar(filepath.Join(r.store.AudioDir(), rel)) {
				// The call pipeline kept this recording on purpose (its row
				// could not be written); it names the call in the sidecar.
				continue
			}
			paths = append(paths, rel)
			bytes += info.Size()
		}
	}
	return paths, bytes, nil
}

// sweepOrphanAudio removes the files orphanAudioFiles lists.
func (r *StorageRotator) sweepOrphanAudio(now time.Time) (removed int, freed int64) {
	paths, _, err := r.orphanAudioFiles(now, orphanAudioMinAge)
	if err != nil {
		log.Printf("[activity-rotator] list unreferenced call audio: %v", err)
		return 0, 0
	}
	for _, rel := range paths {
		path := filepath.Join(r.store.AudioDir(), rel)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("[activity-rotator] remove unreferenced call audio %s: %v", rel, err)
			continue
		}
		removed++
		freed += info.Size()
	}
	if removed > 0 {
		log.Printf("[activity-rotator] removed %d unreferenced call audio file(s), %d bytes", removed, freed)
	}
	return removed, freed
}

// hasMeetingSidecar reports whether a recording has the call pipeline's
// sidecar (<name>.json next to <name>.wav or <name>.opus).
func hasMeetingSidecar(audioPath string) bool {
	_, err := os.Stat(strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".json")
	return err == nil
}

// sweepOrphanSnapshots repeats the startup screenshot reconcile for files
// older than orphanSnapshotMinAge, at most once per snapshotSweepInterval.
// It waits while a call is open, and it never removes a frame the capture
// still holds.
func (r *StorageRotator) sweepOrphanSnapshots(now time.Time, force bool) SnapshotFileReconcile {
	if !force && now.Sub(r.lastSnapshotSweep) < snapshotSweepInterval {
		return SnapshotFileReconcile{}
	}
	var inUse func(string) bool
	if r.framesInUse != nil {
		held, open := r.framesInUse()
		if open {
			return SnapshotFileReconcile{}
		}
		inUse = func(rel string) bool { return held[filepath.Clean(rel)] }
	}
	r.lastSnapshotSweep = now
	result, err := r.store.ReconcileSnapshotFilesExcept(now.Add(-orphanSnapshotMinAge).UnixMilli(), inUse)
	if err != nil {
		log.Printf("[activity-rotator] screenshot reconcile: %v", err)
		return SnapshotFileReconcile{}
	}
	if result != (SnapshotFileReconcile{}) {
		log.Printf("[activity-rotator] screenshot reconcile: marked %d row(s) image-missing, restored %d, removed %d unreferenced file(s)",
			result.Marked, result.Restored, result.FilesRemoved)
	}
	return result
}
