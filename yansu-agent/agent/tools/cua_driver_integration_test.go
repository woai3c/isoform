package tools

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// cuaDriverBinaryFromEnv lets a developer point the integration test at
// a specific cua-driver build (the one wails-gui staged via
// scripts/download-cua-driver.sh, for example). Skipped unless set.
const cuaDriverIntegrationEnv = "YANSU_CUA_INTEGRATION_BINARY"

// TestCuaDriverTool_Integration spawns a real cua-driver daemon at a
// throwaway socket path, monkey-patches the tool to point at that
// socket, and exercises one read-only call (`check_permissions`) plus
// the unknown-tool error path. Skipped unless YANSU_CUA_INTEGRATION_BINARY
// is set — CI hosts without macOS Apple Silicon can't run it.
func TestCuaDriverTool_Integration(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cua-driver is macOS-only")
	}
	bin := os.Getenv(cuaDriverIntegrationEnv)
	if bin == "" {
		t.Skipf("set %s to the cua-driver binary path to run", cuaDriverIntegrationEnv)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("cua-driver binary not found: %v", err)
	}

	tmpdir := t.TempDir()
	sock := filepath.Join(tmpdir, "cua.sock")

	cmd := exec.Command(bin, "serve", "--no-relaunch", "--socket", sock)
	cmd.Stderr = os.Stderr
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn daemon: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	// Wait for socket to bind.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", sock, 200*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	tool := &CuaDriverTool{socketPath: sock}

	t.Run("check_permissions returns text content", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := tool.Execute(ctx, ToolCall{Authority: &AuthorityRuntime{},
			Input: map[string]any{
				"name": "check_permissions",
				"args": map[string]any{"prompt": false},
			},
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Content == "" {
			t.Fatal("expected non-empty content")
		}
		// The unwrap path should give us plain text, not a JSON envelope.
		if res.Content[0] == '{' {
			t.Errorf("expected unwrapped text, got JSON envelope: %.100s", res.Content)
		}
	})

	t.Run("unknown tool surfaces upstream error verbatim", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := tool.Execute(ctx, ToolCall{Authority: &AuthorityRuntime{},
			Input: map[string]any{
				"name": "no_such_tool_definitely",
			},
		})
		if err == nil {
			t.Fatal("expected error for unknown tool")
		}
		// We surface "cua-driver: <upstream message>" — the upstream
		// message is "Unknown tool: …".
		if msg := err.Error(); !contains(msg, "Unknown tool") {
			t.Errorf("expected upstream error to mention 'Unknown tool', got: %s", msg)
		}
	})

	t.Run("missing name field rejected", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := tool.Execute(ctx, ToolCall{Authority: &AuthorityRuntime{}, Input: map[string]any{}})
		if err == nil {
			t.Fatal("expected error for missing name")
		}
		if msg := err.Error(); !contains(msg, "name") {
			t.Errorf("error should mention 'name': %s", msg)
		}
	})

	t.Run("multi-block payload falls back to raw JSON", func(t *testing.T) {
		// Exercise the unwrap fallback branch directly — list_apps
		// returns a single text block so we can't trigger the fallback
		// from a real call, but we can spot-check the helper.
		fake := json.RawMessage(`{"kind":"call","payload":{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}}`)
		got, ok := unwrapCuaTextPayload(fake)
		if ok {
			t.Errorf("multi-block should not unwrap; got=%q", got)
		}
	})
}

// TestUnwrapCuaTextPayload_Cases is a unit test that doesn't need a
// running daemon — keeps the unwrap logic regression-tested even on CI
// hosts that skip the integration test.
func TestUnwrapCuaTextPayload_Cases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			name: "single text block unwraps",
			in:   `{"kind":"call","payload":{"content":[{"type":"text","text":"hello"}]}}`,
			want: "hello",
			ok:   true,
		},
		{
			name: "image block falls through",
			in:   `{"kind":"call","payload":{"content":[{"type":"image","text":""}]}}`,
			want: "",
			ok:   false,
		},
		{
			name: "empty raw falls through",
			in:   ``,
			want: "",
			ok:   false,
		},
		{
			name: "malformed JSON falls through",
			in:   `not json`,
			want: "",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := unwrapCuaTextPayload(json.RawMessage(tc.in))
			if got != tc.want || ok != tc.ok {
				t.Errorf("got=%q,%v want=%q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func contains(s, substr string) bool { return len(s) >= len(substr) && indexOf(s, substr) >= 0 }
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
