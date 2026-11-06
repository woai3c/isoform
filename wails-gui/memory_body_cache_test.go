package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The injection path reads each memory file's bytes once per change, and an edit that
// changes the size or the modification time is read again (R24).
func TestMemoryBodyCacheReadsAgainAfterAnEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readMemoryBody(path)
	if err != nil || string(got) != "first" {
		t.Fatalf("first read = %q, %v", got, err)
	}
	got, _ = readMemoryBody(path)
	if string(got) != "first" {
		t.Fatalf("unchanged read = %q", got)
	}
	if err := os.WriteFile(path, []byte("second, longer"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _ = readMemoryBody(path)
	if string(got) != "second, longer" {
		t.Fatalf("an edited file was served from the cache: %q", got)
	}
}

// A missing file is reported as missing, as before the cache.
func TestMemoryBodyCacheKeepsMissingFilesMissing(t *testing.T) {
	_, err := readMemoryBody(filepath.Join(t.TempDir(), "gone.md"))
	if !os.IsNotExist(err) {
		t.Fatalf("missing file error = %v", err)
	}
}
