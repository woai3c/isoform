package toolserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"yansu-agent/agent/authority"
	"yansu-agent/agent/gate"
	"yansu-agent/agent/native"
	"yansu-agent/agent/tools"
	"yansu-agent/agent/tools/guard"
)

// The MCP client's actual subprocess invokes the same relay function as the
// hidden CLI command. No tools, transport or policy implementations are mocked.
func TestToolShimProcess(t *testing.T) {
	path := os.Getenv("YANSU_TEST_TOOL_TICKET")
	if path == "" {
		return
	}
	if err := Relay(context.Background(), path, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func newTestServer(t *testing.T, grants []authority.Grant) (*Server, string) {
	t.Helper()
	root := retentionRoot(t)
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool())
	registry.Register(tools.NewWriteTool(guard.NewFileLockManager(0)))
	for i := range grants {
		if grants[i].ResourcePrefix == "work" {
			grants[i].ResourcePrefix = work
		}
	}
	cfg := &native.LoopConfig{Registry: registry, WorkDir: work, RunID: "run", SessionID: "run", AutoApproveTools: true, AuthorityEvaluator: authority.NewPolicy(grants), Emit: func(native.LoopEvent) {}}
	s, err := New(context.Background(), Config{RunDir: root, Gate: &gate.Gate{Config: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.SetAssistantPID(os.Getpid())
	s.Unlock()
	return s, work
}

func connectTestClient(t *testing.T, s *Server) *client.Client {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewStdioMCPClient(executable, []string{"YANSU_TEST_TOOL_TICKET=" + s.TicketPath()}, "-test.run=^TestToolShimProcess$")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "tool-server-test", Version: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func callTool(t *testing.T, c *client.Client, name string, input map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: input}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRealToolServerGrantsAndDurableLedger(t *testing.T) {
	for _, tc := range []struct {
		name    string
		grants  []authority.Grant
		allowed bool
	}{
		{"no grant", nil, false},
		{"scoped grant", []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}}, true},
		{"expired grant", []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work", ExpiresAt: time.Now().Add(-time.Hour)}}, false},
		{"wrong capability", []authority.Grant{{ID: "read", Capability: "tool.read", ResourcePrefix: "work"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, work := newTestServer(t, tc.grants)
			c := connectTestClient(t, s)
			list, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
			if err != nil || len(list.Tools) != 2 {
				t.Fatalf("tools=%+v err=%v", list, err)
			}
			path := filepath.Join(work, "artifact.txt")
			result := callTool(t, c, "Write", map[string]any{"path": path, "content": "granted output"})
			if result.IsError == tc.allowed {
				t.Fatalf("allowed=%v result=%+v", tc.allowed, result)
			}
			_, err = os.Stat(path)
			if (err == nil) != tc.allowed {
				t.Fatalf("artifact exists=%v, allowed=%v", err == nil, tc.allowed)
			}
			_ = c.Close()
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			count, unfinished, err := s.Snapshot()
			want := 0
			if tc.allowed {
				want = 1
			}
			if err != nil || unfinished || count != want {
				t.Fatalf("ledger count=%d unfinished=%v err=%v", count, unfinished, err)
			}
			if tc.allowed {
				raw, err := os.ReadFile(s.LedgerPath())
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(raw), `"capability":"tool.write"`) || !strings.Contains(string(raw), `"result_hash"`) || strings.Contains(string(raw), "granted output") {
					t.Fatalf("ledger must hold metadata and hashes, got %s", raw)
				}
			}
			if _, err := os.Stat(s.TicketPath()); !os.IsNotExist(err) {
				t.Fatalf("ticket was not revoked: %v", err)
			}
		})
	}
}

func TestGrantCannotEscapeScopeOrProtectedPaths(t *testing.T) {
	s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}, {ID: "read", Capability: "tool.read"}})
	data := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", data)
	if err := os.WriteFile(filepath.Join(data, "secret"), []byte("private marker"), 0600); err != nil {
		t.Fatal(err)
	}
	c := connectTestClient(t, s)
	outside := filepath.Join(filepath.Dir(work), "outside.txt")
	if result := callTool(t, c, "Write", map[string]any{"path": outside, "content": "must not write"}); !result.IsError {
		t.Fatalf("out-of-scope write passed: %+v", result)
	}
	if result := callTool(t, c, "Read", map[string]any{"path": filepath.Join(data, "secret")}); !result.IsError {
		t.Fatalf("protected read passed: %+v", result)
	}
	count, unfinished, err := s.Snapshot()
	if err != nil || unfinished || count != 0 {
		t.Fatalf("blocked calls dispatched: %d,%v,%v", count, unfinished, err)
	}
}

func TestLedgerRejectsUnknownAndCorruptOutcomes(t *testing.T) {
	l, err := newLedger(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	if err := l.start("Write", tools.ToolCall{ID: "started-only", Input: map[string]any{"content": "x"}}); err != nil {
		t.Fatal(err)
	}
	count, unfinished, err := l.snapshot()
	if err != nil || count != 1 || !unfinished {
		t.Fatalf("unknown outcome lost: %d,%v,%v", count, unfinished, err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{truncated")
	_ = f.Close()
	if _, _, err := l.snapshot(); err == nil {
		t.Fatal("corrupt ledger must prevent fallback")
	}
}

func TestTicketContainsPathsAndPrivateFiles(t *testing.T) {
	s, _ := newTestServer(t, nil)
	raw, err := os.ReadFile(s.TicketPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), s.token) {
		t.Fatal("ticket embeds token")
	}
	var ticket Ticket
	if err := json.Unmarshal(raw, &ticket); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{s.TicketPath(), ticket.TokenFile, ticket.SocketPath, s.LedgerPath()} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode=%v", path, info.Mode().Perm())
		}
	}
}

func TestConcurrentRunIsolation(t *testing.T) {
	allowed, workAllowed := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
	denied, workDenied := newTestServer(t, nil)
	allowedClient := connectTestClient(t, allowed)
	deniedClient := connectTestClient(t, denied)
	if result := callTool(t, allowedClient, "Write", map[string]any{"path": filepath.Join(workAllowed, "yes"), "content": "allowed"}); result.IsError {
		t.Fatalf("allowed run failed: %+v", result)
	}
	if result := callTool(t, deniedClient, "Write", map[string]any{"path": filepath.Join(workDenied, "no"), "content": "denied"}); !result.IsError {
		t.Fatalf("grant leaked to other run: %+v", result)
	}
	if result := callTool(t, allowedClient, "Write", map[string]any{"path": filepath.Join(workDenied, "cross"), "content": "denied"}); !result.IsError {
		t.Fatalf("scope leaked to other run: %+v", result)
	}
	count, unfinished, err := allowed.Snapshot()
	if err != nil || count != 1 || unfinished {
		t.Fatalf("allowed ledger: %d %v %v", count, unfinished, err)
	}
	count, unfinished, err = denied.Snapshot()
	if err != nil || count != 0 || unfinished {
		t.Fatalf("denied ledger: %d %v %v", count, unfinished, err)
	}
}

func TestReceiptIsDurableBeforeActionAndCloseWaits(t *testing.T) {
	s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
	path := filepath.Join(work, "artifact")
	called := false
	s.onActionStarted = func(count int) error {
		called = true
		if count != 1 {
			t.Fatalf("action receipt count=%d", count)
		}
		stored, unfinished, err := s.Snapshot()
		if err != nil || stored != 1 || !unfinished {
			t.Errorf("receipt before fsync: %d %v %v", stored, unfinished, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("action ran before receipt: %v", err)
		}
		return nil
	}
	c := connectTestClient(t, s)
	result := callTool(t, c, "Write", map[string]any{"path": path, "content": "artifact"})
	if result.IsError || !called {
		t.Fatalf("receipt action failed: %+v called=%v", result, called)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	count, unfinished, err := s.Snapshot()
	if err != nil || count != 1 || unfinished {
		t.Fatalf("close left unknown action: %d %v %v", count, unfinished, err)
	}
}
