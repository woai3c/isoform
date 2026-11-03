//go:build !windows

package tools

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBashTool_Echo(t *testing.T) {
	tool := NewBashTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "echo hello"},
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Errorf("output missing 'hello': %q", res.Content)
	}
}

func TestBashTool_ExitCode(t *testing.T) {
	tool := NewBashTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "exit 7"},
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if res.Metadata["exit_code"] != 7 {
		t.Errorf("exit_code = %v, want 7", res.Metadata["exit_code"])
	}
}

func TestBashTool_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("timeout test uses unix sleep")
	}
	tool := NewBashTool()
	start := time.Now()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "sleep 5", "timeout": 500.0},
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 7*time.Second {
		t.Errorf("timeout did not fire — elapsed %v", elapsed)
	}
	if res.Metadata["timed_out"] != true {
		t.Errorf("timed_out = %v, want true", res.Metadata["timed_out"])
	}
}

func TestBashTool_HardBlockedCommand(t *testing.T) {
	tool := NewBashTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "rm -rf /"},
	})
	if err == nil {
		t.Fatal("expected hard block to fire")
	}
}

func TestBashTool_BlocksForegroundLongRunningServerCommand(t *testing.T) {
	tool := NewBashTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input: map[string]any{
			"command": "PORT=4321 bun run --preload ./_data-guard.ts server.ts",
		},
	})
	if err == nil {
		t.Fatal("expected foreground server command to be blocked")
	}
	if !strings.Contains(err.Error(), "foreground long-running server command blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLooksLikeLongRunningServerCommand(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		// Long-running invocations — must block.
		{"PORT=4321 bun run server.ts", true},
		{"bun run --hot server.ts", true},
		{"bun run --hot ./src/server.ts", true},
		{"bun run --preload ./_data-guard.ts ./server.js", true},
		{"bun --hot server.ts", true},
		{"bun server.ts", true},
		{"cd app && pnpm run dev", true},
		{"cd app && next dev", true},
		{"vite", true},
		{"vite preview", true},
		{"vite --port 5173", true},
		{"PORT=4321 vite", true},
		{"npx vite", true},
		{"bunx vite dev", true},
		{"npm start", true},
		{"npm run dev", true},
		{"pnpm dev", true},
		{"yarn start", true},
		{"next start", true},
		{"nuxt preview", true},
		{"astro preview", true},
		{"python -m http.server 8000", true},
		{"python3 -m http.server 8000", true},

		// Finite commands — must NOT block (tests/builds/lints).
		{"vite build", false},
		{"vite optimize", false},
		{"next build", false},
		{"next lint", false},
		{"nuxt build", false},
		{"astro build", false},
		{"astro check", false},
		{"npm test", false},
		{"npm run build", false},
		{"npm run lint", false},
		{"pnpm build", false},
		{"yarn build", false},
		{"bun test", false},
		{"bun run build", false},
		{"bun run server-utils.ts", false},

		// Quoted/argument text — must NOT block.
		{`grep "vite preview" README.md`, false},
		{`echo 'see vite preview docs'`, false},
		{"echo vite", false},
		{`grep -r "next dev" .`, false},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			got := looksLikeLongRunningServerCommand(tc.command)
			if got != tc.want {
				t.Fatalf("looksLikeLongRunningServerCommand(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestBashTool_BlocksFork(t *testing.T) {
	tool := NewBashTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": ":(){ :|:& };:"},
	})
	if err == nil {
		t.Fatal("expected fork-bomb block")
	}
}

func TestBashTool_WorkDirRespected(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: dir,
		Input:   map[string]any{"command": "pwd"},
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(res.Content))
	if want != got {
		// macOS tmp is a symlink to /private/tmp; compare with evalsymlinks
		// and also accept substring match.
		if !strings.Contains(res.Content, dir) {
			t.Errorf("pwd = %q, want %q", res.Content, dir)
		}
	}
}

