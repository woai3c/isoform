package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// installedRecordName is written just before an install swaps the bundle. The
// next launch reads it once: when that launch is the installed version, it
// reports update.running and removes the retained previous app.
const installedRecordName = "installed.json"

// maxRecoveryBackoff caps how far apart checks run after a failed install.
const maxRecoveryBackoff = 24 * time.Hour

type installedRecord struct {
	Version     string `json:"version"`
	From        string `json:"from"`
	How         string `json:"how"`
	Root        string `json:"root,omitempty"`        // transaction folder holding the previous app (quit installs)
	ReleasedAt  int64  `json:"released_at,omitempty"` // unix ms from the feed, when present
	InstalledAt int64  `json:"installed_at"`
}

// updateRecoveryRecord is what the helper leaves after a failed relaunch. Older
// helpers wrote a bare string; reportUpdateRecovery accepts both.
type updateRecoveryRecord struct {
	Detail  string `json:"detail"`
	Version string `json:"version,omitempty"`
	// Previous is set on a rollback: the version restored. The restored app
	// writes Detail again in its own language from these two versions.
	Previous string `json:"previous,omitempty"`
}

// quitInstallInput is everything the quit decision reads, so the decision can
// be tested without a running app.
type quitInstallInput struct {
	Darwin         bool
	ReleaseChannel bool
	BackgroundOn   bool
	Relaunching    bool // the person chose Restart or a relaunch is already reopening the app
	Installing     bool // a click or quit install already swapped the bundle
	LockFree       bool // no check, download or click owns the update right now
	Staged         bool
	Prepared       bool // a prepared, checked candidate matches the staged version
	Blocked        bool // the staged version is the one whose install failed
}

// quitInstallDecision says whether the staged update installs during quit, and
// when not, why. Anything but a clear yes leaves the update staged as before.
func quitInstallDecision(in quitInstallInput) (bool, string) {
	switch {
	case !in.Darwin:
		return false, "not-darwin"
	case !in.ReleaseChannel:
		return false, "not-release"
	case !in.BackgroundOn:
		return false, "background-off"
	case in.Relaunching:
		return false, "relaunching"
	case in.Installing:
		return false, "already-installing"
	case !in.LockFree:
		return false, "busy"
	case !in.Staged:
		return false, "nothing-staged"
	case in.Blocked:
		return false, "blocked-version"
	case !in.Prepared:
		return false, "not-prepared"
	}
	return true, ""
}

// nextRecoveryBackoff doubles the gap between checks after a failed install,
// starting at the normal check interval and stopping at maxRecoveryBackoff.
func nextRecoveryBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return updateCheckInterval
	}
	if next := current * 2; next < maxRecoveryBackoff {
		return next
	}
	return maxRecoveryBackoff
}

// blocksBackground reports whether a background download of version must not
// start. After a failed install that is the failed version only. A record
// without a version (older helpers) holds all background downloads until a
// check names the version offered then. Caller holds u.mu.
func (u *updater) blocksBackground(version string) bool {
	if u.blockedVersion != "" {
		return version == u.blockedVersion
	}
	return u.recoveryError
}

// advanceRecoveryBackoff sets the next scheduled check after a recovery check.
// Caller holds u.mu.
func (u *updater) advanceRecoveryBackoff(now time.Time) {
	u.recoveryBackoff = nextRecoveryBackoff(u.recoveryBackoff)
	u.recoveryNextCheck = now.Add(u.recoveryBackoff)
}

func blockedVersionPath(dataHome string) string {
	return filepath.Join(dataHome, "updates", "blocked-version.json")
}

func loadBlockedVersion(dataHome string) string {
	data, err := os.ReadFile(blockedVersionPath(dataHome))
	var v string
	if err != nil || json.Unmarshal(data, &v) != nil {
		return ""
	}
	return v
}

func saveBlockedVersion(v string) error {
	dataHome, err := flavorDataDir()
	if err != nil {
		return err
	}
	return writeUpdateJSON(blockedVersionPath(dataHome), v)
}

func installedRecordPath(dataHome string) string {
	return filepath.Join(dataHome, "updates", installedRecordName)
}

func writeInstalledRecord(rec installedRecord) error {
	dataHome, err := flavorDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dataHome, "updates"), 0700); err != nil {
		return err
	}
	return writeUpdateJSON(installedRecordPath(dataHome), rec)
}

// removeInstalledRecordIn is used by the helper, which has the data home from
// its transaction record and no App.
func removeInstalledRecordIn(dataHome string) {
	if dataHome != "" {
		_ = os.Remove(installedRecordPath(dataHome))
	}
}

// reportInstalledUpdate runs once at launch. An install record for this version
// means the install reached running: report it and drop the retained previous
// app. Records for other versions stay until a matching launch or a restore.
func (a *App) reportInstalledUpdate() {
	dataHome, err := flavorDataDir()
	if err != nil {
		return
	}
	path := installedRecordPath(dataHome)
	data, err := os.ReadFile(path)
	var rec installedRecord
	if err != nil || json.Unmarshal(data, &rec) != nil || rec.Version != Version {
		return
	}
	recordUpdateRunningEvent(a.telemetry, rec)
	// The kept previous app stays until the health ack (ackQuitInstallTrial).
	_ = os.Remove(path)
}

