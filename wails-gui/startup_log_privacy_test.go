package main

import (
	"os"
	"strings"
	"testing"
)

func TestStartupTriageLogWithoutSingletonLock(t *testing.T) {
	_, path := isolateActivityPrivacyLogs(t)
	closeTriageLog()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	closeLogs := initStartupLogs(false, false)
	t.Cleanup(closeLogs)
	logTriage("test", "lock-unavailable", "accepted", "metadata")
	entries := (&App{}).GetTriageLogs()
	if len(entries) != 1 || entries[0].ID != "lock-unavailable" {
		t.Fatalf("startup without the singleton lock lost triage history: %+v", entries)
	}
}

func TestBindingsStartupDoesNotOpenTriageLog(t *testing.T) {
	_, path := isolateActivityPrivacyLogs(t)
	closeTriageLog()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	closeLogs := initStartupLogs(true, false)
	t.Cleanup(closeLogs)
	logTriage("test", "bindings", "accepted", "metadata")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("binding introspection created triage.log: %v", err)
	}
}

func TestPrivateErrorLogsRetainKindsWithoutContent(t *testing.T) {
	for _, flag := range []string{"", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			if flag == "" {
				if err := os.Unsetenv(backgroundTranscriptDebugEnv); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct{ message, kind string }{
				{"database is locked (SQLITE_BUSY)", "locked"},
				{"database table is locked (SQLITE_LOCKED)", "locked"},
				{"context deadline exceeded", "timeout"},
				{"provider request timed out", "timeout"},
				{"context canceled", "canceled"},
				{"open: permission denied", "permission"},
				{"open: no such file or directory", "not_found"},
				{"read: input/output error", "io"},
				{"write: broken pipe", "io"},
				{"provider unavailable", "unavailable"},
				{"malformed model response", "parse"},
				{`parse "private-response": unexpected end of JSON input`, "parse"},
				{"unexpected provider failure", "unknown"},
			} {
				const secret = "private-source-amber"
				field := activityPrivateLogField("error", tc.message+": "+secret)
				if !strings.Contains(field, "error_kind="+tc.kind+" ") || !strings.Contains(field, "error_bytes=") {
					t.Errorf("missing safe error diagnosis for %q: %s", tc.message, field)
				}
				if strings.Contains(field, secret) != (flag == "1") {
					t.Errorf("debug=%q: content visibility in %s", flag, field)
				}
			}
		})
	}
	// Words in a non-error source field must not be classified as failures.
	t.Setenv(backgroundTranscriptDebugEnv, "0")
	if got := activityPrivateLogField("title", "database is locked"); strings.Contains(got, "kind=") {
		t.Fatalf("non-error text acquired an error classification: %s", got)
	}
}
