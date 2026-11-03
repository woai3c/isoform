//go:build !windows

package shellenv

import (
	"context"
	"os/exec"
)

// BuildSetupCommand creates a command that runs the setup script via a login shell.
func BuildSetupCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, LoginShell(), "-l", "-c", command)
}
