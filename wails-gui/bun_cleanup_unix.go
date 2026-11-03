//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// cleanupOrphanedBunProcesses finds and kills bun processes running "server.ts"
// that were left behind by a previous app instance (e.g. after a crash or
// force-quit). Called during startup before restoring sessions.
func cleanupOrphanedBunProcesses() {
	myPID := os.Getpid()
	killed := 0

	// Patterns to search for orphaned bun processes.
	// Each entry is a pgrep pattern and a verification substring.
	patterns := []struct {
		pgrepPattern string
		verifyStr    string
	}{
		{"bun.*server\\.ts", "server.ts"},
	}

	for _, p := range patterns {
		out, err := exec.Command("pgrep", "-f", p.pgrepPattern).Output()
		if err != nil {
			// pgrep exits 1 when no processes match — not an error.
			continue
		}

		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			pid, err := strconv.Atoi(line)
			if err != nil {
				continue
			}

			// Never kill ourselves.
			if pid == myPID {
				continue
			}

			// Verify the process command line before killing.
			cmdline, err := getProcessCmdline(pid)
			if err != nil {
				continue
			}
			if !strings.Contains(cmdline, p.verifyStr) {
				continue
			}

			// Skip processes whose parent is the current app (they are ours).
			if ppid, err := getProcessPPID(pid); err == nil && ppid == myPID {
				continue
			}

			// Try killing the process group first (negative PID kills the group).
			// Fall back to killing just the process if group kill fails.
			if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
			killed++
			log.Printf("[bun-cleanup] killed orphaned bun process (pid %d): %s", pid, cmdline)
		}
	}

	if killed > 0 {
		log.Printf("[bun-cleanup] cleaned up %d orphaned bun process(es)", killed)
	}
}

// getProcessCmdline returns the command line of a process by PID.
func getProcessCmdline(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// getProcessPPID returns the parent PID of a process.
func getProcessPPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "ppid=").Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// setBunProcessGroup sets SysProcAttr to create a new process group for the
// bun command. This ensures that when we kill the bun process, all its child
// processes (e.g. spawned by server.ts) are also killed via process group signal.
func setBunProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killBunProcessGroup kills the entire process group rooted at the given process.
// Falls back to killing just the process if group kill fails.
func killBunProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// Negative PID sends signal to the entire process group.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
