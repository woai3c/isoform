package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// agentWorkspacePath resolves the separate directory for agent-authored files.
// Creation and the one-time legacy move happen in prepareAgentWorkspace.
func agentWorkspacePath(kind, id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("invalid workspace ID")
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", fmt.Errorf("cannot resolve the home folder for the agent workspace")
	}
	dir := filepath.Join(home, ".isoform-workspaces", appFlavor(), kind, id)
	if err := validateCronProjectDir(dir); err != nil {
		return "", fmt.Errorf("agent workspace: %w", err)
	}
	return dir, nil
}

func prepareAgentWorkspace(kind, id string) (string, error) {
	dir, err := agentWorkspacePath(kind, id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := migrateLegacyAgentWorkspace(kind, id, dir); err != nil {
		return "", fmt.Errorf("migrate agent workspace: %w", err)
	}
	return dir, nil
}
