//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/runtoken"
)

func TestBashRunCredentialReachesForegroundAndBackground(t *testing.T) {
	t.Setenv("YANSU_CRON_SECRET", "master-must-not-reach-shell")
	t.Setenv(runtoken.TokenEnv, "ambient-forgery")
	ctx := runtoken.WithCredential(context.Background(), runtoken.Credential{Token: "run-issued", Port: 12345})
	for _, background := range []bool{false, true} {
		dir := t.TempDir()
		tool := NewBashTool()
		defer tool.KillAll()
		result, err := tool.Execute(ctx, ToolCall{WorkDir: dir, Input: map[string]any{
			"command":           `printf '%s\n' "$YANSU_RUN_TOKEN" "$YANSU_RUN_PORT" "$YANSU_CRON_SECRET" > credential.txt`,
			"run_in_background": background,
		}})
		if err != nil || result.IsError {
			t.Fatalf("shell fixture: result=%+v err=%v", result, err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			raw, err := os.ReadFile(filepath.Join(dir, "credential.txt"))
			if err == nil && strings.Count(string(raw), "\n") == 3 {
				if string(raw) != "run-issued\n12345\n\n" {
					t.Fatal("shell received ambient credentials")
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("background shell did not write credential fixture")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
