package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// A prepared update is the downloaded app, extracted and fully verified beside
// the installed app while it runs, so the click only checks, swaps and quits.
// Preparing never installs: the installed app is untouched until the click.
const preparedUpdatePrefix = ".yansu-prepared-"

// The candidate is kept without the .app extension so Launch Services never
// registers a second, newer copy of the app (URL schemes, Spotlight) while it
// waits for the click. The swap renames it to the installed bundle's name.
const preparedCandidateName = "next.prepared"

// updateQuitDeadline bounds the quit after the swap. The helper waits for this
// process to exit before it opens the new app.
const updateQuitDeadline = 5 * time.Second

var errNoPreparedUpdate = errors.New("no prepared update")

type preparedUpdate struct {
	Root        string `json:"root"`
	Bundle      string `json:"bundle"`
	Version     string `json:"version"`
	Checksum    string `json:"checksum"`
	Requirement string `json:"requirement"`
}

func (p *preparedUpdate) candidate() string { return filepath.Join(p.Root, preparedCandidateName) }

// bundleInstallSteps are the checks and helper start of a macOS install.
type bundleInstallSteps struct {
	verify func(current, candidate, version string) (string, error)
	quick  func(current, candidate, version, requirement string) error
	start  func(*bundleUpdate) error
	// probe runs the candidate headless before a quit install commits. A nil
	// probe fails closed: a quit install then never happens.
	probe func(candidateBundle string) (bool, string)
}

var darwinInstallSteps = bundleInstallSteps{
	verify: verifyUpdateBundleRequirement,
	quick:  quickVerifyPreparedBundle,
	start:  startUpdateHelper,
	probe:  probeCandidateBundle,
}

func dataHomeOutsideBundle(bundle, dataHome string) error {
	resolvedHome := dataHome
	if resolved, err := filepath.EvalSymlinks(dataHome); err == nil {
		resolvedHome = resolved
	}
	if pathInside(bundle, resolvedHome) {
		return fmt.Errorf("data home is inside the app bundle; move it before updating")
	}
	return nil
}

// prepareUpdateCandidate extracts the archive into a new prepared folder
// beside the bundle and runs every install check on it.
func prepareUpdateCandidate(archive, bundle, version, checksum, dataHome string, verify func(string, string, string) (string, error)) (*preparedUpdate, error) {
	if err := dataHomeOutsideBundle(bundle, dataHome); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(filepath.Dir(bundle), preparedUpdatePrefix)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(root)
		}
	}()
	app, err := extractAppArchiveFile(archive, root)
	if err != nil {
		return nil, err
	}
	requirement, err := verify(bundle, app, version)
	if err != nil {
		return nil, fmt.Errorf("verify update app: %w", err)
	}
	if err := os.Rename(app, filepath.Join(root, preparedCandidateName)); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(filepath.Dir(app))
	ok = true
	return &preparedUpdate{Root: root, Bundle: bundle, Version: version, Checksum: checksum, Requirement: requirement}, nil
}

