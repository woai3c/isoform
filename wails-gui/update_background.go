package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const updateLaunchDelay = 3 * time.Second
const updateCheckInterval = 2 * time.Hour

// One cancellable schedule owns both launch and periodic checks.
func runUpdateSchedule(ctx context.Context, launch, interval time.Duration, check func()) {
	runUpdateScheduleWithWake(ctx, launch, interval, nil, check)
}

func runUpdateScheduleWithWake(ctx context.Context, launch, interval time.Duration, wake <-chan struct{}, check func()) {
	timer := time.NewTimer(launch)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if ctx.Err() != nil {
				return
			}
			check()
			timer.Reset(interval)
		case <-wake:
			if ctx.Err() != nil {
				return
			}
			check()
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
		}
	}
}

func updateStatePath(name string) (string, error) {
	root, err := flavorDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "updates")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func backgroundUpdatesEnabled() bool {
	path, err := updateStatePath("background.json")
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return true
	}
	var enabled bool
	return err == nil && json.Unmarshal(data, &enabled) == nil && enabled
}

// SetBackgroundUpdates changes download policy; it never starts an install.
func (a *App) SetBackgroundUpdates(enabled bool) error {
	path, err := updateStatePath("background.json")
	if err != nil {
		return err
	}
	if err := writeUpdateJSON(path, enabled); err != nil {
		return err
	}
	if enabled {
		a.queueBackgroundDownload()
	}
	return nil
}

// queueBackgroundDownload returns as soon as policy is saved or a check ends.
// One worker owns downloads; a policy change during a queued attempt asks that
// worker to recheck once more, without spawning another concurrent download.
func (a *App) queueBackgroundDownload() {
	if !isReleaseChannel() {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	queueUpdateDownload(ctx, a.updater, a.downloadBackgroundUpdate)
}

func queueUpdateDownload(ctx context.Context, state *updater, download func()) <-chan struct{} {
	if ctx.Err() != nil {
		return nil
	}
	state.mu.Lock()
	state.backgroundRequested = true
	if state.backgroundQueued {
		done := state.backgroundDone
		state.mu.Unlock()
		return done
	}
	state.backgroundQueued = true
	done := make(chan struct{})
	state.backgroundDone = done
	state.mu.Unlock()
	go func() {
		for {
			state.mu.Lock()
			state.backgroundRequested = false
			state.mu.Unlock()
			if ctx.Err() == nil {
				download()
			}
			state.mu.Lock()
			if state.backgroundRequested && ctx.Err() == nil {
				state.mu.Unlock()
				continue
			}
			state.backgroundQueued = false
			close(done)
			state.mu.Unlock()
			return
		}
	}()
	return done
}

func (a *App) downloadBackgroundUpdate() {
	if isReleaseChannel() {
		_ = a.downloadUpdate(true)
	}
}

type pendingUpdate struct {
	Path         string `json:"path"`
	Checksum     string `json:"checksum"`
	Version      string `json:"version"`
	ReleaseNotes string `json:"release_notes,omitempty"`
	StagedAt     int64  `json:"staged_at,omitempty"` // unix ms the archive was verified
	// ProbeFailed records that the quit probe refused this archive; it installs
	// only through Restart now.
	ProbeFailed bool  `json:"probe_failed,omitempty"`
	ReleasedAt  int64 `json:"released_at,omitempty"` // unix ms the feed published it, when known
	// Prepared is the extracted and verified app waiting beside the bundle.
	Prepared *preparedUpdate `json:"prepared,omitempty"`
}

func savePendingUpdate(path, checksum, version, releaseNotes string) error {
	return savePendingUpdateRecord(pendingUpdate{Path: path, Checksum: checksum, Version: version,
		ReleaseNotes: releaseNotes, StagedAt: time.Now().UnixMilli()})
}

// savePendingUpdateRecord atomically replaces pending.json. Its rename is the
// commit point that makes one archive the staged one.
func savePendingUpdateRecord(pending pendingUpdate) error {
	flag, err := updateStatePath("pending.json")
	if err != nil {
		return err
	}
	return writeUpdateJSON(flag, pending)
}

// restorePendingUpdate only restores verified bytes in this computer's update
// folder. It is called before observation starts on a subsequent launch.
func (a *App) restorePendingUpdate() bool {
	if !isReleaseChannel() {
		return false
	}
	flag, err := updateStatePath("pending.json")
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(flag)
	var pending pendingUpdate
	if err != nil || json.Unmarshal(raw, &pending) != nil {
		return false
	}
	primary, next := updateSlotPaths(filepath.Dir(flag))
	valid := (pending.Path == primary || pending.Path == next) &&
		isNewer(pending.Version, Version) && verifySHA256(pending.Path, pending.Checksum) == nil
	if !valid {
		_ = os.Remove(flag)
		// An archive no record commits to is an orphan from an interrupted download.
		_ = os.Remove(next)
		return false
	}
	// Only the committed slot stays; the other is an orphan from an interrupted replacement.
	if pending.Path == primary {
		_ = os.Remove(next)
	} else {
		_ = os.Remove(primary)
	}
	a.updater.mu.Lock()
	a.updater.stagingPath = pending.Path
	a.updater.downloadedChecksum = pending.Checksum
	a.updater.downloadedVersion = pending.Version
	a.updater.stagedAt = pending.StagedAt
	a.updater.releasedAt = pending.ReleasedAt
	a.updater.restartNeeded = pending.ProbeFailed
	a.updater.info = UpdateInfo{Available: true, Current: Version, Latest: pending.Version, ReleaseNotes: pending.ReleaseNotes}
	a.updater.status = UpdateStatus{Status: "update-downloaded", Version: pending.Version, RestartNeeded: pending.ProbeFailed}
	// Startup cleanup removes a prepared folder that no record keeps.
	if p := pending.Prepared; p != nil && p.Version == pending.Version && p.Checksum == pending.Checksum {
		if st, err := os.Stat(p.candidate()); err == nil && st.IsDir() {
			a.updater.prepared = p
		}
	}
	a.updater.mu.Unlock()
	return true
}

// RestartToUpdate is the explicit, user-initiated restart action.
func (a *App) RestartToUpdate() error { return a.QuitAndInstall() }

// AcknowledgeRollbackNotice clears the one-time rollback notice after About shows it.
func (a *App) AcknowledgeRollbackNotice() {
	if a.updater == nil {
		return
	}
	a.updater.mu.Lock()
	a.updater.rollbackNotice = ""
	a.updater.mu.Unlock()
}

// DismissUpdateUntilNextLaunch keeps the account status and staged update.
func (a *App) DismissUpdateUntilNextLaunch() {
	a.updater.mu.Lock()
	a.updater.later = true
	a.updater.mu.Unlock()
}
