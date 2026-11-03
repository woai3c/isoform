package worktree

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
		{name: "default", args: []string{"status"}, want: GitCommandTimeoutDefault},
		{name: "merge", args: []string{"merge", "feature"}, want: GitCommandTimeoutLong},
		{name: "worktree", args: []string{"worktree", "add"}, want: GitCommandTimeoutLong},
		{name: "empty args", args: nil, want: GitCommandTimeoutDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GitCommandTimeout(tt.args); got != tt.want {
				t.Fatalf("GitCommandTimeout(%v) = %s, want %s", tt.args, got, tt.want)
			}
		})
	}
}

func TestUniqueDirPathNoConflict(t *testing.T) {
	base := filepath.Join(t.TempDir(), "worktrees", "project", "fix-bug")
	got := UniqueDirPath(base)
	if got != base {
		t.Fatalf("UniqueDirPath() = %q, want %q (no conflict)", got, base)
	}
}

func TestUniqueDirPathWithConflicts(t *testing.T) {
	base := filepath.Join(t.TempDir(), "worktrees", "project", "fix-bug")

	// Create base and -2 directories to simulate conflicts.
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	conflict2 := base + "-2"
	if err := os.MkdirAll(conflict2, 0o755); err != nil {
		t.Fatalf("mkdir conflict-2: %v", err)
	}

	got := UniqueDirPath(base)
	want := base + "-3"
	if got != want {
		t.Fatalf("UniqueDirPath() = %q, want %q", got, want)
	}
}

func TestUniqueBranchNameNoConflict(t *testing.T) {
	// Use a temp dir that is NOT a git repo — gitRefExists will return false.
	dir := t.TempDir()
	got := UniqueBranchName(dir, "workspace/fix-bug")
	if got != "workspace/fix-bug" {
		t.Fatalf("UniqueBranchName() = %q, want %q", got, "workspace/fix-bug")
	}
}

func TestUniqueBranchNameWithConflict(t *testing.T) {
	// Create a real git repo with a branch to conflict with.
	dir := t.TempDir()
	if _, err := RunGit(dir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := RunGit(dir, "commit", "--allow-empty", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	if _, err := RunGit(dir, "branch", "ws/fix-bug"); err != nil {
		t.Fatalf("git branch: %v", err)
	}

	got := UniqueBranchName(dir, "ws/fix-bug")
	if got != "ws/fix-bug-2" {
		t.Fatalf("UniqueBranchName() = %q, want %q", got, "ws/fix-bug-2")
	}
}

func TestUniqueBranchNameMultipleConflicts(t *testing.T) {
	dir := t.TempDir()
	if _, err := RunGit(dir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := RunGit(dir, "commit", "--allow-empty", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	// Create ws/fix-bug and ws/fix-bug-2.
	if _, err := RunGit(dir, "branch", "ws/fix-bug"); err != nil {
		t.Fatalf("git branch: %v", err)
	}
	if _, err := RunGit(dir, "branch", "ws/fix-bug-2"); err != nil {
		t.Fatalf("git branch -2: %v", err)
	}

	got := UniqueBranchName(dir, "ws/fix-bug")
	if got != "ws/fix-bug-3" {
		t.Fatalf("UniqueBranchName() = %q, want %q", got, "ws/fix-bug-3")
	}
}

func TestCanSafelyRemovePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worktrees")
	inside := filepath.Join(root, "project", "ws")
	outside := filepath.Join(t.TempDir(), "outside")

	if !CanSafelyRemovePath(inside, []string{root}) {
		t.Fatalf("expected inside path to be removable: %s", inside)
	}
	if CanSafelyRemovePath(root, []string{root}) {
		t.Fatalf("expected root path to be non-removable: %s", root)
	}
	if CanSafelyRemovePath(outside, []string{root}) {
		t.Fatalf("expected outside path to be blocked: %s", outside)
	}
}
