package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/internal/config"
)

type analyzeRouteAdapter struct {
	done    chan struct{}
	replies chan string
}

func (*analyzeRouteAdapter) Listen(context.Context, func(Event)) error { return nil }
func (a *analyzeRouteAdapter) Reply(_ Event, message string) {
	if a.replies != nil {
		a.replies <- message
	}
}
func (a *analyzeRouteAdapter) PostAndStream(_ Event, output <-chan string) {
	for range output {
	}
	close(a.done)
}

func daemonAnalyzeFixture(t *testing.T) (string, string) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "iso-da-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", "")
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	return home, project
}

// The stand-in executable records the actual route argv without invoking an
// external model or requiring any personal CLI configuration or credentials.
func captureDaemonRouteArgs(t *testing.T, analyze bool) ([]string, string, string) {
	t.Helper()
	home, project := daemonAnalyzeFixture(t)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(home, "args")
	promptCapture := filepath.Join(home, "prompt")
	t.Setenv("YANSU_TEST_CAPTURE_ARGS", capture)
	t.Setenv("YANSU_TEST_CAPTURE_PROMPT", promptCapture)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin:/usr/sbin:/sbin")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$YANSU_TEST_CAPTURE_ARGS\"\n/bin/cat > \"$YANSU_TEST_CAPTURE_PROMPT\"\nprintf 'route complete\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	commit := createAnalyzeGitFixture(t, home, project)
	adapter := &analyzeRouteAdapter{done: make(chan struct{}), replies: make(chan string, 1)}
	sessions := NewSessionManager()
	t.Cleanup(sessions.Close)
	router := &Router{cfg: &config.Config{}, daemonCfg: &config.DaemonConfig{}, sessions: sessions, adapter: adapter}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if analyze {
		router.runAnalyze(ctx, Event{UserID: "owner", Platform: "test"}, project)
	} else {
		router.runAITask(ctx, Event{UserID: "owner", Platform: "test"}, project, "test relay", true)
	}
	select {
	case <-adapter.done:
	case message := <-adapter.replies:
		t.Fatalf("route refused: %s", message)
	case <-ctx.Done():
		t.Fatal("route did not finish")
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(promptCapture)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), string(prompt), commit
}

func createAnalyzeGitFixture(t *testing.T, home, project string) string {
	t.Helper()
	requireTrustedAnalyzeGit(t)
	if err := os.WriteFile(filepath.Join(project, "evidence.txt"), []byte("ANALYZE_FILE_EVIDENCE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output []byte
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "evidence.txt"}, {"commit", "-qm", "analyze evidence commit sentinel"}, {"rev-parse", "HEAD"}} {
		prefix := []string{"-c", "user.name=Isoform Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}
		cmd := exec.Command("/usr/bin/git", append(prefix, args...)...)
		cmd.Dir = project
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		var err error
		output, err = cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v: %s", err, output)
		}
	}
	return strings.TrimSpace(string(output))
}

func daemonArgValue(args []string, flag string) string {
	for i, value := range args {
		if value == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "<missing>"
}

func TestAnalyzeRouteUsesGuardedProjectFileTools(t *testing.T) {
	args, prompt, commit := captureDaemonRouteArgs(t, true)
	if got := daemonArgValue(args, "--tools"); got != "" {
		t.Errorf("analyze must disable all unguarded built-ins; --tools=%q", got)
	}
	want := "mcp__isoform_analyze__Read,mcp__isoform_analyze__Glob,mcp__isoform_analyze__Grep"
	if got := daemonArgValue(args, "--allowedTools"); got != want {
		t.Errorf("analyze requires exactly guarded project readers: got %q, want %q", got, want)
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(daemonArgValue(args, "--mcp-config")), &cfg); err != nil {
		t.Fatal(err)
	}
	bridge := cfg.Servers["isoform_analyze"]
	if len(cfg.Servers) != 1 || bridge.Type != "http" || !strings.HasPrefix(bridge.URL, "http://127.0.0.1:") || !strings.HasPrefix(bridge.Headers["Authorization"], "Bearer ") {
		t.Errorf("analyze lacks one authenticated loopback tool bridge: %+v", cfg)
	}
	for _, evidence := range []string{commit, "analyze evidence commit sentinel", "ANALYZE_FILE_EVIDENCE", "untrusted JSON"} {
		if !strings.Contains(prompt, evidence) {
			t.Errorf("analyze prompt lacks real commit evidence %q: %s", evidence, prompt)
		}
	}
	for _, arg := range args {
		if arg == "--safe-mode" {
			t.Error("safe mode would discard the explicit guarded MCP bridge")
		}
	}
}

func TestRelayRouteRemainsWebSearchOnly(t *testing.T) {
	args, _, _ := captureDaemonRouteArgs(t, false)
	for _, flag := range []string{"--tools", "--allowedTools"} {
		if got := daemonArgValue(args, flag); got != "WebSearch" {
			t.Errorf("relay %s=%q", flag, got)
		}
	}
	if got := daemonArgValue(args, "--mcp-config"); got != `{"mcpServers":{}}` {
		t.Errorf("relay inherited analyze file access: %q", got)
	}
}
