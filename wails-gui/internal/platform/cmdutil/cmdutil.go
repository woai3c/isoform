// Package cmdutil provides exec.Command wrappers that automatically hide the
// console window on Windows. On other platforms the wrappers are simple
// pass-throughs.
package cmdutil

import (
	"context"
	"os/exec"
	"sync"
)

// gitPathResolver is set by the main package at startup to provide the
// resolved git binary path. This avoids an import cycle (main -> cmdutil).
var (
	gitPathResolverMu sync.RWMutex
	gitPathResolver   func() string
)

// SetGitPathResolver registers the function that returns the resolved git path.
// Must be called once at startup from the main package.
func SetGitPathResolver(fn func() string) {
	gitPathResolverMu.Lock()
	defer gitPathResolverMu.Unlock()
	gitPathResolver = fn
}

// GitPath returns the resolved git binary path. Falls back to "git" if no
// resolver is set (e.g. during tests).
func GitPath() string {
	gitPathResolverMu.RLock()
	fn := gitPathResolver
	gitPathResolverMu.RUnlock()
	if fn != nil {
		if p := fn(); p != "" {
			return p
		}
	}
	return "git"
}

// GitCommand creates a git command using the resolved git binary path.
func GitCommand(arg ...string) *exec.Cmd {
	return Command(GitPath(), arg...)
}

// GitCommandContext creates a git command with context using the resolved git binary path.
func GitCommandContext(ctx context.Context, arg ...string) *exec.Cmd {
	return CommandContext(ctx, GitPath(), arg...)
}

// Command is a drop-in replacement for exec.Command that hides the console
// window on Windows.
func Command(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	hideConsoleWindow(cmd)
	return cmd
}

// CommandContext is a drop-in replacement for exec.CommandContext that hides
// the console window on Windows.
func CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	hideConsoleWindow(cmd)
	return cmd
}

// StartWithJobObject starts a command inside a process job (Windows Job Object)
// so that all descendant processes inherit the parent's hidden-window state.
// On non-Windows platforms this is equivalent to cmd.Start().
//
// The returned cleanup function MUST be called after cmd.Wait() completes to
// release system resources (the Job Object handle on Windows).
//
// Usage:
//
//	cmd := cmdutil.CommandContext(ctx, "git", "clone", ...)
//	cleanup, err := cmdutil.StartWithJobObject(cmd)
//	if err != nil { return err }
//	defer cleanup()
//	err = cmd.Wait()
func StartWithJobObject(cmd *exec.Cmd) (cleanup func(), err error) {
	return startWithJobObject(cmd)
}
