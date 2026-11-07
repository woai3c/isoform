package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// quitInstallFixture performs a real quit install in a temp bundle: the prepared
// app (a script that exits 0 here, standing in for the new version) is swapped
// in, the previous app is kept beside it, and the trial record is written.
func quitInstallFixture(t *testing.T) *preparedUpdateFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell candidate")
	}
	f := newPreparedUpdateFixture(t)
	p := f.prepare(t)
	writeProbeCandidate(t, p.candidate(), "exit 0")
	steps := f.steps(nil)
	steps.probe = probeAt
	steps.start = func(*bundleUpdate) error { f.started++; return nil }
	if err := f.app.installPreparedOnQuit(f.bundle, steps); err != nil {
		t.Fatal(err)
	}
	return f
}

// The new version starts and never acks, as if it crashed after the early
// point in main. Two such launches are tolerated; the third restores the
// previous app and reopens it.
func TestQuitInstallCrashingBeforeHealthAckIsRolledBack(t *testing.T) {
	f := quitInstallFixture(t)
	var launched []string
	launch := func(bundle string) error { launched = append(launched, bundle); return nil }
	for i := 1; i <= quitTrialMaxUnacked; i++ {
		if runQuitInstallTrial(f.home, "0.2.0", f.bundle, launch) {
			t.Fatalf("rolled back on unacked launch %d", i)
		}
		if tr, ok := readQuitTrial(f.home); !ok || tr.Attempts != i {
			t.Fatalf("attempt %d not recorded: %+v", i, tr)
		}
	}
	if !runQuitInstallTrial(f.home, "0.2.0", f.bundle, launch) {
		t.Fatal("not rolled back after unacked launches")
	}
	if readFile(t, f.bundle, "Contents/MacOS/Yansu") != "old" {
		t.Fatal("previous app not restored")
	}
	if len(launched) != 1 || launched[0] != f.bundle {
		t.Fatalf("restored app not reopened: %v", launched)
	}
	if _, ok := readQuitTrial(f.home); ok {
		t.Fatal("trial record kept after rollback")
	}
	if left := preparedDirs(t, filepath.Dir(f.bundle), ".yansu-transaction-"); len(left) != 0 {
		t.Fatalf("kept folder left after rollback: %v", left)
	}
	// The restored app shows the same message the click path leaves, and the
	// failed version is blocked from background downloads.
	raw, err := os.ReadFile(filepath.Join(f.home, "updates", "last-error.json"))
	var rec updateRecoveryRecord
	if err != nil || json.Unmarshal(raw, &rec) != nil || rec.Version != "0.2.0" || rec.Detail == "" {
		t.Fatalf("recovery record %q: %v", raw, err)
	}
	restored := &App{updater: &updater{}}
	restored.reportUpdateRecovery()
	if !restored.updater.recoveryError || restored.updater.blockedVersion != "0.2.0" {
		t.Fatalf("failed version not blocked: %+v", restored.updater)
	}
	// The restored app reports the rollback once.
	raw, err = os.ReadFile(filepath.Join(f.home, "updates", rolledBackName))
	var ev rolledBackRecord
	if err != nil || json.Unmarshal(raw, &ev) != nil || ev != (rolledBackRecord{From: "0.2.0", To: Version, Attempts: 2}) {
		t.Fatalf("rollback event record %+v: %v", ev, err)
	}
}

// A healthy start clears the trial and removes the kept previous app; later
// launches never roll back.
func TestQuitInstallAckedHealthyIsKeptAndPreviousAppRemoved(t *testing.T) {
	f := quitInstallFixture(t)
	var launched []string
	launch := func(bundle string) error { launched = append(launched, bundle); return nil }
	if runQuitInstallTrial(f.home, "0.2.0", f.bundle, launch) {
		t.Fatal("rolled back on the first launch")
	}
	ackQuitInstallTrial(f.home, "0.2.0", f.bundle)
	if _, ok := readQuitTrial(f.home); ok {
		t.Fatal("trial kept after the healthy ack")
	}
	if left := preparedDirs(t, filepath.Dir(f.bundle), ".yansu-transaction-"); len(left) != 0 {
		t.Fatalf("previous app kept after the healthy ack: %v", left)
	}
	for i := 0; i < 3; i++ {
		if runQuitInstallTrial(f.home, "0.2.0", f.bundle, launch) {
			t.Fatal("rolled back after a healthy start")
		}
	}
	if len(launched) != 0 || readFile(t, f.bundle, "Contents/MacOS/Yansu") == "old" {
		t.Fatalf("healthy install changed: launched=%v", launched)
	}
}

// Only the version a trial belongs to counts its launches or acks.
func TestQuitTrialIgnoresOtherVersions(t *testing.T) {
	f := quitInstallFixture(t)
	launch := func(string) error { t.Fatal("launched for another version"); return nil }
	for i := 0; i < 3; i++ {
		if runQuitInstallTrial(f.home, "0.1.0", f.bundle, launch) {
			t.Fatal("rolled back for another version")
		}
	}
	ackQuitInstallTrial(f.home, "0.1.0", f.bundle)
	if tr, ok := readQuitTrial(f.home); !ok || tr.Attempts != 0 {
		t.Fatalf("other version changed the trial: %+v", tr)
	}
}

// Without the kept previous app a rollback cannot happen; nothing is touched.
func TestQuitTrialWithoutKeptAppDoesNotRollBack(t *testing.T) {
	f := quitInstallFixture(t)
	var root string
	if tr, ok := readQuitTrial(f.home); ok {
		root = tr.Root
	}
	if err := os.RemoveAll(filepath.Join(root, "previous.app")); err != nil {
		t.Fatal(err)
	}
	launch := func(string) error { t.Fatal("reopened without a kept app"); return nil }
	for i := 0; i < 3; i++ {
		if runQuitInstallTrial(f.home, "0.2.0", f.bundle, launch) {
			t.Fatal("rolled back without a kept app")
		}
	}
	if readFile(t, f.bundle, "Contents/MacOS/Yansu") == "old" {
		t.Fatal("installed app changed")
	}
}

func TestUpdateRolledBackEventAllowsOnlyClosedShapes(t *testing.T) {
	props, ok := productTelemetryProperties("update.rolled_back", map[string]any{"from": "0.2.0", "to": "0.1.0", "attempts": 2, "url": "x"})
	if !ok || props["attempts"] != 2 {
		t.Fatalf("valid event refused: %v %v", props, ok)
	}
	if _, leaked := props["url"]; leaked {
		t.Fatal("arbitrary property kept")
	}
	if _, ok := productTelemetryProperties("update.rolled_back", map[string]any{"from": "0.2.0", "to": "0.1.0", "attempts": -1}); ok {
		t.Fatal("negative attempts accepted")
	}
	if _, ok := productTelemetryProperties("update.rolled_back", map[string]any{"from": "free text", "to": "0.1.0", "attempts": 1}); ok {
		t.Fatal("free text version accepted")
	}
}