// installStagedUpdateOnQuit runs first in shutdown, before any teardown, so it
// shares the ordering of the click install: swap, then the app exits and the
// helper waits for it. It never reopens the app and never blocks on a check.
func (a *App) installStagedUpdateOnQuit() {
	if a == nil || a.updater == nil {
		return
	}
	if !a.updater.opMu.TryLock() {
		log.Printf("[update] quit: an update operation is running; the staged update stays staged")
		return
	}
	defer a.updater.opMu.Unlock()

	bundle := ""
	if runtime.GOOS == "darwin" {
		bundle = findAppBundle()
	}
	a.updater.mu.Lock()
	version, checksum := a.updater.downloadedVersion, a.updater.downloadedChecksum
	prepared := a.updater.prepared != nil &&
		a.updater.prepared.Version == version &&
		a.updater.prepared.Checksum == checksum &&
		a.updater.prepared.Bundle == bundle
	in := quitInstallInput{
		Darwin:         runtime.GOOS == "darwin",
		ReleaseChannel: isReleaseChannel(),
		Installing:     a.updater.installStarted,
		Staged:         a.updater.stagingPath != "",
		Prepared:       prepared,
		Blocked:        a.updater.blockedVersion != "" && a.updater.blockedVersion == version,
	}
	a.updater.mu.Unlock()
	in.BackgroundOn = backgroundUpdatesEnabled()
	in.Relaunching = relaunchPending.Load()
	in.LockFree = true

	install, reason := quitInstallDecision(in)
	if !install {
		if in.Staged {
			log.Printf("[update] quit: staged update not installed (%s)", reason)
		}
		return
	}
	if err := a.installPreparedOnQuit(bundle, darwinInstallSteps); err != nil {
		// The staged update is unchanged; the next launch prepares it again.
		log.Printf("[update] quit: install skipped, update stays staged: %v", err)
		return
	}
	log.Printf("[update] quit: installed %s; it opens on the next launch", version)
}

// installPreparedOnQuit swaps in the prepared candidate and starts the helper
// in quit mode. It never falls back to extracting or re-verifying the archive:
// that work is too slow for quit, so the update stays staged instead.
func (a *App) installPreparedOnQuit(bundle string, steps bundleInstallSteps) error {
	a.updater.mu.Lock()
	version := a.updater.downloadedVersion
	staging := a.updater.stagingPath
	checksum := a.updater.downloadedChecksum
	prepared := a.updater.prepared
	released := a.updater.releasedAt
	stagedAt := a.updater.stagedAt
	a.updater.prepared = nil
	a.updater.mu.Unlock()

	dataHome, err := flavorDataDir()
	if err != nil {
		return err
	}
	if prepared == nil {
		return errNoPreparedUpdate
	}
	// The probe runs before any swap. Only a clean exit lets the swap commit;
	// anything else discards the prepared folder and leaves the update staged.
	passed, reason := false, probeReasonLaunchError
	if steps.probe != nil {
		passed, reason = steps.probe(prepared.candidate())
	}
	a.recordQuitProbe(passed, reason)
	if !passed {
		discardPreparedUpdate(prepared)
		a.markRestartNeeded(version)
		return fmt.Errorf("quit probe %s", reason)
	}
	txn, err := takePreparedUpdate(prepared, bundle, version, checksum, dataHome, steps.quick)
	if err != nil {
		discardPreparedUpdate(prepared)
		return err
	}
	txn.Quit = true
	rec := installedRecord{Version: version, From: Version, How: updateHowQuit, Root: txn.Root,
		ReleasedAt: released, InstalledAt: time.Now().UnixMilli()}
	if err := writeInstalledRecord(rec); err != nil {
		os.RemoveAll(txn.Root)
		return fmt.Errorf("record install: %w", err)
	}
	// The trial keeps the previous app until the new version acks healthy at
	// launch; a launch that never acks is rolled back (see update_trial.go).
	if err := writeQuitTrial(dataHome, quitTrial{Version: version, From: Version, Root: txn.Root}); err != nil {
		removeInstalledRecordIn(dataHome)
		os.RemoveAll(txn.Root)
		return fmt.Errorf("record trial: %w", err)
	}
	if err := txn.swapAndStart(steps.start); err != nil {
		removeInstalledRecordIn(dataHome)
		_ = os.Remove(quitTrialPath(dataHome))
		return err
	}
	a.finishInstallRecords(staging, version, stagedAt, updateHowQuit)
	return nil
}

// markRestartNeeded records that the quit probe refused the staged version. The
// flag is kept in pending.json, so the next launch shows the Restart now copy.
// A newer archive replaces the record and the flag with it.
func (a *App) markRestartNeeded(version string) {
	a.updater.mu.Lock()
	a.updater.restartNeeded = true
	a.updater.mu.Unlock()
	flag, err := updateStatePath("pending.json")
	if err != nil {
		return
	}
	raw, err := os.ReadFile(flag)
	var pending pendingUpdate
	if err != nil || json.Unmarshal(raw, &pending) != nil || pending.Version != version {
		return
	}
	pending.ProbeFailed = true
	if err := savePendingUpdateRecord(pending); err != nil {
		log.Printf("[update] save probe result: %v", err)
	}
}

// finishInstallRecords runs after a swap has started. It marks the install as
// done, drops the staged archive, and reports it.
func (a *App) finishInstallRecords(staging, version string, stagedAt int64, how string) {
	a.updater.mu.Lock()
	a.updater.installStarted = true
	a.updater.mu.Unlock()
	if staging != "" {
		_ = os.Remove(staging)
	}
	removePendingUpdate()
	a.recordUpdateInstalled(version, how, stagedAt)
}
