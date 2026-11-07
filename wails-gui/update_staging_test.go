package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A newer release is staged in the other slot, and the old archive goes only
// after the new one is committed to pending.json.
func TestNewerStagedReplacesOnlyAfterTheNewArchiveVerifies(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	slot := f.archive + ".next"
	next := []byte("newer verified archive")
	nextSum := fmt.Sprintf("%x", sha256.Sum256(next))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(next) }))
	defer srv.Close()

	f.app.updater.expectedChecksum = nextSum
	f.app.updater.targetVersion = "0.3.0"
	f.app.updater.targetNotes = "newer notes"
	if err := f.app.downloadNewerStaged(srv.URL, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Dir(slot), filepath.Base(slot)) != string(next) {
		t.Fatal("newer archive not in the other slot")
	}
	if _, err := os.Stat(f.archive); !os.IsNotExist(err) {
		t.Fatalf("old archive kept after commit: %v", err)
	}
	pendingPath, err := updateStatePath("pending.json")
	if err != nil {
		t.Fatal(err)
	}
	var pending pendingUpdate
	raw, err := os.ReadFile(pendingPath)
	if err != nil || json.Unmarshal(raw, &pending) != nil {
		t.Fatalf("pending record unreadable: %v", err)
	}
	if pending.Path != slot || pending.Version != "0.3.0" || pending.Checksum != nextSum || pending.StagedAt == 0 {
		t.Fatalf("pending record %+v", pending)
	}
	if f.app.updater.stagingPath != slot || f.app.updater.downloadedVersion != "0.3.0" {
		t.Fatalf("memory not switched: %+v", f.app.updater)
	}
}

// A failed newer download leaves the staged archive, its record and its status alone.
func TestFailedNewerDownloadKeepsTheStagedArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("retries sleep between attempts")
	}
	f := newPreparedUpdateFixture(t)
	slot := f.archive + ".next"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("corrupt")) }))
	defer srv.Close()
	f.app.updater.expectedChecksum = strings.Repeat("a", 64)
	f.app.updater.targetVersion = "0.3.0"

	if err := f.app.downloadNewerStaged(srv.URL, "0.3.0"); err == nil {
		t.Fatal("corrupt newer archive accepted")
	}
	if _, err := os.Stat(slot); !os.IsNotExist(err) {
		t.Fatalf("failed slot left behind: %v", err)
	}
	data, err := os.ReadFile(f.archive)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != f.checksum {
		t.Fatal("staged archive changed by a failed newer download")
	}
	if f.app.updater.stagingPath != f.archive || f.app.updater.downloadedVersion != "0.2.0" || f.app.updater.targetVersion != "" {
		t.Fatalf("state after failure: %+v", f.app.updater)
	}
	if got := f.app.GetUpdateStatus(); got.Status != "update-downloaded" || got.Version != "0.2.0" {
		t.Fatalf("staged card status lost: %+v", got)
	}
}

// Startup keeps only the archive that pending.json commits to.
func TestRestorePendingKeepsOnlyTheCommittedArchive(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	slot := f.archive + ".next"
	data, err := os.ReadFile(f.archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slot, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := savePendingUpdateRecord(pendingUpdate{Path: slot, Checksum: f.checksum, Version: "0.2.0"}); err != nil {
		t.Fatal(err)
	}
	next := &App{updater: &updater{}}
	if !next.restorePendingUpdate() || next.updater.stagingPath != slot {
		t.Fatalf("committed archive not restored: %+v", next.updater)
	}
	if _, err := os.Stat(f.archive); !os.IsNotExist(err) {
		t.Fatal("orphaned archive kept at startup")
	}
}

func TestQuitInstallDecision(t *testing.T) {
	ready := quitInstallInput{Darwin: true, ReleaseChannel: true, BackgroundOn: true, LockFree: true, Staged: true, Prepared: true}
	cases := []struct {
		name   string
		change func(*quitInstallInput)
		want   string
	}{
		{"ready installs", func(*quitInstallInput) {}, ""},
		{"not macOS", func(in *quitInstallInput) { in.Darwin = false }, "not-darwin"},
		{"development build", func(in *quitInstallInput) { in.ReleaseChannel = false }, "not-release"},
		{"background updates off", func(in *quitInstallInput) { in.BackgroundOn = false }, "background-off"},
		{"a Restart is reopening", func(in *quitInstallInput) { in.Relaunching = true }, "relaunching"},
		{"click already swapped", func(in *quitInstallInput) { in.Installing = true }, "already-installing"},
		{"check or download running", func(in *quitInstallInput) { in.LockFree = false }, "busy"},
		{"nothing staged", func(in *quitInstallInput) { in.Staged = false }, "nothing-staged"},
		{"failed version", func(in *quitInstallInput) { in.Blocked = true }, "blocked-version"},
		{"no checked candidate", func(in *quitInstallInput) { in.Prepared = false }, "not-prepared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := ready
			tc.change(&in)
			install, reason := quitInstallDecision(in)
			if install != (tc.want == "") || reason != tc.want {
				t.Fatalf("install=%v reason=%q, want reason %q", install, reason, tc.want)
			}
		})
	}
}

