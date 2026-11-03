package ocr

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A hung sidecar fails the call instead of blocking it forever; stop is what
// unblocks the read.
func TestScanLineWithinTimesOutAndStops(t *testing.T) {
	r, w := io.Pipe()
	stopped := false
	start := time.Now()
	ok, timedOut := scanLineWithin(bufio.NewScanner(r), 50*time.Millisecond, func() {
		stopped = true
		_ = w.Close()
	})
	if ok || !timedOut || !stopped {
		t.Fatalf("ok=%v timedOut=%v stopped=%v, want a timeout that called stop", ok, timedOut, stopped)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %s, want about the timeout", elapsed)
	}
}

func TestScanLineWithinReadsALine(t *testing.T) {
	r, w := io.Pipe()
	go func() { _, _ = w.Write([]byte("{\"code\":100}\n")) }()
	sc := bufio.NewScanner(r)
	ok, timedOut := scanLineWithin(sc, time.Second, func() { t.Error("stop called on a timely response") })
	if !ok || timedOut || sc.Text() != `{"code":100}` {
		t.Fatalf("ok=%v timedOut=%v line=%q, want the line", ok, timedOut, sc.Text())
	}
}

// Temp folders of dead runs are removed; folders of live processes stay
// however old, and folders from older builds (no pid) go only once stale.
func TestSweepStaleTempDirs(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, age time.Duration) string {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ocr-input.jpg"), []byte{1}, 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Skipf("cannot start a short-lived process: %v", err)
	}
	deadPID := dead.Process.Pid

	const prefix = "yansu-paddle-ocr-"
	liveOld := mk(prefix+strconv.Itoa(os.Getpid())+"-123", 3*time.Hour)
	deadRecent := mk(prefix+strconv.Itoa(deadPID)+"-456", time.Minute)
	legacyOld := mk(prefix+"111", 3*time.Hour)
	legacyRecent := mk(prefix+"222", time.Minute)
	other := mk("something-else", 3*time.Hour)

	if got := sweepStaleTempDirs(root, prefix, time.Now().Add(-time.Hour)); got != 2 {
		t.Fatalf("removed %d folders, want 2 (dead owner, stale legacy)", got)
	}
	for _, dir := range []string{deadRecent, legacyOld} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s kept: %v", dir, err)
		}
	}
	for _, dir := range []string{liveOld, legacyRecent, other} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s must stay: %v", dir, err)
		}
	}
}

func TestOwnedTempDirPattern(t *testing.T) {
	dir, err := os.MkdirTemp(t.TempDir(), ownedTempDirPattern("yansu-paddle-ocr-"))
	if err != nil {
		t.Fatal(err)
	}
	if got := tempDirOwner(filepath.Base(dir), "yansu-paddle-ocr-"); got != os.Getpid() {
		t.Fatalf("tempDirOwner(%q) = %d, want this pid %d", filepath.Base(dir), got, os.Getpid())
	}
	if got := tempDirOwner("yansu-paddle-ocr-123456789", "yansu-paddle-ocr-"); got != 0 {
		t.Fatalf("tempDirOwner(legacy) = %d, want 0", got)
	}
}

func TestRemoveOCRTempFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ocr-input.jpg")
	if err := os.WriteFile(path, []byte{1}, 0o600); err != nil {
		t.Fatal(err)
	}
	removeOCRTempFile(path)
	removeOCRTempFile(path) // already gone: no error, no log
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp frame not removed: %v", err)
	}
}
