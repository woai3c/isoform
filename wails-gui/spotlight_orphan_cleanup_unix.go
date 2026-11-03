//go:build !windows

package main

import (
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// cleanupOrphanedSpotlightHelpers removes helpers detached by a hard Wails
// restart. A live helper remains parented to Yansu; only exact-binary matches
// already reparented to init are eligible.
func cleanupOrphanedSpotlightHelpers(binaryPath string) {
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath == "" {
		return
	}
	out, err := exec.Command("pgrep", "-f", regexp.QuoteMeta(binaryPath)).Output()
	if err != nil {
		return
	}
	currentPID := os.Getpid()
	killed := 0
	for _, raw := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		commandLine, commandErr := getProcessCmdline(pid)
		parentPID, parentErr := getProcessPPID(pid)
		if commandErr != nil || parentErr != nil ||
			!isOrphanedSpotlightHelper(
				currentPID,
				pid,
				parentPID,
				commandLine,
				binaryPath,
			) {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			continue
		}
		killed++
		log.Printf(
			"[spotlight] removed orphaned helper pid=%d before launch",
			pid,
		)
	}
	if killed > 0 {
		log.Printf("[spotlight] removed %d orphaned helper(s)", killed)
	}
}