// The quit install never falls back to re-verifying the archive. Without a
// prepared candidate it fails and leaves the bundle untouched.
func TestQuitInstallNeverFallsBackToFullChecks(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	steps := f.steps(nil)
	steps.start = func(*bundleUpdate) error { f.started++; return nil }
	if err := f.app.installPreparedOnQuit(f.bundle, steps); err == nil {
		t.Fatal("quit install without a prepared candidate succeeded")
	}
	if f.verifies != 0 || f.started != 0 || readFile(t, f.bundle, "Contents/MacOS/Yansu") != "old" {
		t.Fatalf("quit install did more than check: verifies=%d started=%d", f.verifies, f.started)
	}
}

// With a prepared candidate, the quit install swaps the app, starts the helper in
// quit mode, and writes the record that the next launch reads.
func TestQuitInstallSwapsWithoutReopening(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	p := f.prepare(t)
	writeProbeCandidate(t, p.candidate(), "exit 0")
	steps := f.steps(nil)
	steps.probe = probeAt
	var started *bundleUpdate
	steps.start = func(u *bundleUpdate) error { started = u; f.started++; return nil }
	if err := f.app.installPreparedOnQuit(f.bundle, steps); err != nil {
		t.Fatal(err)
	}
	if started == nil || !started.Quit || f.started != 1 {
		t.Fatalf("helper not started in quit mode: %+v", started)
	}
	if !strings.Contains(readFile(t, f.bundle, "Contents/MacOS/Yansu"), "exit 0") {
		t.Fatal("prepared app not swapped in")
	}
	if readFile(t, started.Backup, "Contents/MacOS/Yansu") != "old" {
		t.Fatal("previous app not retained for the transaction")
	}
	if _, err := os.Stat(f.archive); !os.IsNotExist(err) {
		t.Fatal("staged archive kept after install")
	}
	if pendingPath, err := updateStatePath("pending.json"); err == nil {
		if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
			t.Fatal("pending record kept after install")
		}
	}
	var rec installedRecord
	recPath, err := updateStatePath(installedRecordName)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(recPath)
	if err != nil || json.Unmarshal(raw, &rec) != nil {
		t.Fatalf("installed record: %v", err)
	}
	if rec.Version != "0.2.0" || rec.How != updateHowQuit || rec.Root != started.Root {
		t.Fatalf("installed record %+v", rec)
	}
	if !f.app.updater.installStarted || readFile(t, f.home, "keep.db") != "user data" {
		t.Fatal("state or data changed unexpectedly")
	}
}

