package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeProbeCandidate replaces the prepared app's binary with a shell script
// body, so the probe runs a real process with a real exit status.
func writeProbeCandidate(t *testing.T, candidate, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell probes")
	}
	exe := filepath.Join(candidate, "Contents", "MacOS", "Yansu")
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(exe, 0755); err != nil {
		t.Fatal(err)
	}
}

// probeAt runs the probe on the prepared app's binary, as the production
// probe does for a real bundle.
func probeAt(candidate string) (bool, string) {
	return runQuitProbe(filepath.Join(candidate, "Contents", "MacOS", "Yansu"))
}

func TestUpdateProbeCommandOnlyAnswersItsFlagAndHome(t *testing.T) {
	if code, handled := runUpdateProbeCommand([]string{"--something-else", "x"}); handled || code != 0 {
		t.Fatalf("other flags handled: code=%d handled=%v", code, handled)
	}
	t.Setenv(agentHomeEnv, "/some/other/home")
	if code, handled := runUpdateProbeCommand([]string{updateProbeFlag, t.TempDir()}); !handled || code != 2 {
		t.Fatalf("mismatched home accepted: code=%d handled=%v", code, handled)
	}
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	home := t.TempDir()
	t.Setenv(agentHomeEnv, home)
	if code, handled := runUpdateProbeCommand([]string{updateProbeFlag, home}); !handled || code != 0 {
		t.Fatalf("probe did not exit cleanly: code=%d handled=%v", code, handled)
	}
}

// Each way the probe can end maps to one reason class; only a clean exit passes.
func TestQuitProbeOutcomesMapToReasonClasses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probes")
	}
	cases := []struct {
		name   string
		body   string
		passed bool
		reason string
	}{
		{"clean exit", "exit 0", true, probeReasonClean},
		{"exit status", "exit 3", false, probeReasonExitStatus},
		{"signal", "kill -9 $$", false, probeReasonSignal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProbeCandidate(t, dir, tc.body)
			passed, reason := probeAt(dir)
			if passed != tc.passed || reason != tc.reason {
				t.Fatalf("passed=%v reason=%q, want %v %q", passed, reason, tc.passed, tc.reason)
			}
		})
	}
	t.Run("timeout", func(t *testing.T) {
		old := updateProbeTimeout
		updateProbeTimeout = 200 * time.Millisecond
		t.Cleanup(func() { updateProbeTimeout = old })
		dir := t.TempDir()
		writeProbeCandidate(t, dir, "exec sleep 30")
		started := time.Now()
		passed, reason := probeAt(dir)
		if passed || reason != probeReasonTimeout || time.Since(started) > 10*time.Second {
			t.Fatalf("passed=%v reason=%q after %v", passed, reason, time.Since(started))
		}
	})
	t.Run("not executable", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "Contents", "MacOS", "Yansu")
		if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, []byte("not a program"), 0644); err != nil {
			t.Fatal(err)
		}
		if passed, reason := probeAt(dir); passed || reason != probeReasonLaunchError {
			t.Fatalf("passed=%v reason=%q", passed, reason)
		}
	})
}

// The probe must not touch the real data folder: its temporary home is removed.
func TestQuitProbeRemovesItsTemporaryHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probes")
	}
	dir := t.TempDir()
	writeProbeCandidate(t, dir, `echo "$YANSU_AGENT_HOME" > "`+filepath.Join(dir, "home.txt")+`"`)
	if passed, reason := probeAt(dir); !passed {
		t.Fatalf("probe failed: %q", reason)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "home.txt"))
	if err != nil {
		t.Fatal(err)
	}
	home := strings.TrimSpace(string(raw))
	if !strings.Contains(home, "isoform-probe-") {
		t.Fatalf("probe ran with data folder %q", home)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("temporary home left behind")
	}
}

// assertStillStaged checks that a refused quit install changed nothing: the
// old app is intact, the archive and its pending record remain, and no
// prepared or transaction folder is left beside the bundle.
func assertStillStaged(t *testing.T, f *preparedUpdateFixture) {
	t.Helper()
	if readFile(t, f.bundle, "Contents/MacOS/Yansu") != "old" {
		t.Fatal("refused quit install changed the installed app")
	}
	if readFile(t, f.home, "keep.db") != "user data" {
		t.Fatal("data changed")
	}
	if _, err := os.Stat(f.archive); err != nil {
		t.Fatalf("staged archive removed: %v", err)
	}
	pendingPath, err := updateStatePath("pending.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pendingPath); err != nil {
		t.Fatalf("pending record removed: %v", err)
	}
	if f.started != 0 || f.app.updater.installStarted {
		t.Fatal("helper started or install marked done")
	}
	dir := filepath.Dir(f.bundle)
	if left := append(preparedDirs(t, dir, preparedUpdatePrefix), preparedDirs(t, dir, ".yansu-transaction-")...); len(left) != 0 {
		t.Fatalf("refused install left %v", left)
	}
	if _, err := os.Stat(filepath.Join(f.home, "updates", installedRecordName)); !os.IsNotExist(err) {
		t.Fatal("install record written for a refused install")
	}
}

// A failed probe refuses the quit install and keeps the update staged.
func TestQuitProbeFailureLeavesTheOldAppAndKeepsItStaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probes")
	}
	modes := map[string]string{"exit status": "exit 3", "signal": "kill -9 $$"}
	for name, body := range modes {
		t.Run(name, func(t *testing.T) {
			f := newPreparedUpdateFixture(t)
			p := f.prepare(t)
			writeProbeCandidate(t, p.candidate(), body)
			steps := f.steps(nil)
			steps.probe = probeAt
			steps.start = func(*bundleUpdate) error { f.started++; return nil }
			if err := f.app.installPreparedOnQuit(f.bundle, steps); err == nil {
				t.Fatal("quit install accepted after a failed probe")
			}
			assertStillStaged(t, f)
			if f.app.updater.prepared != nil {
				t.Fatal("failed candidate kept as prepared")
			}
		})
	}
}

func TestQuitProbeTimeoutLeavesTheOldAppAndKeepsItStaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probes")
	}
	old := updateProbeTimeout
	updateProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { updateProbeTimeout = old })
	f := newPreparedUpdateFixture(t)
	p := f.prepare(t)
	writeProbeCandidate(t, p.candidate(), "exec sleep 30")
	steps := f.steps(nil)
	steps.probe = probeAt
	steps.start = func(*bundleUpdate) error { f.started++; return nil }
	if err := f.app.installPreparedOnQuit(f.bundle, steps); err == nil {
		t.Fatal("quit install accepted after a timed-out probe")
	}
	assertStillStaged(t, f)
}

// A clean probe is the only way the swap commits; the record and helper follow it.
func TestQuitProbeCleanExitCommitsTheSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probes")
	}
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
		t.Fatal("probed app not swapped in")
	}
	if readFile(t, started.Backup, "Contents/MacOS/Yansu") != "old" {
		t.Fatal("previous app not retained")
	}
}

// Without a probe the quit install fails closed.
func TestQuitInstallWithoutProbeFailsClosed(t *testing.T) {
	f := newPreparedUpdateFixture(t)
	f.prepare(t)
	steps := f.steps(nil)
	steps.probe = nil
	steps.start = func(*bundleUpdate) error { f.started++; return nil }
	if err := f.app.installPreparedOnQuit(f.bundle, steps); err == nil {
		t.Fatal("quit install without a probe succeeded")
	}
	assertStillStaged(t, f)
}
