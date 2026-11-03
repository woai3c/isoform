//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

// cleanupOrphanedBunProcesses finds and kills bun processes left behind by a
// previous app instance (e.g. crash, force-quit, or incomplete shutdown).
// Uses WMIC to list bun.exe processes and checks their command lines for
// known patterns before killing.
func cleanupOrphanedBunProcesses() {
	myPID := os.Getpid()
	killed := 0

	// Patterns to search for: each has a substring that must appear in the
	// command line for the process to be considered an orphan.
	patterns := []string{
		"server.ts",
		"claude-agent-sdk/cli.js",
		"claude-agent-sdk\\cli.js",
	}

	// Query all bun.exe processes with their PID, PPID, and command line.
	out, err := cmdutil.Command("wmic", "process", "where", "name='bun.exe'",
		"get", "ProcessId,ParentProcessId,CommandLine", "/format:csv").Output()
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Node,") {
			continue
		}

		// CSV format: Node,CommandLine,ParentProcessId,ProcessId
		// CommandLine may contain commas, so parse PID/PPID from the right.
		lastComma := strings.LastIndex(line, ",")
		if lastComma < 0 {
			continue
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(line[lastComma+1:]))
		rest := line[:lastComma]
		secondLastComma := strings.LastIndex(rest, ",")
		if secondLastComma < 0 {
			continue
		}
		ppid, _ := strconv.Atoi(strings.TrimSpace(rest[secondLastComma+1:]))
		// Everything between first comma and secondLastComma is the command line.
		firstComma := strings.Index(rest, ",")
		if firstComma < 0 {
			continue
		}
		cmdLine := rest[firstComma+1 : secondLastComma]
		if pid == 0 || pid == myPID {
			continue
		}

		// Skip processes whose parent is us (we just started, they're ours).
		if ppid == myPID {
			continue
		}

		// Check if command line matches any known pattern.
		matched := false
		for _, p := range patterns {
			if strings.Contains(cmdLine, p) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// Kill the process tree.
		killCmd := cmdutil.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
		if err := killCmd.Run(); err == nil {
			killed++
			log.Printf("[bun-cleanup] killed orphaned bun process (pid %d)", pid)
		}
	}

	if killed > 0 {
		log.Printf("[bun-cleanup] cleaned up %d orphaned bun process(es)", killed)
	}
}

// setBunProcessGroup is a no-op on Windows.
func setBunProcessGroup(_ *exec.Cmd) {}

// killBunProcessGroup kills the bun process and its entire child process tree.
// Uses taskkill /T to terminate the process tree, falling back to Process.Kill()
// if taskkill is unavailable.
func killBunProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	err := cmdutil.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
	if err != nil {
		_ = cmd.Process.Kill()
	}
}
