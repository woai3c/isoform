package singleinstance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMain points HOME (and the data-folder override the lock resolution
// reads) at a fresh temporary directory, so the lock and socket files can
// never land in the user's real data folder.
func TestMain(m *testing.M) {
	home := shortTestHome()
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	_ = os.Setenv("YANSU_AGENT_HOME", filepath.Join(home, ".yansu-agent"))
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// shortTestHome keeps the base directory short: the IPC socket path must stay
// under the macOS Unix socket length limit. Falls back to the default temp
// root where /tmp does not exist (Windows uses named mutexes and pipes, not
// filesystem sockets).
func shortTestHome() string {
	home, err := os.MkdirTemp("/tmp", "si-home-")
	if err != nil {
		home, err = os.MkdirTemp("", "si-home-")
	}
	if err != nil {
		panic(err)
	}
	return home
}

// isolateTestHome gives the test its own base folder for the lock and IPC
// files, so the test stays clean even when run in a way that bypasses the
// package TestMain. The short /tmp root keeps socket paths bindable on macOS.
func isolateTestHome(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "si-")
	if err != nil {
		t.Setenv("YANSU_AGENT_HOME", t.TempDir())
		return
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("YANSU_AGENT_HOME", dir)
}

// TestAcquireRejectsSecondCaller verifies that once Acquire succeeds, a
// second call with the same appID returns ErrAlreadyRunning, and that
// releasing the first instance lets a third call succeed again.
func TestAcquireRejectsSecondCaller(t *testing.T) {
	isolateTestHome(t)
	const appID = "yansu-desktop-test-lock"

	first, err := Acquire(appID)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	if _, err := Acquire(appID); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Acquire: want ErrAlreadyRunning, got %v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	third, err := Acquire(appID)
	if err != nil {
		t.Fatalf("third Acquire after Release: %v", err)
	}
	defer third.Release()
}

// TestSendDeliversMessage verifies the IPC round-trip: Send from a "second
// launch" reaches the running instance's Messages() channel intact.
func TestSendDeliversMessage(t *testing.T) {
	isolateTestHome(t)
	const appID = "yansu-desktop-test-ipc"

	inst, err := Acquire(appID)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer inst.Release()

	if err := Send(appID, "focus"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-inst.Messages():
		if got != "focus" {
			t.Fatalf("Messages received %q, want %q", got, "focus")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Messages timeout — IPC not delivered")
	}
}

// TestSendWithoutListener errors cleanly when no instance is running.
func TestSendWithoutListener(t *testing.T) {
	isolateTestHome(t)
	const appID = "yansu-desktop-test-no-listener"
	if err := Send(appID, "focus"); err == nil {
		t.Fatal("Send to nonexistent listener should error")
	}
}
