package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const preparedTestRequirement = `identifier "com.wails.Isoform" and anchor apple generic`

type preparedUpdateFixture struct {
	app      *App
	bundle   string
	home     string
	archive  string
	checksum string
	verifies int
	quick    []string
	started  int
}

// newPreparedUpdateFixture is an installed bundle, a downloaded archive of
// the next version recorded as pending, and install steps that count calls.
func newPreparedUpdateFixture(t *testing.T) *preparedUpdateFixture {
	t.Helper()
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	parent := canonicalUpdateTempDir(t)
	home := filepath.Join(parent, "data")
	t.Setenv(agentHomeEnv, home)
	f := &preparedUpdateFixture{bundle: filepath.Join(parent, "Isoform.app"), home: home}
	mustWrite(t, f.bundle, "Contents/MacOS/Yansu", "old")
	mustWrite(t, home, "keep.db", "user data")
	updates, err := updateStatePath("")
	if err != nil {
		t.Fatal(err)
	}
	f.archive = filepath.Join(updates, platformAssetName())
	payload := updateTestArchive(t, []*tar.Header{
		{Name: "Isoform.app/Contents/MacOS/Yansu", Typeflag: tar.TypeReg, Mode: 0755, Size: 3},
	})
	if err := os.WriteFile(f.archive, payload, 0600); err != nil {
		t.Fatal(err)
	}
	f.checksum = fmt.Sprintf("%x", sha256.Sum256(payload))
	if err := savePendingUpdate(f.archive, f.checksum, "0.2.0", ""); err != nil {
		t.Fatal(err)
	}
	f.app = &App{updater: &updater{stagingPath: f.archive, downloadedChecksum: f.checksum, downloadedVersion: "0.2.0"}}
	return f
}

func (f *preparedUpdateFixture) verify(current, candidate, version string) (string, error) {
	f.verifies++
	if filepath.Base(current) != "Isoform.app" || version != "0.2.0" {
		return "", fmt.Errorf("unexpected verify %s %s", current, version)
	}
	return preparedTestRequirement, nil
}

func (f *preparedUpdateFixture) steps(quickErr error) bundleInstallSteps {
	return bundleInstallSteps{
		verify: f.verify,
		quick: func(current, candidate, version, requirement string) error {
			f.quick = append(f.quick, requirement)
			if current != f.bundle || filepath.Base(candidate) != preparedCandidateName {
				return fmt.Errorf("unexpected quick check %s %s", current, candidate)
			}
			return quickErr
		},
		start: func(*bundleUpdate) error { f.started++; return nil },
	}
}

func (f *preparedUpdateFixture) prepare(t *testing.T) *preparedUpdate {
	t.Helper()
	f.app.prepareDownloadedUpdateLocked(f.bundle, f.verify)
	p := f.app.updater.prepared
	if p == nil {
		t.Fatal("update was not prepared")
	}
	return p
}

func preparedDirs(t *testing.T, dir, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			found = append(found, e.Name())
		}
	}
	return found
}

func TestPrepareDownloadedUpdateVerifiesBesideTheBundleWithoutInstalling(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	p := f.prepare(t)
	if f.verifies != 1 || filepath.Dir(p.Root) != filepath.Dir(f.bundle) || p.Requirement != preparedTestRequirement {
		t.Fatalf("prepared record %+v after %d verifies", p, f.verifies)
	}
	if !strings.HasPrefix(filepath.Base(p.Root), preparedUpdatePrefix) || readFile(t, p.candidate(), "Contents/MacOS/Yansu") != "xxx" {
		t.Fatalf("candidate not extracted at %s", p.candidate())
	}
	// Only the verified candidate remains; it is not an .app Launch Services registers.
	if entries, _ := os.ReadDir(p.Root); len(entries) != 1 || entries[0].Name() != preparedCandidateName {
		t.Fatalf("prepared folder holds %v", entries)
	}
	if readFile(t, f.bundle, "Contents/MacOS/Yansu") != "old" || readFile(t, f.home, "keep.db") != "user data" {
		t.Fatal("preparing changed the installed app or data")
	}
	// Preparing again for the same download is a no-op.
	f.app.prepareDownloadedUpdateLocked(f.bundle, f.verify)
	if f.verifies != 1 || f.app.updater.prepared.Root != p.Root {
		t.Fatal("prepared twice")
	}
	// The next launch restores the record with the download.
	next := &App{updater: &updater{}}
	if !next.restorePendingUpdate() || next.updater.prepared == nil || *next.updater.prepared != *p {
		t.Fatalf("next launch lost the prepared update: %+v", next.updater.prepared)
	}
}

