package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

// TestWriteInstallDiscoveryWritesParseableJSON locks in the install.json
// schema that external tools (most notably the Yansu skill) depend on for
// locating the running desktop app. If you intentionally rename a field, the
// matching schema in yansu-skill/SKILL.md must move with it.
func TestWriteInstallDiscoveryWritesParseableJSON(t *testing.T) {
	isolatedYansuCLIHome(t)
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}

	writeInstallDiscovery()

	discoveryPath := filepath.Join(configDir, installDiscoveryDirName(), installDiscoveryFileName)
	data, err := os.ReadFile(discoveryPath)
	if err != nil {
		t.Fatalf("install.json not written at %s: %v", discoveryPath, err)
	}

	var got installDiscovery
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("install.json is not valid JSON: %v\npayload: %s", err, data)
	}

	if got.AppPath == "" {
		t.Error("AppPath is empty — discovery is useless without it")
	}
	if got.AppDir == "" {
		t.Error("AppDir is empty — discovery is useless without it")
	}
	if got.Platform != goruntime.GOOS {
		t.Errorf("Platform = %q, want %q", got.Platform, goruntime.GOOS)
	}
	if got.Version == "" {
		t.Error("Version is empty")
	}
	if got.WrittenAt == "" {
		t.Error("WrittenAt is empty")
	}
}
