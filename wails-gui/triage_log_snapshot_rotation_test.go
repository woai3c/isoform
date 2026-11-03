package main

import (
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

func TestTriageLogSnapshotSurvivesConcurrentRotation(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", dataRoot)
	previousOutput := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	writer, err := newRotatingAppLog(filepath.Join(dataRoot, triageLogName), 300)
	if err != nil {
		t.Fatal(err)
	}
	triage.mu.Lock()
	previousFile, previousApp := triage.file, triage.app
	triage.file, triage.app = writer, nil
	triage.mu.Unlock()
	t.Cleanup(func() {
		triage.mu.Lock()
		defer triage.mu.Unlock()
		_ = writer.Close()
		triage.file, triage.app = previousFile, previousApp
	})

	// Each complete record fits one small file but two cannot. Fill all three
	// retained files so the next write both evicts the oldest and renames the
	// remaining snapshots, including the active file.
	reason := strings.Repeat("x", 200)
	for _, id := range []string{"before-1", "before-2", "before-3"} {
		logTriage("test", id, "accepted", reason)
	}
	rotated := false
	entries := readTriageLogs(func(reader io.Reader, appendEntry func(TriageLogEntry)) {
		if !rotated {
			if !triage.mu.TryLock() {
				t.Error("snapshot scan holds the writer lock")
				return
			}
			triage.mu.Unlock()
			logTriage("test", "after-rotation", "accepted", reason)
			rotated = true
		}
		scanTriageLogEntries(reader, appendEntry)
	})
	if len(entries) != 3 {
		t.Fatalf("snapshot retained %d entries, want all three before rotation", len(entries))
	}
	for i, want := range []string{"before-1", "before-2", "before-3"} {
		if entries[i].ID != want {
			t.Fatalf("snapshot entry %d = %q, want %q", i, entries[i].ID, want)
		}
	}
	latest := (&App{}).GetTriageLogs()
	if len(latest) != 3 {
		t.Fatalf("after rotation retained %d entries, want three", len(latest))
	}
	for i, want := range []string{"before-2", "before-3", "after-rotation"} {
		if latest[i].ID != want {
			t.Fatalf("rotation lost a decision: entry %d = %q, want %q", i, latest[i].ID, want)
		}
	}
}
