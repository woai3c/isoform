package ocr

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Helpers for the Windows OCR engines, which hand each frame to a sidecar
// through a temp JPEG and read one response line back. They build on every
// platform so they can be tested anywhere.

// removeOCRTempFile removes the temp JPEG written for one OCR call. The PII
// mask OCRs a frame before masking it, so a failure is logged rather than
// ignored: the file holds an unmasked screenshot.
func removeOCRTempFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("[ocr] failed to remove temp frame %s: %v", path, err)
	}
}

// ownedTempDirPattern returns the os.MkdirTemp pattern for a temp folder owned
// by this process: prefix, the pid, then a random suffix. The pid lets
// sweepStaleTempDirs tell a live engine's folder from a dead run's.
func ownedTempDirPattern(prefix string) string {
	return prefix + strconv.Itoa(os.Getpid()) + "-*"
}

// tempDirOwner returns the pid in a folder name made from
// ownedTempDirPattern, or 0 for a folder without one (older builds).
func tempDirOwner(name, prefix string) int {
	rest := strings.TrimPrefix(name, prefix)
	digits, _, found := strings.Cut(rest, "-")
	if !found {
		return 0
	}
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// sweepStaleTempDirs removes the folders in root whose name starts with prefix
// and that no running process owns: temp folders of earlier runs, which exit
// without cleaning up. A folder named with a live pid is kept (another
// instance, or this one). A folder without a pid (older builds) is removed
// once it was last modified before olderThan. Returns the number of folders
// removed.
func sweepStaleTempDirs(root, prefix string, olderThan time.Time) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if pid := tempDirOwner(e.Name(), prefix); pid != 0 {
			if processAlive(pid) {
				continue
			}
		} else {
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(olderThan) {
				continue
			}
		}
		path := filepath.Join(root, e.Name())
		if err := os.RemoveAll(path); err != nil {
			log.Printf("[ocr] failed to remove stale temp folder %s: %v", path, err)
			continue
		}
		removed++
	}
	return removed
}

// scanLineWithin reads the next line from sc, giving up after timeout. On
// timeout it calls stop, which must make the read end (by killing the process
// that feeds sc), waits briefly for it, and reports timedOut. A sidecar that
// hangs therefore fails the call instead of blocking it, and everything
// queued behind it, forever.
func scanLineWithin(sc *bufio.Scanner, timeout time.Duration, stop func()) (ok, timedOut bool) {
	done := make(chan bool, 1)
	go func() { done <- sc.Scan() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ok := <-done:
		return ok, false
	case <-timer.C:
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			// The read is still blocked; the scanner is abandoned with the
			// killed process and the next call starts a fresh one.
		}
		return false, true
	}
}
