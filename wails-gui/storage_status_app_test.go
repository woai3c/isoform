package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewAppLeavesStoragePendingUntilWindowStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", home)
	app := NewApp()
	t.Cleanup(func() {
		app.storageStartup.stop()
		if app.telemetry != nil {
			app.telemetry.Close()
		}
	})
	if got := app.GetStorageStartupStatus().Code; got != "checking" {
		t.Fatalf("NewApp status = %q, want checking before Wails starts", got)
	}
	if app.store != nil {
		t.Fatal("NewApp opened storage before the progress window")
	}
	if !app.IsClosedEyesModeEnabled() {
		t.Fatal("unknown persisted observation policy must withhold governed reads")
	}
	if _, err := os.Stat(filepath.Join(home, "sessions.db")); !os.IsNotExist(err) {
		t.Fatalf("NewApp created a database before window startup: %v", err)
	}
}