// The first launch of the installed version drops the record and the retained app.
func TestInstalledRecordIsReadOnceByTheInstalledVersion(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	t.Setenv(agentHomeEnv, t.TempDir())
	bundle := filepath.Join(canonicalUpdateTempDir(t), "Isoform.app")
	retained := filepath.Join(filepath.Dir(bundle), ".yansu-transaction-x")
	mustWrite(t, retained, "previous.app/Contents/MacOS/Yansu", "old")
	recPath, err := updateStatePath(installedRecordName)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeUpdateJSON(recPath, installedRecord{Version: "0.1.9", How: updateHowQuit, Root: retained}); err != nil {
		t.Fatal(err)
	}
	(&App{updater: &updater{}}).reportInstalledUpdate()
	if _, err := os.Stat(recPath); err != nil {
		t.Fatal("record for another version was removed")
	}
	if err := writeUpdateJSON(recPath, installedRecord{Version: Version, How: updateHowQuit, Root: retained, InstalledAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	(&App{updater: &updater{}}).reportInstalledUpdate()
	if _, err := os.Stat(recPath); !os.IsNotExist(err) {
		t.Fatal("record kept after the installed version launched")
	}
}

func TestRecoveryBackoffDoublesAndCaps(t *testing.T) {
	want := []time.Duration{2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	got := time.Duration(0)
	for i, w := range want {
		got = nextRecoveryBackoff(got)
		if got != w {
			t.Fatalf("step %d: backoff %v, want %v", i, got, w)
		}
	}
}

// After a failed install, the failed version is blocked from background
// downloads; a newer one is not. A record without a version holds background
// downloads until a check names the version offered.
func TestRecoveryBlocksOnlyTheFailedVersion(t *testing.T) {
	u := &updater{recoveryError: true, blockedVersion: "0.3.0"}
	if !u.blocksBackground("0.3.0") || u.blocksBackground("0.4.0") {
		t.Fatal("failed version not blocked alone")
	}
	legacy := &updater{recoveryError: true}
	if !legacy.blocksBackground("0.4.0") {
		t.Fatal("record without a version released background downloads")
	}
}

// The scheduled check after a failed install: records the failed version,
// never queues it, offers a newer one, and advances the backoff.
func TestRecoveryCheckSkipsTheFailedVersionAndBacksOff(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	home := t.TempDir()
	t.Setenv(agentHomeEnv, home)
	t.Setenv(appFlavorEnv, appFlavorIsoform)
	key := runtime.GOOS + "-" + displayArch()
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("x")))
	manifest := func(v string) map[string]any {
		return map[string]any{"version": v, "updater_assets": map[string]string{key: "http://127.0.0.1:1/asset"},
			"updater_checksums": map[string]string{key: sum}}
	}
	if err := os.MkdirAll(filepath.Join(home, "updates"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeUpdateJSON(filepath.Join(home, "updates", "last-error.json"), updateRecoveryRecord{Detail: "relaunch failed", Version: "0.3.0"}); err != nil {
		t.Fatal(err)
	}
	app := appWithR2Manifest(t, manifest("0.3.0"))
	app.reportUpdateRecovery()
	if !app.updater.recoveryError || app.updater.blockedVersion != "0.3.0" {
		t.Fatalf("recovery %+v", app.updater)
	}
	app.checkAfterRecovery()
	if app.updater.targetVersion != "" || !app.updater.recoveryNextCheck.After(time.Now()) {
		t.Fatalf("failed version targeted or no backoff: %+v", app.updater)
	}
	if got := app.GetUpdateStatus(); got.Status != "error" || got.Detail != "relaunch failed" {
		t.Fatalf("rollback message lost: %+v", got)
	}

	newer := appWithR2Manifest(t, manifest("0.4.0"))
	newer.updater.blockedVersion = "0.3.0"
	newer.updater.recoveryError = true
	newer.checkAfterRecovery()
	if newer.updater.targetVersion != "0.4.0" {
		t.Fatalf("newer release not offered after recovery: %+v", newer.updater)
	}
}

// A record from an older helper names no version. The release offered at the
// first check after recovery is blocked, not the releases after it.
func TestLegacyRecoveryRecordBlocksTheOfferedVersion(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	home := t.TempDir()
	t.Setenv(agentHomeEnv, home)
	t.Setenv(appFlavorEnv, appFlavorIsoform)
	key := runtime.GOOS + "-" + displayArch()
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("x")))
	if err := os.MkdirAll(filepath.Join(home, "updates"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeUpdateJSON(filepath.Join(home, "updates", "last-error.json"), "relaunch failed"); err != nil {
		t.Fatal(err)
	}
	app := appWithR2Manifest(t, map[string]any{"version": "0.3.0", "updater_assets": map[string]string{key: "http://127.0.0.1:1/a"},
		"updater_checksums": map[string]string{key: sum}})
	app.reportUpdateRecovery()
	if app.updater.blockedVersion != "" {
		t.Fatal("legacy record already names a version")
	}
	app.checkAfterRecovery()
	if app.updater.blockedVersion != "0.3.0" || app.updater.targetVersion != "" {
		t.Fatalf("offered version not blocked: %+v", app.updater)
	}
}

// Update events carry only closed values and version strings.
func TestUpdateEventsAllowOnlyClosedShapes(t *testing.T) {
	cases := []struct {
		event string
		in    map[string]any
		ok    bool
	}{
		{"update.checked", map[string]any{"result": "found"}, true},
		{"update.checked", map[string]any{"result": "none"}, true},
		{"update.checked", map[string]any{"result": "error", "error_class": "network"}, true},
		{"update.checked", map[string]any{"result": "error", "error_class": "https://example.test/private"}, false},
		{"update.checked", map[string]any{"result": "found", "url": "https://example.test/x"}, true},
		{"update.staged", map[string]any{"from": "0.1.0", "to": "0.2.0"}, true},
		{"update.staged", map[string]any{"from": "0.1.0", "to": "free text"}, false},
		{"update.installed", map[string]any{"from": "0.1.0", "to": "0.2.0", "how": "quit", "waited_days": 2}, true},
		{"update.installed", map[string]any{"from": "0.1.0", "to": "0.2.0", "how": updateHowRestartNow}, true},
		{"update.installed", map[string]any{"from": "0.1.0", "to": "0.2.0", "how": "click"}, false},
		{"update.installed", map[string]any{"from": "0.1.0", "to": "0.2.0", "how": "quit", "waited_days": -1}, false},
		{"update.running", map[string]any{"version": "0.2.0", "how": "quit", "release_to_run_minutes": 30}, true},
		{"update.running", map[string]any{"version": "0.2.0", "how": "quit", "release_to_run_minutes": 1.5}, false},
	}
	for _, tc := range cases {
		props, ok := productTelemetryProperties(tc.event, tc.in)
		if ok != tc.ok {
			t.Fatalf("%s %v: allowed=%v, want %v", tc.event, tc.in, ok, tc.ok)
		}
		if ok {
			if _, leaked := props["url"]; leaked {
				t.Fatalf("%s kept an arbitrary property: %v", tc.event, props)
			}
		}
	}
}

func TestUpdateErrorClassCarriesNoText(t *testing.T) {
	cases := map[string]string{
		"R2 request failed: Get https://secret.example/x: dial": "network",
		"R2 returned 503": "http",
		"Update feed has no valid SHA-256 checksum": "feed",
		"something unexpected https://x":            "other",
	}
	for msg, want := range cases {
		if got := updateErrorClass(msg); got != want {
			t.Fatalf("%q -> %q, want %q", msg, got, want)
		}
	}
}
