//go:build !windows

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalCancellationKillsAssistantAndChild(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `sh -c 'sleep 0.4; touch "$1"' child "$1" & wait`, "assistant", marker)
	configureExternalProcess(cmd)
	if err := cmd.Run(); err == nil {
		t.Fatal("cancelled assistant succeeded")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("unexpected context error: %v", ctx.Err())
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("assistant child survived cancellation")
	}
}
