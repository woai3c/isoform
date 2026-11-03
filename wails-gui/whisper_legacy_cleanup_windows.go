//go:build windows

package main

import (
	"log"
	"os/exec"
	"syscall"

	"wails-gui/internal/platform/cmdutil"
)

// cleanupLegacyWhisperZombies kills any stray whisper-server.exe processes
// left behind by a crash-on-exit of a pre-sherpa Yansu build. The new
// build doesn't spawn whisper-server at all, so anything matching is by
// definition orphaned. Best-effort: errors are logged and swallowed.
func cleanupLegacyWhisperZombies() {
	cmd := cmdutil.Command("taskkill", "/F", "/IM", "whisper-server.exe", "/T")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		// taskkill exits non-zero when no matching process exists, which is
		// the common (good) case. Only log when we actually killed something
		// or hit a surprising error code.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 128 {
			return // 128 = "process not found" — silent
		}
		log.Printf("[whisper-legacy] taskkill whisper-server.exe: err=%v out=%q", err, string(out))
		return
	}
	log.Printf("[whisper-legacy] terminated stale whisper-server.exe process(es)")
}
