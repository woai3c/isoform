package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupUnmigratedSessionRemovesLegacyFiles(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-root", true: "custom-root"}[override], func(t *testing.T) {
			home := shortGuardTestHome(t)
			if override {
				t.Setenv(agentHomeEnv, filepath.Join(home, "custom-agent-data"))
			}
			root, err := flavorDataDir()
			if err != nil {
				t.Fatal(err)
			}
			const tag = "never-opened-after-upgrade"
			legacy := filepath.Join(root, "chats", tag)
			other := filepath.Join(root, "chats", "keep-other-chat", "report.md")
			automation := filepath.Join(root, "automations", tag, "report.md")
			for _, path := range []string{filepath.Join(legacy, "nested", "report.md"), other, automation} {
				writeWorkspaceMigrationFile(t, path, "original report")
			}
			app := &App{}
			stable, err := app.chatSessionWorkDir(tag)
			if err != nil {
				t.Fatal(err)
			}
			app.sessionWorkDirRemoveAll = func(path string) error {
				if _, err := os.Lstat(stable); !os.IsNotExist(err) {
					t.Errorf("deletion created a workspace before removing files: %v", err)
				}
				return os.RemoveAll(path)
			}
			app.CleanupSessionTempDir(tag)
			for _, path := range []string{legacy, stable} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Errorf("deleted session directory still exists (or was migrated): %s: %v", path, err)
				}
			}
			for _, path := range []string{other, automation} {
				if body, err := os.ReadFile(path); err != nil || string(body) != "original report" {
					t.Errorf("unrelated report changed: %s: %q %v", path, body, err)
				}
			}
		})
	}
}

func TestCleanupLegacySessionDoesNotFollowParentAlias(t *testing.T) {
	home := shortGuardTestHome(t)
	root := filepath.Join(home, "custom-data")
	t.Setenv(agentHomeEnv, root)
	const tag = "keep"
	report := filepath.Join(home, "unrelated", tag, "report.md")
	writeWorkspaceMigrationFile(t, report, "original report")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(filepath.Dir(report)), filepath.Join(root, "chats")); err != nil {
		t.Fatal(err)
	}
	(&App{}).CleanupSessionTempDir(tag)
	if body, err := os.ReadFile(report); err != nil || string(body) != "original report" {
		t.Fatalf("cleanup followed a legacy parent alias: %q %v", body, err)
	}
}
