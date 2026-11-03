//go:build windows

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRetireRotatingLogFileAllowsImmediateReuseWithOpenSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "triage.log.old.2")
	if err := os.WriteFile(path, []byte("snapshot history"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := openTriageLogSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snapshot.Close() })
	if err := retireRotatingLogFile(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("next retained file"), 0600); err != nil {
		t.Fatalf("retired rotation name cannot be reused while snapshot is open: %v", err)
	}
	content, err := io.ReadAll(snapshot)
	if err != nil || string(content) != "snapshot history" {
		t.Fatalf("snapshot content = %q, error = %v", content, err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != filepath.Base(path) {
		t.Fatalf("retirement left temporary files after snapshot closed: %v", files)
	}
}

func TestRetireRotatingLogFileMissingLeavesNoPlaceholder(t *testing.T) {
	dir := t.TempDir()
	err := retireRotatingLogFile(filepath.Join(dir, "triage.log.old.2"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing retained file error = %v, want not-exist", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("missing retained file left placeholders: %v", files)
	}
}

func TestAppLogStartupCleansOnlyItsAbandonedRetirements(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "triage.log")
	abandoned := path + ".old.2.retired-abandoned"
	if err := os.WriteFile(abandoned, []byte("abandoned history"), 0600); err != nil {
		t.Fatal(err)
	}
	preserved := map[string]string{
		path:                           "active history",
		path + ".old":                  "previous history",
		path + ".old.2":                "oldest history",
		path + ".old.2.retired":        "different name",
		path + ".old.retired-other":    "different suffix",
		path + "2.old.2.retired-other": "another log",
	}
	for name, content := range preserved {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	preservedDirectory := path + ".old.2.retired-directory"
	if err := os.Mkdir(preservedDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	writer, err := newRotatingAppLog(path, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned retirement was not removed: %v", err)
	}
	for name, want := range preserved {
		got, err := os.ReadFile(name)
		if err != nil || string(got) != want {
			t.Errorf("preserved %s = %q, error = %v", filepath.Base(name), got, err)
		}
	}
	if info, err := os.Stat(preservedDirectory); err != nil || !info.IsDir() {
		t.Fatalf("retirement cleanup changed a directory: %v", err)
	}
}
