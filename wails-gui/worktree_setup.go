package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	"wails-gui/internal/platform/shellenv"
)

const (
	defaultSetupTimeoutMS = 10 * 60 * 1000 // 10 minutes
	maxSetupOutputBytes   = 64 * 1024      // 64 KB tail
)

// SetupCommandResult holds the outcome of a worktree setup command.
type SetupCommandResult struct {
	WorkspaceID string `json:"workspaceId"`
	Success     bool   `json:"success"`
	Output      string `json:"output,omitempty"`
	Error       string `json:"error,omitempty"`
}

// resolveSetupCommand returns the setup command to run for a workspace.
// Priority: SQLite per-project > project.json > global config > empty.
// dbCommand is the value from SQLite (pass "" if unavailable).
func resolveSetupCommand(dbCommand, projectPath string) string {
	if dbCommand != "" {
		return dbCommand
	}
	if cmd := loadProjectSetupCommand(projectPath); cmd != "" {
		return cmd
	}
	return loadConfig().WorktreeSetupCommand
}

// resolveSetupTimeout returns the setup timeout duration.
func resolveSetupTimeout() time.Duration {
	ms := loadConfig().WorktreeSetupTimeoutMS
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return time.Duration(defaultSetupTimeoutMS) * time.Millisecond
}

// runWorktreeSetupCommand executes the configured setup command in the
// given worktree directory. Returns nil if no command is configured.
// dbCommand is the per-project setup command from SQLite (may be empty).
func runWorktreeSetupCommand(dbCommand, projectPath, worktreePath, workspaceID string) *SetupCommandResult {
	command := resolveSetupCommand(dbCommand, projectPath)
	if command == "" {
		return nil
	}

	timeout := resolveSetupTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := shellenv.BuildSetupCommand(ctx, command)
	cmd.Dir = worktreePath

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	log.Printf("[worktree setup] running %q in %s (timeout %s)", command, worktreePath, timeout)

	err := cmd.Run()

	// Truncate output to last 64KB.
	output := buf.Bytes()
	if len(output) > maxSetupOutputBytes {
		output = output[len(output)-maxSetupOutputBytes:]
	}
	outputStr := string(output)

	if err != nil {
		errMsg := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = fmt.Sprintf("setup command timed out after %s", timeout.Round(time.Second))
		}
		log.Printf("[worktree setup] command failed: %s", errMsg)
		return &SetupCommandResult{
			WorkspaceID: workspaceID,
			Success:     false,
			Output:      outputStr,
			Error:       errMsg,
		}
	}

	log.Printf("[worktree setup] command succeeded")
	return &SetupCommandResult{
		WorkspaceID: workspaceID,
		Success:     true,
		Output:      outputStr,
	}
}
