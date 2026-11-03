//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A hard link in the work folder can name a file outside it, and a FIFO
// would block the notify goroutine: neither is uploaded.
func TestUploadRefusesHardLinksAndFIFOs(t *testing.T) {
	workDir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workDir, "copy.txt")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	fifo := filepath.Join(workDir, "pipe.txt")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan struct{})
	var files []deliverableFile
	var skipped []string
	go func() {
		files, skipped = readDeliverableFiles([]string{link, fifo}, workDir, "", touchedBy(t, link, fifo))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("reading deliverables blocked on a FIFO")
	}
	if len(files) != 0 {
		t.Fatalf("uploaded %+v, want nothing", files)
	}
	joined := strings.Join(skipped, "\n")
	if !strings.Contains(joined, "has other hard links") || !strings.Contains(joined, "not a regular file") {
		t.Fatalf("skipped = %v, want hard-link and FIFO reasons", skipped)
	}
}