func TestBashTool_EnvScrubbed(t *testing.T) {
	t.Setenv("SUPER_SECRET_TOKEN", "abcd1234")
	tool := NewBashTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "env"},
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if strings.Contains(res.Content, "SUPER_SECRET_TOKEN") {
		t.Errorf("secret env leaked: %q", res.Content)
	}
}

func TestBashTool_OutputTruncation(t *testing.T) {
	tool := NewBashTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "yes line | head -n 5000"},
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if res.Metadata["truncated"] != true {
		t.Errorf("expected truncation metadata")
	}
	if !strings.Contains(res.Content, "lines truncated") {
		t.Errorf("expected truncation marker in output")
	}
}

func TestBashTool_BackgroundLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bg test uses sh")
	}
	tool := NewBashTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "echo first; sleep 1; echo second", "run_in_background": true},
	})
	if err != nil {
		t.Fatalf("bash bg: %v", err)
	}
	shellID, _ := res.Metadata["shell_id"].(string)
	if shellID == "" {
		t.Fatal("missing shell_id")
	}
	time.Sleep(2 * time.Second)
	status, err := tool.Output(shellID)
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if !strings.Contains(status.Output, "first") || !strings.Contains(status.Output, "second") {
		t.Errorf("missing background output: %q", status.Output)
	}
	if status.Status != "completed" {
		t.Errorf("status = %s, want completed", status.Status)
	}
}

func TestBashTool_BackgroundKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	tool := NewBashTool()
	res, err := tool.Execute(context.Background(), ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "sleep 60", "run_in_background": true},
	})
	if err != nil {
		t.Fatalf("bash bg: %v", err)
	}
	shellID, _ := res.Metadata["shell_id"].(string)
	if err := tool.Kill(shellID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	status, err := tool.Output(shellID)
	if err != nil {
		t.Fatalf("output after kill: %v", err)
	}
	if status.Status == "running" {
		t.Errorf("expected shell to stop, still running")
	}
}

func TestBashTool_BackgroundOutlivesCallContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	tool := NewBashTool()
	ctx, cancel := context.WithCancel(context.Background())
	res, err := tool.Execute(ctx, ToolCall{
		WorkDir: t.TempDir(),
		Input:   map[string]any{"command": "sleep 60", "run_in_background": true},
	})
	if err != nil {
		t.Fatalf("bash bg: %v", err)
	}
	shellID, _ := res.Metadata["shell_id"].(string)
	cancel()
	time.Sleep(100 * time.Millisecond)

	status, err := tool.Output(shellID)
	if err != nil {
		t.Fatalf("output after call context cancellation: %v", err)
	}
	if status.Status != "running" {
		t.Fatalf("status = %s, want running", status.Status)
	}

	tool.KillAll()
}

func TestRingBuffer_Basic(t *testing.T) {
	rb := newRingBuffer(32)
	rb.Write([]byte("hello"))
	data, dropped := rb.ReadFrom(0)
	if dropped {
		t.Error("unexpected drop flag")
	}
	if string(data) != "hello" {
		t.Errorf("read = %q", string(data))
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := newRingBuffer(8)
	rb.Write([]byte("0123456789ABCDEF"))
	data, dropped := rb.ReadFrom(0)
	if !dropped {
		t.Error("expected dropped flag on overflow")
	}
	if string(data) != "89ABCDEF" {
		t.Errorf("read = %q, want 89ABCDEF", string(data))
	}
}

func TestRingBuffer_Cursor(t *testing.T) {
	rb := newRingBuffer(64)
	rb.Write([]byte("aaa"))
	d1, _ := rb.ReadFrom(0)
	rb.Write([]byte("bbb"))
	d2, _ := rb.ReadFrom(len(d1))
	if string(d1) != "aaa" || string(d2) != "bbb" {
		t.Errorf("cursor reads: %q / %q", d1, d2)
	}
}
