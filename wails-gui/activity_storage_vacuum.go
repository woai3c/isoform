package main

import (
	"log"
	"os"
	"time"
)

// The database file only shrinks through VACUUM: dropped tables (the removed
// Agency left 3.1 GB on one profile) and deleted rows become free pages that
// SQLite reuses but never gives back. VACUUM rewrites the whole file on the
// store's single connection, so every other read and write waits until it
// finishes (minutes on a large profile; at launch it once blocked startup
// recovery). It therefore runs only while the screen is locked, never in the
// first minutes after launch, at most every vacuumMinInterval, and only when
// there is enough free space to rewrite the file.
const (
	vacuumCheckInterval = 5 * time.Minute
	vacuumMinUptime     = 10 * time.Minute
	vacuumMinInterval   = 12 * time.Hour
	// A VACUUM is worth its pause once this many bytes are free and they are
	// at least vacuumMinFreeShare of the file.
	vacuumMinFreeBytes = 256 << 20
	vacuumMinFreeShare = 0.10
	// vacuumDiskHeadroom: VACUUM copies the live pages into a temporary
	// database and writes them back through the WAL before the old file is
	// replaced; a probe peaked at 2.1x the live size.
	vacuumDiskHeadroom = 2.2
)

// vacuumDiskNeed is the free disk a VACUUM of a file with total bytes, free
// of them unused, needs.
func vacuumDiskNeed(free, total int64) uint64 {
	return uint64(float64(max(total-free, 0)) * vacuumDiskHeadroom)
}

// processStartedAt is when this process started; VACUUM waits
// vacuumMinUptime past it.
var processStartedAt = time.Now()

// RequestVacuum asks for a VACUUM at the next safe moment even when the free
// space is below the usual threshold (after a cleanup the user started).
func (r *StorageRotator) RequestVacuum() {
	r.mu.Lock()
	r.vacuumRequested = true
	r.mu.Unlock()
}

// vacuumPending reports whether a VACUUM is requested or worth running.
func (r *StorageRotator) vacuumPending() bool {
	r.mu.RLock()
	requested := r.vacuumRequested
	r.mu.RUnlock()
	free, total, err := r.store.databaseFreeBytes()
	if err != nil {
		return false
	}
	if requested {
		return free > 0
	}
	return free >= vacuumMinFreeBytes && float64(free) >= vacuumMinFreeShare*float64(total)
}

// maybeVacuum runs VACUUM when one is pending and it is a safe moment.
// Reports whether it ran.
func (r *StorageRotator) maybeVacuum(now time.Time) bool {
	if now.Sub(processStartedAt) < vacuumMinUptime || now.Sub(r.lastVacuum) < vacuumMinInterval {
		return false
	}
	if !r.vacuumMoment() || !r.vacuumPending() {
		return false
	}
	free, total, err := r.store.databaseFreeBytes()
	if err != nil {
		return false
	}
	need := vacuumDiskNeed(free, total)
	if avail, err := freeDiskBytes(r.store.baseDir()); err != nil || avail < need {
		log.Printf("[activity-rotator] VACUUM skipped: needs %d bytes free, have %d (err=%v)", need, avail, err)
		return false
	}

	r.runMu.Lock()
	defer r.runMu.Unlock()
	// The wait for runMu can be long; the screen may have been unlocked.
	if !r.vacuumMoment() {
		return false
	}
	before := databaseFileBytes(r.store)
	started := time.Now()
	r.lastVacuum = now
	if err := r.store.Vacuum(); err != nil {
		log.Printf("[activity-rotator] VACUUM failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
		return false
	}
	r.checkpointWAL()
	r.mu.Lock()
	r.vacuumRequested = false
	r.mu.Unlock()
	log.Printf("[activity-rotator] VACUUM done in %s: database %d -> %d bytes",
		time.Since(started).Round(time.Millisecond), before, databaseFileBytes(r.store))
	return true
}

// databaseFileBytes is the size of the database, WAL and shared-memory
// files together.
func databaseFileBytes(store *ActivityStore) int64 {
	var total int64
	for _, p := range store.databaseFiles() {
		if info, err := os.Stat(p); err == nil {
			total += info.Size()
		}
	}
	return total
}