// takePreparedUpdate turns a prepared update into a transaction when it is
// still the one verified for this download and this installed app. Any error
// means the caller falls back to extracting and verifying the archive now.
func takePreparedUpdate(p *preparedUpdate, bundle, version, checksum, dataHome string, quick func(string, string, string, string) error) (*bundleUpdate, error) {
	if p == nil {
		return nil, errNoPreparedUpdate
	}
	switch {
	case p.Version != version || p.Checksum != checksum:
		return nil, fmt.Errorf("prepared update %s is not the downloaded %s", p.Version, version)
	case p.Bundle != bundle || filepath.Dir(p.Root) != filepath.Dir(bundle):
		return nil, fmt.Errorf("the app moved since the update was prepared")
	case !strings.HasPrefix(filepath.Base(p.Root), preparedUpdatePrefix):
		return nil, fmt.Errorf("prepared update folder %q is not an update folder", p.Root)
	}
	if err := relaunchableBundle(bundle); err != nil {
		return nil, err
	}
	if err := dataHomeOutsideBundle(bundle, dataHome); err != nil {
		return nil, err
	}
	if err := quick(bundle, p.candidate(), version, p.Requirement); err != nil {
		return nil, fmt.Errorf("prepared update failed its check: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// A transaction folder is never removed by startup cleanup, which only
	// sweeps prepared folders: the helper owns it until the new app starts.
	root := filepath.Join(filepath.Dir(p.Root), ".yansu-transaction-"+strings.TrimPrefix(filepath.Base(p.Root), preparedUpdatePrefix))
	if err := os.Rename(p.Root, root); err != nil {
		return nil, err
	}
	return &bundleUpdate{Root: root, Bundle: bundle, Candidate: filepath.Join(root, preparedCandidateName),
		Backup: filepath.Join(root, "previous.app"), Executable: filepath.Base(exe),
		Version: version, DataHome: dataHome, ParentPID: os.Getpid()}, nil
}

// discardPreparedUpdate removes a prepared folder; the name check keeps a bad
// record from pointing it at anything else.
func discardPreparedUpdate(p *preparedUpdate) {
	if p != nil && strings.HasPrefix(filepath.Base(p.Root), preparedUpdatePrefix) {
		_ = os.RemoveAll(p.Root)
	}
}

// cleanPreparedUpdates removes the prepared folders beside the bundle except
// keep: ones a newer download replaced, an install already used, or a quit
// cut short. Transaction folders are left alone, since a helper may still own
// one while this app acknowledges its start.
func cleanPreparedUpdates(bundleDir, keep string) {
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(bundleDir, e.Name())
		if e.IsDir() && strings.HasPrefix(e.Name(), preparedUpdatePrefix) && path != keep {
			_ = os.RemoveAll(path)
		}
	}
}

// cleanStalePreparedUpdates runs once after launch. A record for another
// bundle path (the app was moved) is dropped along with its folder. It never
// waits behind a download or preparation; the next launch cleans instead.
func (a *App) cleanStalePreparedUpdates(bundle string) {
	if !a.updater.opMu.TryLock() {
		return
	}
	defer a.updater.opMu.Unlock()
	a.updater.mu.Lock()
	p := a.updater.prepared
	keep := ""
	if p != nil && p.Bundle == bundle && filepath.Dir(p.Root) == filepath.Dir(bundle) {
		keep = p.Root
	} else {
		a.updater.prepared = nil
	}
	a.updater.mu.Unlock()
	if keep == "" {
		discardPreparedUpdate(p)
	}
	cleanPreparedUpdates(filepath.Dir(bundle), keep)
}

func recordPreparedUpdate(p *preparedUpdate) error {
	flag, err := updateStatePath("pending.json")
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(flag)
	var pending pendingUpdate
	if err == nil {
		err = json.Unmarshal(raw, &pending)
	}
	if err != nil {
		return err
	}
	if pending.Version != p.Version || pending.Checksum != p.Checksum {
		return fmt.Errorf("pending update changed while preparing")
	}
	pending.Prepared = p
	return writeUpdateJSON(flag, pending)
}

func removePendingUpdate() {
	if flag, err := updateStatePath("pending.json"); err == nil {
		_ = os.Remove(flag)
	}
}

// queuePrepareDownloadedUpdate prepares after a download, once the download's
// own operation lock is released.
func (a *App) queuePrepareDownloadedUpdate() {
	if runtime.GOOS != "darwin" {
		return
	}
	go func() {
		a.updater.opMu.Lock()
		defer a.updater.opMu.Unlock()
		a.prepareDownloadedUpdateLocked(findAppBundle(), darwinInstallSteps.verify)
	}()
}

// prepareDownloadedUpdateLocked needs opMu. One failed version is not retried
// in this process; the click then runs the full checks and reports the error.
func (a *App) prepareDownloadedUpdateLocked(bundle string, verify func(string, string, string) (string, error)) {
	if relaunchableBundle(bundle) != nil {
		return
	}
	a.updater.mu.Lock()
	staging, checksum, version := a.updater.stagingPath, a.updater.downloadedChecksum, a.updater.downloadedVersion
	current, failed := a.updater.prepared, a.updater.prepareFailed
	a.updater.mu.Unlock()
	if staging == "" || version == "" || failed == version+"/"+checksum {
		return
	}
	if current != nil && current.Version == version && current.Checksum == checksum && current.Bundle == bundle {
		if _, err := os.Stat(current.candidate()); err == nil {
			return
		}
	}
	started := time.Now()
	dataHome, err := flavorDataDir()
	if err == nil {
		err = verifySHA256(staging, checksum)
	}
	var p *preparedUpdate
	if err == nil {
		p, err = prepareUpdateCandidate(staging, bundle, version, checksum, dataHome, verify)
	}
	if err == nil {
		if err = recordPreparedUpdate(p); err != nil {
			discardPreparedUpdate(p)
		}
	}
	if err != nil {
		log.Printf("[update] could not prepare %s; the restart will verify it instead: %v", version, err)
		a.updater.mu.Lock()
		a.updater.prepareFailed = version + "/" + checksum
		a.updater.mu.Unlock()
		return
	}
	a.updater.mu.Lock()
	a.updater.prepared = p
	a.updater.mu.Unlock()
	if current != nil && current.Root != p.Root {
		discardPreparedUpdate(current)
	}
	log.Printf("[update] prepared %s in %v", version, time.Since(started).Round(time.Millisecond))
}

// installBundleUpdate swaps in the prepared app when it still checks out, and
// otherwise extracts and verifies the archive now. It returns once the helper
// is waiting for this process to exit.
func (a *App) installBundleUpdate(bundle, staging, checksum string, steps bundleInstallSteps) error {
	a.updater.mu.Lock()
	version := a.updater.downloadedVersion
	prepared := a.updater.prepared
	a.updater.prepared = nil
	a.updater.mu.Unlock()
	started := time.Now()
	var txn *bundleUpdate
	fast := false
	dataHome, err := flavorDataDir()
	if err == nil {
		txn, err = takePreparedUpdate(prepared, bundle, version, checksum, dataHome, steps.quick)
		if err != nil && prepared != nil {
			log.Printf("[update] prepared update not used: %v", err)
			discardPreparedUpdate(prepared)
		}
		fast = err == nil
	}
	if !fast {
		if err := verifySHA256(staging, checksum); err != nil {
			return err
		}
		if err := relaunchableBundle(bundle); err != nil {
			return err
		}
		if dataHome, err = flavorDataDir(); err != nil {
			return err
		}
		txn, err = prepareBundleUpdate(staging, bundle, version, dataHome, func(current, candidate, version string) error {
			_, err := steps.verify(current, candidate, version)
			return err
		})
		if err != nil {
			return err
		}
	}
	checked := time.Since(started)
	if err := txn.swapAndStart(steps.start); err != nil {
		return err
	}
	log.Printf("[update] swapped in %s in %v: checks %v, swap and helper %v (prepared: %v)", version,
		time.Since(started).Round(time.Millisecond), checked.Round(time.Millisecond), (time.Since(started) - checked).Round(time.Millisecond), fast)
	return nil
}

// quitAfterUpdateSwap quits the usual way, so background services stop and
// stores close, while the helper waits to open the new app. The deadline
// covers a quit that never starts or a teardown that stalls.
func (a *App) quitAfterUpdateSwap() {
	stopResearchProcesses()
	go func() {
		time.Sleep(updateQuitDeadline)
		log.Printf("[update] quit took longer than %v; exiting", updateQuitDeadline)
		if a.magpie != nil {
			a.magpie.KillNow()
		}
		os.Exit(0)
	}()
	performRealQuit()
}
