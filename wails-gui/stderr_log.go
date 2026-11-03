package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxStderrLogSize = 10 * 1024 * 1024

// Retain the file through process exit, including panics after main's defers.
var processStderrLog *os.File

// initStderrLog captures Go runtime failures and native diagnostics, which
// bypass log.SetOutput. Call only after acquiring the single-instance lock:
// a second launch must not rotate the running process's diagnostic file.
func initStderrLog() error {
	dir := strings.TrimSpace(os.Getenv(agentHomeEnv))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, configDirName)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return redirectStderrLog(filepath.Join(dir, "yansu-gui.stderr.log"), maxStderrLogSize)
}

func redirectStderrLog(path string, maxSize int64) error {
	// Reuse the normal log's startup rotation and .old/.old.2 retention.
	// Afterwards, writes go straight to the OS file descriptor, without a
	// Go pipe, goroutine or buffer that a runtime fatal error cannot flush.
	// Rotation is deliberately startup-only; one session can exceed maxSize.
	w, err := newRotatingAppLog(path, maxSize)
	if err != nil {
		return fmt.Errorf("open stderr log: %w", err)
	}
	if err := w.file.Chmod(0600); err != nil {
		_ = w.Close()
		return fmt.Errorf("restrict stderr log: %w", err)
	}
	if err := redirectProcessStderr(w.file); err != nil {
		_ = w.Close()
		return fmt.Errorf("redirect process stderr: %w", err)
	}
	processStderrLog = w.file
	return nil
}
