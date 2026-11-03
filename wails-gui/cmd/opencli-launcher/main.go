// opencli-launcher runs the shipped JS package with the shipped Bun runtime.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

func runtimePaths(executable string) (string, []string) {
	root := filepath.Dir(filepath.Dir(executable))
	bun := "bun"
	if runtime.GOOS == "windows" {
		bun += ".exe"
	}
	return filepath.Join(root, "node_modules", "@jackwener", "opencli", "dist", "src", "main.js"), []string{
		filepath.Join(root, "..", "bun-bundle", "bin", bun), // app/portable bundle
		filepath.Join(root, "..", "bin", bun),               // installed tools
	}
}

func command(executable string, args, environment []string) (*exec.Cmd, error) {
	entry, candidates := runtimePaths(executable)
	if _, err := os.Stat(entry); err != nil {
		return nil, fmt.Errorf("opencli not ready: bundled package missing: %w", err)
	}
	for _, bun := range candidates {
		if info, err := os.Stat(bun); err != nil || info.IsDir() {
			continue
		}
		// --no-install prevents Bun's implicit package auto-install. CI is the
		// pinned OpenCLI release's supported opt-out from background npm and
		// GitHub version checks; it applies only to this child process.
		cmd := cmdutil.Command(bun, append([]string{"--no-install", entry}, args...)...)
		for _, env := range environment {
			if !strings.HasPrefix(env, "CI=") {
				cmd.Env = append(cmd.Env, env)
			}
		}
		cmd.Env = append(cmd.Env, "CI=1")
		return cmd, nil
	}
	return nil, fmt.Errorf("opencli not ready: bundled bun runtime missing")
}

func main() {
	executable, err := os.Executable()
	if err == nil {
		var cmd *exec.Cmd
		cmd, err = command(executable, os.Args[1:], os.Environ())
		if err == nil {
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			err = run(cmd)
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
