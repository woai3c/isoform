//go:build !darwin

package main

import (
	"context"
	"os/exec"

	"wails-gui/internal/platform/cmdutil"
)

// withBackgroundPriority runs fn; only macOS lowers the thread's priority.
func withBackgroundPriority(fn func()) { fn() }

// lowPriorityCommand starts name; only macOS lowers its priority.
func lowPriorityCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return cmdutil.CommandContext(ctx, name, args...)
}