func TestPrepareFailureLeavesNothingAndIsNotRetried(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	calls := 0
	reject := func(string, string, string) (string, error) { calls++; return "", errors.New("not notarized") }
	f.app.prepareDownloadedUpdateLocked(f.bundle, reject)
	f.app.prepareDownloadedUpdateLocked(f.bundle, reject)
	if calls != 1 || f.app.updater.prepared != nil {
		t.Fatalf("calls=%d prepared=%v", calls, f.app.updater.prepared)
	}
	if left := preparedDirs(t, filepath.Dir(f.bundle), preparedUpdatePrefix); len(left) != 0 {
		t.Fatalf("failed preparation left %v", left)
	}
	if readFile(t, f.bundle, "Contents/MacOS/Yansu") != "old" {
		t.Fatal("failed preparation changed the installed app")
	}
}

func TestPreparedUpdateClickOnlyChecksAndSwaps(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	f.prepare(t)
	f.verifies = 0
	if err := os.WriteFile(f.archive, []byte("not read again"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.app.installBundleUpdate(f.bundle, f.archive, f.checksum, f.steps(nil)); err != nil {
		t.Fatal(err)
	}
	if f.verifies != 0 || len(f.quick) != 1 || f.quick[0] != preparedTestRequirement || f.started != 1 {
		t.Fatalf("click ran verifies=%d quick=%v started=%d", f.verifies, f.quick, f.started)
	}
	if readFile(t, f.bundle, "Contents/MacOS/Yansu") != "xxx" {
		t.Fatal("prepared app not swapped in")
	}
	// The used folder became a transaction holding the old app for rollback.
	txns := preparedDirs(t, filepath.Dir(f.bundle), ".yansu-transaction-")
	if len(txns) != 1 || len(preparedDirs(t, filepath.Dir(f.bundle), preparedUpdatePrefix)) != 0 {
		t.Fatalf("transactions %v", txns)
	}
	if readFile(t, filepath.Join(filepath.Dir(f.bundle), txns[0], "previous.app"), "Contents/MacOS/Yansu") != "old" {
		t.Fatal("old app not kept for rollback")
	}
	if f.app.updater.prepared != nil || readFile(t, f.home, "keep.db") != "user data" {
		t.Fatal("prepared record kept or data changed")
	}
}

func TestPreparedUpdateFallsBackToFullChecks(t *testing.T) {
	for _, mode := range []string{"missing", "stale-version", "other-download", "moved", "check-fails", "folder-gone", "no-write-access"} {
		t.Run(mode, func(t *testing.T) {
			f := newPreparedUpdateFixture(t)
			var quickErr error
			bundle := f.bundle
			if mode != "missing" {
				p := f.prepare(t)
				switch mode {
				case "stale-version":
					p.Version = "0.1.9"
				case "other-download":
					p.Checksum = strings.Repeat("0", 64)
				case "moved":
					bundle = filepath.Join(filepath.Dir(f.bundle), "Moved", "Isoform.app")
					mustWrite(t, bundle, "Contents/MacOS/Yansu", "old")
				case "check-fails":
					quickErr = errors.New("a sealed resource is missing or invalid")
				case "folder-gone":
					if err := os.RemoveAll(p.Root); err != nil {
						t.Fatal(err)
					}
				case "no-write-access":
					// The prepared folder cannot become a transaction; the full path
					// then fails the same way, and the installed app stays put.
					if runtime.GOOS == "windows" || os.Geteuid() == 0 {
						t.Skip("permissions not enforced")
					}
					dir := filepath.Dir(f.bundle)
					if err := os.Chmod(dir, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
				}
				f.verifies = 0
			}
			err := f.app.installBundleUpdate(bundle, f.archive, f.checksum, f.steps(quickErr))
			if mode == "no-write-access" {
				if err == nil || readFile(t, f.bundle, "Contents/MacOS/Yansu") != "old" || f.started != 0 {
					t.Fatalf("unwritable folder: err=%v started=%d", err, f.started)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if f.verifies != 1 || f.started != 1 {
				t.Fatalf("full checks not run: verifies=%d started=%d", f.verifies, f.started)
			}
			if readFile(t, bundle, "Contents/MacOS/Yansu") != "xxx" {
				t.Fatal("update not installed by the full path")
			}
			if left := preparedDirs(t, filepath.Dir(f.bundle), preparedUpdatePrefix); len(left) != 0 {
				t.Fatalf("unusable prepared update left behind: %v", left)
			}
		})
	}
}

func TestPreparedUpdateFastPathKeepsTheRollback(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	f.prepare(t)
	steps := f.steps(nil)
	steps.start = func(u *bundleUpdate) error {
		if readFile(t, u.Backup, "Contents/MacOS/Yansu") != "old" {
			t.Fatal("backup missing before helper start")
		}
		return errors.New("cannot start helper")
	}
	if err := f.app.installBundleUpdate(f.bundle, f.archive, f.checksum, steps); err == nil {
		t.Fatal("helper failure accepted")
	}
	if readFile(t, f.bundle, "Contents/MacOS/Yansu") != "old" {
		t.Fatal("failed swap did not restore the installed app")
	}
	dir := filepath.Dir(f.bundle)
	if left := append(preparedDirs(t, dir, preparedUpdatePrefix), preparedDirs(t, dir, ".yansu-transaction-")...); len(left) != 0 {
		t.Fatalf("failed swap left %v", left)
	}
}

func TestStartupCleansStaleAndOrphanedPreparedUpdates(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	p := f.prepare(t)
	dir := filepath.Dir(f.bundle)
	orphan := filepath.Join(dir, preparedUpdatePrefix+"orphan")
	transaction := filepath.Join(dir, ".yansu-transaction-live")
	mustWrite(t, orphan, preparedCandidateName+"/x", "x")
	mustWrite(t, transaction, "previous.app/x", "x")

	// A launch with the pending record keeps its prepared update only.
	next := &App{updater: &updater{}}
	if !next.restorePendingUpdate() {
		t.Fatal("pending update not restored")
	}
	next.cleanStalePreparedUpdates(f.bundle)
	if _, err := os.Stat(p.Root); err != nil || next.updater.prepared == nil {
		t.Fatal("recorded prepared update removed")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphaned prepared update kept")
	}

	// From another bundle path, the record and its folder are dropped.
	moved := &App{updater: &updater{}}
	moved.restorePendingUpdate()
	moved.cleanStalePreparedUpdates(filepath.Join(dir, "Other", "Isoform.app"))
	if _, err := os.Stat(p.Root); !os.IsNotExist(err) || moved.updater.prepared != nil {
		t.Fatal("prepared update for a moved app kept")
	}

	// After the update installed, the pending record is stale.
	f2 := newPreparedUpdateFixture(t)
	p2 := f2.prepare(t)
	oldVersion := Version
	Version = "0.2.0"
	t.Cleanup(func() { Version = oldVersion })
	installed := &App{updater: &updater{}}
	if installed.restorePendingUpdate() {
		t.Fatal("stale pending update restored")
	}
	installed.cleanStalePreparedUpdates(f2.bundle)
	if _, err := os.Stat(p2.Root); !os.IsNotExist(err) {
		t.Fatal("stale prepared update kept")
	}
	if _, err := os.Stat(transaction); err != nil {
		t.Fatal("cleanup touched a transaction folder a helper may own")
	}
}

func TestQuickVerifyPreparedBundleChecksBothAppsAndVersion(t *testing.T) {
	designated := "designated => " + preparedTestRequirement + "\n"
	for _, tt := range []struct {
		name, fail, requirement, plist string
		ok                             bool
	}{
		{name: "ok", requirement: preparedTestRequirement, plist: "0.2.0", ok: true},
		{name: "candidate-seal", fail: "/new", requirement: preparedTestRequirement, plist: "0.2.0"},
		{name: "installed-not-developer-id", fail: "anchor", requirement: preparedTestRequirement, plist: "0.2.0"},
		{name: "installed-identity-changed", requirement: `identifier "com.wails.Other"`, plist: "0.2.0"},
		{name: "version", requirement: preparedTestRequirement, plist: "0.1.9"},
		{name: "no-record", requirement: "", plist: "0.2.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls = make(chan string, 8)
			run := func(name string, args ...string) ([]byte, error) {
				line := name + " " + strings.Join(args, " ")
				calls <- line
				if strings.Contains(line, "--deep") || strings.Contains(name, "spctl") {
					t.Errorf("click ran a full check: %s", line)
				}
				switch {
				case tt.fail == "/new" && strings.HasSuffix(line, "/new"):
					return nil, errors.New("sealed resource changed")
				case tt.fail == "anchor" && strings.Contains(line, "6.1.13] exists"):
					return nil, errors.New("not Developer ID")
				case strings.Contains(line, " -d -r- "):
					return []byte(designated), nil
				case strings.Contains(name, "PlistBuddy"):
					return []byte(tt.plist + "\n"), nil
				}
				return nil, nil
			}
			err := quickVerifyPreparedBundleWithTools("/old", "/new", "0.2.0", tt.requirement, run)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v", err)
			}
			close(calls)
			if tt.ok {
				var lines []string
				for line := range calls {
					lines = append(lines, line)
				}
				if len(lines) != 4 {
					t.Fatalf("calls %v", lines)
				}
			}
		})
	}
}

func TestUpdateHelperWaitsForExitWithoutPolling(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS updater")
	}
	cmd := exec.Command("/bin/sleep", "0.3")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	start := time.Now()
	if !waitUpdateProcessExit(cmd.Process.Pid, 5*time.Second) {
		t.Fatal("exit not observed")
	}
	<-done
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waited %v", elapsed)
	}
	// An already gone process returns at once; a live one times out.
	if !waitUpdateProcessExit(cmd.Process.Pid, time.Second) {
		t.Fatal("gone process reported alive")
	}
	if waitUpdateProcessExit(os.Getpid(), 50*time.Millisecond) {
		t.Fatal("live process reported gone")
	}
}

func TestPendingUpdateRecordsPreparedUpdate(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	p := f.prepare(t)
	flag, _ := updateStatePath("pending.json")
	raw, err := os.ReadFile(flag)
	if err != nil {
		t.Fatal(err)
	}
	var pending pendingUpdate
	if err := json.Unmarshal(raw, &pending); err != nil || pending.Prepared == nil || *pending.Prepared != *p {
		t.Fatalf("pending.json %s", raw)
	}
	// A new download replaces the record without a prepared update.
	if err := savePendingUpdate(f.archive, f.checksum, "0.2.0", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(flag)
	if strings.Contains(string(raw), "prepared") {
		t.Fatal("new download kept the old prepared update")
	}
	if err := recordPreparedUpdate(&preparedUpdate{Version: "0.3.0", Checksum: f.checksum}); err == nil {
		t.Fatal("recorded a prepared update for another download")
	}
}

// TestPreparedUpdateRealBundleTiming times preparation and the click on a
// real notarized release, with the real codesign, spctl and tar. Opt-in:
// YANSU_UPDATE_REAL_ARCHIVE=<Isoform-X.Y.Z-darwin-arm64.app.tar.gz> and
// YANSU_UPDATE_REAL_VERSION=X.Y.Z. The archive's app stands in for both the
// installed app and the update; nothing is launched.
func TestPreparedUpdateRealBundleTiming(t *testing.T) {
	archive, version := os.Getenv("YANSU_UPDATE_REAL_ARCHIVE"), os.Getenv("YANSU_UPDATE_REAL_VERSION")
	if archive == "" || version == "" || runtime.GOOS != "darwin" {
		t.Skip("set YANSU_UPDATE_REAL_ARCHIVE and YANSU_UPDATE_REAL_VERSION")
	}
	parent := canonicalUpdateTempDir(t)
	bundle := filepath.Join(parent, "Isoform.app")
	if out, err := exec.Command("/usr/bin/tar", "-xzf", archive, "-C", parent).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
	home := filepath.Join(parent, "data")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	p, err := prepareUpdateCandidate(archive, bundle, version, checksum, home, verifyUpdateBundleRequirement)
	if err != nil {
		t.Fatal(err)
	}
	prepared := time.Since(started)

	started = time.Now()
	if err := quickVerifyPreparedBundle(bundle, p.candidate(), version, p.Requirement); err != nil {
		t.Fatal(err)
	}
	checked := time.Since(started)
	txn, err := takePreparedUpdate(p, bundle, version, checksum, home, func(string, string, string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.swapAndStart(func(*bundleUpdate) error { return nil }); err != nil {
		t.Fatal(err)
	}
	swapped := time.Since(started) - checked
	t.Logf("background prepare (extract + deep codesign + spctl): %v", prepared.Round(time.Millisecond))
	t.Logf("click: quick check %v, renames %v", checked.Round(time.Millisecond), swapped.Round(time.Millisecond))
	// A tampered resource fails the click check, sending the click to the full path.
	if err := os.WriteFile(filepath.Join(txn.Backup, "Contents", "Resources", "tampered"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := quickVerifyPreparedBundle(bundle, txn.Backup, version, p.Requirement); err == nil {
		t.Fatal("tampered app passed the click check")
	}
}
