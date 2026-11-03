package daemon

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"yansu-agent/internal/config"
)

func analyzeTestClient(t *testing.T, project string) (*analyzeToolBridge, *client.Client) {
	t.Helper()
	bridge, err := startAnalyzeToolBridge(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.close)
	connection, err := client.NewStreamableHttpClient(bridge.endpoint, transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + bridge.token}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.Start(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = connection.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "isoform-analyze-test", Version: "1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return bridge, connection
}

func analyzeTestWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func analyzeTestCall(t *testing.T, connection *client.Client, name string, args map[string]any, wantError bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := connection.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
	if err != nil {
		if wantError {
			return err.Error()
		}
		t.Fatal(err)
	}
	if result.IsError != wantError {
		t.Fatalf("%s error=%v, want %v: %+v", name, result.IsError, wantError, result)
	}
	var output strings.Builder
	for _, block := range result.Content {
		if text, ok := block.(mcp.TextContent); ok {
			output.WriteString(text.Text)
		}
	}
	return output.String()
}

func TestAnalyzeToolBridgeUsesNativeProjectGuard(t *testing.T) {
	home, project := daemonAnalyzeFixture(t)
	analyzeTestWrite(t, filepath.Join(project, "code.txt"), "project-readable")
	outside := filepath.Join(home, "outside.txt")
	analyzeTestWrite(t, outside, "OUTSIDE_PROJECT_SECRET")
	if err := os.Symlink(home, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	_, connection := analyzeTestClient(t, project)
	listed, err := connection.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Glob", "Grep", "Read"}) {
		t.Fatalf("unexpected analyze tools: %v", names)
	}
	if got := analyzeTestCall(t, connection, "Read", map[string]any{"path": filepath.Join(project, "code.txt")}, false); !strings.Contains(got, "project-readable") {
		t.Fatalf("project file unavailable: %s", got)
	}
	for _, name := range []string{"Read", "Glob", "Grep"} {
		for _, path := range []string{outside, filepath.Join(project, "escape/outside.txt")} {
			output := analyzeTestCall(t, connection, name, map[string]any{"path": path, "pattern": "*"}, true)
			if strings.Contains(output, "OUTSIDE_PROJECT_SECRET") {
				t.Fatal("outside content escaped through guarded bridge")
			}
		}
	}
	for _, name := range []string{"Bash", "Write", "Edit", "WebFetch", "WebSearch"} {
		analyzeTestCall(t, connection, name, map[string]any{"command": "echo unsafe", "path": outside, "content": "unsafe"}, true)
	}
}

func TestAnalyzeToolBridgeFiltersPrivatePathsDuringRecursion(t *testing.T) {
	home, project := daemonAnalyzeFixture(t)
	analyzeTestWrite(t, filepath.Join(project, "code.txt"), "project-readable")
	for _, relative := range []string{".ssh/id_ed25519", ".isoform-agent/cron-secret", ".yansu-agent/activity/private.txt"} {
		analyzeTestWrite(t, filepath.Join(home, relative), "PRIVATE_ANALYZE_SENTINEL")
	}
	_, connection := analyzeTestClient(t, home)
	for _, relative := range []string{".ssh/id_ed25519", ".isoform-agent/cron-secret", ".yansu-agent/activity/private.txt"} {
		analyzeTestCall(t, connection, "Read", map[string]any{"path": filepath.Join(home, relative)}, true)
	}
	glob := analyzeTestCall(t, connection, "Glob", map[string]any{"pattern": "**/*"}, false)
	if !strings.Contains(glob, "code.txt") || strings.Contains(glob, "id_ed25519") || strings.Contains(glob, "cron-secret") || strings.Contains(glob, "private.txt") {
		t.Fatalf("recursive Glob did not retain only safe files: %s", glob)
	}
	t.Run("recursive Grep", func(t *testing.T) {
		if _, err := exec.LookPath("rg"); err != nil {
			t.Skip("recursive Grep requires ripgrep")
		}
		grep := analyzeTestCall(t, connection, "Grep", map[string]any{"pattern": "PRIVATE_ANALYZE_SENTINEL", "output_mode": "content"}, false)
		if strings.Contains(grep, "PRIVATE_ANALYZE_SENTINEL") || strings.Contains(grep, "cron-secret") || strings.Contains(grep, "id_ed25519") {
			t.Fatalf("recursive Grep leaked protected data: %s", grep)
		}
	})
}

func TestAnalyzeToolBridgeRequiresPerRunAuthentication(t *testing.T) {
	_, project := daemonAnalyzeFixture(t)
	bridge, _ := analyzeTestClient(t, project)
	for _, test := range []struct {
		auth, origin string
		status       int
	}{
		{status: http.StatusUnauthorized},
		{auth: "Bearer wrong", status: http.StatusUnauthorized},
		{auth: "Bearer " + bridge.token, origin: "https://example.invalid", status: http.StatusForbidden},
	} {
		request, err := http.NewRequest(http.MethodPost, bridge.endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", test.auth)
		request.Header.Set("Origin", test.origin)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Errorf("auth/origin status=%d, want %d", response.StatusCode, test.status)
		}
	}
}

func TestAnalyzeExecutorKeepsOnlyExplicitMCPTools(t *testing.T) {
	daemonAnalyzeFixture(t)
	t.Setenv("CLAUDE_CODE_SAFE_MODE", "1")
	t.Setenv("CLAUDE_CODE_SIMPLE", "1")
	t.Setenv("CLAUDE_CODE_PLUGIN_DIRS", "/untrusted/plugins")
	for _, stdin := range []bool{false, true} {
		cmd, err := analyzeExecutorCommand(context.Background(), config.DaemonExecutorClaude, "prompt", stdin, `{"mcpServers":{"isoform_analyze":{}}}`)
		if err != nil {
			t.Fatal(err)
		}
		for flag, value := range map[string]string{"--tools": "", "--allowedTools": analyzeMCPTools, "--settings": analyzeSettings, "--permission-mode": "dontAsk"} {
			if got := daemonArgValue(cmd.Args, flag); got != value {
				t.Errorf("stdin=%v %s=%q, want %q", stdin, flag, got, value)
			}
		}
		for _, required := range []string{"--restricted", "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence"} {
			if !strings.Contains(strings.Join(cmd.Args, " "), required) {
				t.Errorf("missing %s", required)
			}
		}
		env := strings.Join(cmd.Env, "\n")
		for _, blocked := range []string{"CLAUDE_CODE_SAFE_MODE=", "CLAUDE_CODE_SIMPLE=", "CLAUDE_CODE_PLUGIN_DIRS="} {
			if strings.Contains(env, blocked) {
				t.Errorf("analyze inherited %s", blocked)
			}
		}
		for _, required := range []string{"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_SKIP_PLUGIN_MCP_SERVERS=1"} {
			if !strings.Contains(env, required) {
				t.Errorf("missing %s", required)
			}
		}
	}
	if cmd, err := analyzeExecutorCommand(context.Background(), config.DaemonExecutorCodex, "prompt", true, ""); cmd != nil || err == nil {
		t.Fatal("Codex analyze must refuse before launch")
	}
}

func TestAnalyzeToolBridgeClosesOnCancellation(t *testing.T) {
	_, project := daemonAnalyzeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	bridge, err := startAnalyzeToolBridge(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.close)
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		response, err := http.Get(bridge.endpoint)
		if err != nil {
			break
		}
		response.Body.Close()
		if time.Now().After(deadline) {
			t.Fatal("cancelled analyze bridge kept its listener open")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := bridge.callTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "Glob", Arguments: map[string]any{"pattern": "**/*"}}})
	if err != nil || !result.IsError {
		t.Fatalf("cancelled bridge dispatched tool: %v %+v", err, result)
	}
}

func TestAnalyzeRefusesWithoutGitEvidence(t *testing.T) {
	_, project := daemonAnalyzeFixture(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	adapter := &analyzeRouteAdapter{replies: make(chan string, 1)}
	sessions := NewSessionManager()
	t.Cleanup(sessions.Close)
	router := &Router{daemonCfg: &config.DaemonConfig{}, sessions: sessions, adapter: adapter}
	router.runAnalyze(context.Background(), Event{}, project)
	select {
	case reply := <-adapter.replies:
		if !strings.Contains(reply, "could not collect recent Git evidence") {
			t.Fatalf("unexpected refusal: %s", reply)
		}
		if len(sessions.GetOrCreate(":").aiTaskSlots) != 0 {
			t.Fatal("failed evidence collection kept its AI task slot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing repository must refuse before model startup")
	}
}
