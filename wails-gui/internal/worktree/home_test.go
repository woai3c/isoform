package worktree

import (
	"path/filepath"
	"testing"
)

func TestDefaultWorktreeDirHonorsAgentHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	override := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", override)
	s := NewService(nil, Dependencies{})
	got, err := s.worktreeBaseDir()
	if err != nil || got != filepath.Join(override, "worktrees") {
		t.Fatalf("worktree root = %q, %v", got, err)
	}
}
