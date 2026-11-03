package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGitCommandTimeout(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want time.Duration
	}{
		{name: "default", args: []string{"status"}, want: gitCommandTimeoutDefault},
		{name: "merge", args: []string{"merge", "feature"}, want: gitCommandTimeoutLong},
		{name: "worktree", args: []string{"worktree", "add"}, want: gitCommandTimeoutLong},
		{name: "checkout", args: []string{"checkout", "branch"}, want: gitCommandTimeoutLong},
		{name: "empty args", args: nil, want: gitCommandTimeoutDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gitCommandTimeout(tt.args)
			if got != tt.want {
				t.Fatalf("gitCommandTimeout(%v) = %s, want %s", tt.args, got, tt.want)
			}
		})
	}
}

func TestGitCommandTimeoutFromEnv(t *testing.T) {
	t.Setenv("YANSU_GIT_TIMEOUT_MS", "250")
	got := gitCommandTimeoutFromEnv(5 * time.Second)
	if got != 250*time.Millisecond {
		t.Fatalf("gitCommandTimeoutFromEnv() = %s, want %s", got, 250*time.Millisecond)
	}

	t.Setenv("YANSU_GIT_TIMEOUT_MS", "bad-value")
	got = gitCommandTimeoutFromEnv(7 * time.Second)
	if got != 7*time.Second {
		t.Fatalf("gitCommandTimeoutFromEnv() with invalid value = %s, want fallback %s", got, 7*time.Second)
	}
}

func TestGetWorktreeSettingsDefaultCloneTimeoutEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a := &App{}
	settings := a.GetWorktreeSettings()
	if settings.GitCloneTimeoutMinutes != "" {
		t.Fatalf("GetWorktreeSettings().GitCloneTimeoutMinutes = %q, want empty", settings.GitCloneTimeoutMinutes)
	}
}

func TestCanSafelyRemoveWorktreePathDefaultRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	defaultRoot := filepath.Join(home, configDirName, "worktrees")
	managedPath := filepath.Join(defaultRoot, "project-a", "ws-a")
	outsidePath := filepath.Join(home, "Downloads", "not-worktree")

	if !canSafelyRemoveWorktreePath(managedPath) {
		t.Fatalf("expected managed path to be removable: %s", managedPath)
	}
	if canSafelyRemoveWorktreePath(defaultRoot) {
		t.Fatalf("expected root path to be non-removable: %s", defaultRoot)
	}
	if canSafelyRemoveWorktreePath(outsidePath) {
		t.Fatalf("expected unmanaged path to be blocked: %s", outsidePath)
	}
	if canSafelyRemoveWorktreePath(string(filepath.Separator)) {
		t.Fatalf("expected filesystem root to be blocked")
	}
}

func TestCanSafelyRemoveWorktreePathWithConfiguredRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	customRoot := filepath.Join(home, "custom-worktrees")
	if err := saveConfig(yansuConfig{WorktreeRoot: customRoot}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	customManaged := filepath.Join(customRoot, "project-b", "ws-b")
	defaultManaged := filepath.Join(home, configDirName, "worktrees", "project-c", "ws-c")

	if !canSafelyRemoveWorktreePath(customManaged) {
		t.Fatalf("expected configured root path to be removable: %s", customManaged)
	}
	if !canSafelyRemoveWorktreePath(defaultManaged) {
		t.Fatalf("expected default root path to remain removable for backward compatibility: %s", defaultManaged)
	}
}

func TestRemoveWorktreePathSafe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	defaultRoot := filepath.Join(home, configDirName, "worktrees")
	managedPath := filepath.Join(defaultRoot, "project-d", "ws-d")
	if err := os.MkdirAll(managedPath, 0o755); err != nil {
		t.Fatalf("mkdir managed path: %v", err)
	}
	marker := filepath.Join(managedPath, "marker.txt")
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := removeWorktreePathSafe(managedPath); err != nil {
		t.Fatalf("removeWorktreePathSafe(managed): %v", err)
	}
	if _, err := os.Stat(managedPath); !os.IsNotExist(err) {
		t.Fatalf("expected managed path to be deleted, stat err=%v", err)
	}

	unmanagedPath := filepath.Join(home, "Desktop", "keep-me")
	if err := os.MkdirAll(unmanagedPath, 0o755); err != nil {
		t.Fatalf("mkdir unmanaged path: %v", err)
	}
	if err := removeWorktreePathSafe(unmanagedPath); err == nil {
		t.Fatalf("expected unmanaged path removal to fail")
	}
	if _, err := os.Stat(unmanagedPath); err != nil {
		t.Fatalf("expected unmanaged path to remain, stat err=%v", err)
	}
}
