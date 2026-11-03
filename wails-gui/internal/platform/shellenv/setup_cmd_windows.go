//go:build windows

package shellenv

import (
	"context"
	"os"
	"os/exec"

	"wails-gui/internal/platform/cmdutil"
)

// BuildSetupCommand creates a command that runs the setup script via cmd.exe on Windows.
func BuildSetupCommand(ctx context.Context, command string) *exec.Cmd {
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	return cmdutil.CommandContext(ctx, comspec, "/C", command)
}
