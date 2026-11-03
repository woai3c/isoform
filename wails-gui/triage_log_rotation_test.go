package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTriageLogRotatesDuringSessionAndKeepsRecentEntries(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "w4log-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	// An inherited YANSU_AGENT_HOME would win over HOME and point the test at a real data folder.
	t.Setenv("YANSU_AGENT_HOME", "")
	triage.mu.Lock()
	previousFile, previousApp := triage.file, triage.app
	triage.file, triage.app = nil, nil
	triage.mu.Unlock()
	t.Cleanup(func() {
		triage.mu.Lock()
		defer triage.mu.Unlock()
		if triage.file != nil {
			_ = triage.file.Close()
		}
		triage.file, triage.app = previousFile, previousApp
	})
	previousOutput := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	initTriageLog()

	// Fill more than three files in one process. Fixed-size diagnostic data
	// makes the rolling-window assertions independent of timestamps.
	const count = 700
	reason := strings.Repeat("x", 32*1024)
	for i := 0; i < count; i++ {
		logTriage("test", reason+fmt.Sprintf("entry-%04d", i), "accepted", reason)
	}
	path := filepath.Join(home, configDirName, triageLogName)
	for _, suffix := range []string{".old.2", ".old", ""} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatalf("missing retained log %s: %v", suffix, err)
		}
		if info.Size() > triageMaxLogSize {
			t.Fatalf("log %s exceeds in-session cap: %d > %d", suffix, info.Size(), triageMaxLogSize)
		}
	}
	entries := (&App{}).GetTriageLogs()
	if len(entries) < 300 || len(entries) > triageMaxLines {
		t.Fatalf("recent history should span rotation without exceeding the read limit: %d", len(entries))
	}
	for i, entry := range entries {
		wantID := reason + fmt.Sprintf("entry-%04d", count-len(entries)+i)
		if entry.ID != wantID || entry.Reason != fmt.Sprintf("reason_bytes=%d", len(reason)) {
			t.Fatalf("retained entry %d is incomplete or out of order: %s, want %s", i, entry.ID, wantID)
		}
	}
	for i := 0; i < count; i++ {
		logTriage("test", fmt.Sprintf("tail-%04d", i), "accepted", "metadata")
	}
	entries = (&App{}).GetTriageLogs()
	if len(entries) != triageMaxLines {
		t.Fatalf("read returned %d entries, want newest %d", len(entries), triageMaxLines)
	}
	for i, entry := range entries {
		if want := fmt.Sprintf("tail-%04d", count-triageMaxLines+i); entry.ID != want {
			t.Fatalf("tail entry %d = %s, want %s", i, entry.ID, want)
		}
	}
}

func TestTriageLogReadsLargeEntriesWithoutHidingTail(t *testing.T) {
	isolateActivityPrivacyLogs(t)
	reason := strings.Repeat("x", 96*1024)
	// Use a large opaque identifier to exercise record bounds without source prose.
	largeID := "large-" + reason
	logTriage("test", largeID, "accepted", reason)
	logTriage("test", "tail", "accepted", "metadata")
	entries := (&App{}).GetTriageLogs()
	if len(entries) != 2 || entries[0].ID != largeID || entries[0].Reason != fmt.Sprintf("reason_bytes=%d", len(reason)) || entries[1].ID != "tail" {
		t.Fatalf("large retained entry hid the later diagnostic: got %d entries", len(entries))
	}
}
