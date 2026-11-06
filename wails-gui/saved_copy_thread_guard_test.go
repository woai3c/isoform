package main

import (
	"os"
	"strings"
	"testing"
)

// The saved-view refresh runs on a plain goroutine. A goroutine that locks its
// OS thread and exits without unlocking has its thread killed by the Go
// runtime, and the saved views once crashed the app that way. The refresh paths
// must never lock a thread or change a thread's priority.
func TestSavedViewRefreshPathsNeverLockOrReprioritizeAThread(t *testing.T) {
	banned := []string{"LockOSThread", "UnlockOSThread", "Setpriority", "lowerCurrentThreadPriority"}
	for _, file := range []string{"topic_finds_snapshot.go", "stream_page_snapshot.go", "saved_copy_stamp.go"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, word := range banned {
			if strings.Contains(string(raw), word) {
				t.Fatalf("%s uses %s; the refresh must run on a plain goroutine", file, word)
			}
		}
	}
	for _, file := range []string{"topic_finds_priority_darwin.go", "topic_finds_priority_other.go"} {
		if _, err := os.Stat(file); err == nil {
			t.Fatalf("%s should not exist: no thread-priority trick in the refresh", file)
		}
	}
}
