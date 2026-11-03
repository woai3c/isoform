package cmdutil

import (
	"context"
	"testing"
)

func TestGitPathDefaultFallback(t *testing.T) {
	// Without a resolver set, GitPath should return "git".
	gitPathResolverMu.Lock()
	old := gitPathResolver
	gitPathResolver = nil
	gitPathResolverMu.Unlock()
	defer func() {
		gitPathResolverMu.Lock()
		gitPathResolver = old
		gitPathResolverMu.Unlock()
	}()

	got := GitPath()
	if got != "git" {
		t.Fatalf("GitPath() = %q, want %q", got, "git")
	}
}

func TestGitPathWithResolver(t *testing.T) {
	SetGitPathResolver(func() string { return "/custom/bin/git" })
	defer func() {
		gitPathResolverMu.Lock()
		gitPathResolver = nil
		gitPathResolverMu.Unlock()
	}()

	got := GitPath()
	if got != "/custom/bin/git" {
		t.Fatalf("GitPath() = %q, want %q", got, "/custom/bin/git")
	}
}

func TestGitPathResolverReturnsEmpty(t *testing.T) {
	// If resolver returns empty string, should fall back to "git".
	SetGitPathResolver(func() string { return "" })
	defer func() {
		gitPathResolverMu.Lock()
		gitPathResolver = nil
		gitPathResolverMu.Unlock()
	}()

	got := GitPath()
	if got != "git" {
		t.Fatalf("GitPath() = %q, want %q (empty resolver should fall back)", got, "git")
	}
}

func TestGitCommandUsesResolvedPath(t *testing.T) {
	SetGitPathResolver(func() string { return "/resolved/git" })
	defer func() {
		gitPathResolverMu.Lock()
		gitPathResolver = nil
		gitPathResolverMu.Unlock()
	}()

	cmd := GitCommand("status", "--short")
	if cmd.Path != "/resolved/git" && cmd.Args[0] != "/resolved/git" {
		t.Fatalf("GitCommand().Args[0] = %q, want %q", cmd.Args[0], "/resolved/git")
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "status" || cmd.Args[2] != "--short" {
		t.Fatalf("GitCommand() args = %v, want [/resolved/git status --short]", cmd.Args)
	}
}

func TestGitCommandContextUsesResolvedPath(t *testing.T) {
	SetGitPathResolver(func() string { return "/resolved/git" })
	defer func() {
		gitPathResolverMu.Lock()
		gitPathResolver = nil
		gitPathResolverMu.Unlock()
	}()

	ctx := context.Background()
	cmd := GitCommandContext(ctx, "log", "--oneline")
	if cmd.Args[0] != "/resolved/git" {
		t.Fatalf("GitCommandContext().Args[0] = %q, want %q", cmd.Args[0], "/resolved/git")
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "log" || cmd.Args[2] != "--oneline" {
		t.Fatalf("GitCommandContext() args = %v, want [/resolved/git log --oneline]", cmd.Args)
	}
}

func TestSetGitPathResolverOverrides(t *testing.T) {
	SetGitPathResolver(func() string { return "/first" })
	if got := GitPath(); got != "/first" {
		t.Fatalf("after first set, GitPath() = %q, want /first", got)
	}

	SetGitPathResolver(func() string { return "/second" })
	defer func() {
		gitPathResolverMu.Lock()
		gitPathResolver = nil
		gitPathResolverMu.Unlock()
	}()

	if got := GitPath(); got != "/second" {
		t.Fatalf("after second set, GitPath() = %q, want /second", got)
	}
}
