//go:build darwin

package main

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRelaunchEnvArgsArePairs(t *testing.T) {
	got := relaunchEnvArgs([]string{"ISOFORM_RELAY_URL=http://127.0.0.1:8791", "YANSU_BROWSER_BRIDGE=1"})
	want := []string{"--env", "ISOFORM_RELAY_URL=http://127.0.0.1:8791", "--env", "YANSU_BROWSER_BRIDGE=1"}
	if !slices.Equal(got, want) {
		t.Fatalf("relaunchEnvArgs() = %q, want %q", got, want)
	}
	if got := relaunchEnvArgs(nil); len(got) != 0 {
		t.Fatalf("relaunchEnvArgs(nil) = %q, want no arguments", got)
	}
}

func TestRelaunchKeepsCompletedSetupInItsDataHome(t *testing.T) {
	home, dataHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(appFlavorEnv, appFlavorIsoform)
	previousFlavor, previousConfigDir := buildFlavor, configDirName
	t.Cleanup(func() { buildFlavor, configDirName = previousFlavor, previousConfigDir })
	buildFlavor = appFlavorIsoform
	configDirName = configDirForFlavor(buildFlavor)

	db, err := sql.Open("sqlite", filepath.Join(dataHome, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ui_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ui_state (key, value) VALUES (?, ?)`, onboardingStateKey, `{"version":2,"completed":true}`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv(agentHomeEnv, dataHome)
	carried := relaunchEnv()
	t.Setenv(agentHomeEnv, "")
	for _, entry := range carried {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			t.Setenv(key, value)
		}
	}
	launch := readNativeSetupLaunchState()
	if launch.SetupNeeded || !launch.MenuBarVisible {
		t.Fatalf("completed setup relaunched as %+v, want main and menu mark", launch)
	}
}

// fakeOpener writes a script that appends each call's arguments (one per
// line, calls separated by "--") to a log, and fails when failWhen is in
// its arguments.
func fakeOpener(t *testing.T, failWhen string) (opener, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	opener = filepath.Join(dir, "open")
	script := "#!/bin/sh\n{ printf '%s\\n' \"$@\"; echo --; } >> '" + log + "'\n"
	if failWhen != "" {
		script += "for a; do [ \"$a\" = '" + failWhen + "' ] && exit 3; done\n"
	}
	script += "exit 0\n"
	if err := os.WriteFile(opener, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return opener, log
}

func readCalls(t *testing.T, log string) [][]string {
	t.Helper()
	data, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	var call []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "--" {
			calls = append(calls, call)
			call = nil
			continue
		}
		call = append(call, line)
	}
	return calls
}

// startReaped starts a process and reaps it when it exits (kill -0 keeps
// seeing an unreaped zombie); exited receives the exit time.
func startReaped(t *testing.T, seconds string) (*exec.Cmd, <-chan time.Time) {
	t.Helper()
	cmd := exec.Command("/bin/sleep", seconds)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan time.Time, 1)
	go func() {
		_ = cmd.Wait()
		exited <- time.Now()
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, exited
}

// The reopen starts only after the quitting process is gone, opens a new
// instance of exactly this bundle and passes every argument through
// unchanged, spaces and quotes included.
func TestWaitThenOpenOpensOnceTheProcessHasExited(t *testing.T) {
	quitting, exited := startReaped(t, "0.6")
	opener, log := fakeOpener(t, "")
	bundle := "/tmp/dev builds/it's \"Isoform\".app"
	args := []string{"--env", "ISOFORM_RELAY_URL=http://127.0.0.1:8791", "--env", "X=$HOME;"}
	if err := waitThenOpen(quitting.Process.Pid, 50, opener, bundle, args).Run(); err != nil {
		t.Fatal(err)
	}
	ranAt := time.Now()
	select {
	case exitedAt := <-exited:
		if ranAt.Before(exitedAt) {
			t.Fatalf("opened at %v, before the process exited at %v", ranAt, exitedAt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the process did not exit")
	}
	want := append(append([]string{"-n"}, args...), bundle)
	if calls := readCalls(t, log); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("open calls = %q, want one call %q", calls, want)
	}
}

// A process that does not exit within the wait is left alone: no second
// copy is opened beside it.
func TestWaitThenOpenNeverOpensBesideARunningProcess(t *testing.T) {
	running, _ := startReaped(t, "30")
	opener, log := fakeOpener(t, "")
	if err := waitThenOpen(running.Process.Pid, 2, opener, "/tmp/Isoform.app", nil).Run(); err != nil {
		t.Fatalf("the wait ended with %v, want a clean exit", err)
	}
	if calls := readCalls(t, log); len(calls) != 0 {
		t.Fatalf("open was called %q while the process still ran", calls)
	}
}

// A failed open (an unknown option, a timeout after the app is already up)
// is never retried without the --env pairs: a development build without its
// flavor would run on the user's real data folder. Its status is the
// script's.
func TestWaitThenOpenNeverRetriesWithoutEnv(t *testing.T) {
	gone, exited := startReaped(t, "0")
	<-exited
	opener, log := fakeOpener(t, "--env")
	err := waitThenOpen(gone.Process.Pid, 5, opener, "/tmp/Isoform.app", []string{"--env", "YANSU_APP_FLAVOR=isoform"}).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("a failed open ended with %v, want open's exit status 3", err)
	}
	if calls := readCalls(t, log); len(calls) != 1 {
		t.Fatalf("open calls = %q, want exactly the one with --env", calls)
	}
}

// When the quit stalls past the wait, the shell ends with this process
// still alive: the waiter reports it, so the guard clears and the card stops
// saying it is reopening.
func TestStalledQuitClearsTheRelaunchGuard(t *testing.T) {
	running, _ := startReaped(t, "30")
	opener, log := fakeOpener(t, "")
	relaunchPending.Store(true)
	defer relaunchPending.Store(false)
	a := &App{}
	done := make(chan struct{})
	if err := startWaiter(waitThenOpen(running.Process.Pid, 2, opener, "/tmp/Isoform.app", nil), func() {
		a.relaunchGaveUp()
		close(done)
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled reopen was never reported")
	}
	if relaunchPending.Load() {
		t.Fatal("the relaunch guard is still set after the reopen gave up")
	}
	if calls := readCalls(t, log); len(calls) != 0 {
		t.Fatalf("open was called %q beside the running process", calls)
	}
}

// Screen Recording allowed after launch with macOS's "Later" (preflight
// still no) asks for the reopen instead of leaving the row on Turn on
// (fresh-install rehearsal, 2026-10-27).
func TestScreenRecordingGrantedWithLaterNeedsTheReopen(t *testing.T) {
	recorded, atLaunch, granted := screenRecordingLaunchRecorded.Load(), screenRecordingAtLaunch.Load(), screenRecordingGrantedNow
	t.Cleanup(func() {
		screenRecordingLaunchRecorded.Store(recorded)
		screenRecordingAtLaunch.Store(atLaunch)
		screenRecordingGrantedNow = granted
	})
	screenRecordingLaunchRecorded.Store(true)
	screenRecordingAtLaunch.Store(false)
	screenRecordingGrantedNow = func() bool { return true }
	if !screenRecordingNeedsRelaunch() {
		t.Fatal("a grant after launch did not ask for the reopen")
	}
	if got := setupPermissionCurrentState("denied", false || screenRecordingNeedsRelaunch(), screenRecordingNeedsRelaunch()); got != "onAfterReopen" {
		t.Fatalf("setup state %q, want onAfterReopen", got)
	}
	screenRecordingGrantedNow = func() bool { return false }
	if screenRecordingNeedsRelaunch() {
		t.Fatal("no grant still asked for the reopen")
	}
	screenRecordingAtLaunch.Store(true)
	screenRecordingGrantedNow = func() bool { return true }
	if screenRecordingNeedsRelaunch() {
		t.Fatal("on at launch asked for a reopen")
	}
}
