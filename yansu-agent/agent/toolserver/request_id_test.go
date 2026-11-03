package toolserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"yansu-agent/agent/authority"
	"yansu-agent/agent/native"
)

type requestIDClient struct {
	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
}

// Use the real authenticated socket and MCP dispatch while controlling the
// request ID, which the SDK client normally increments for every call.
func newRequestIDClient(t *testing.T, s *Server) *requestIDClient {
	t.Helper()
	conn, err := net.Dial("unix", s.ticket.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	c := &requestIDClient{conn: conn, encoder: json.NewEncoder(conn), decoder: json.NewDecoder(bufio.NewReader(conn))}
	if err := c.encoder.Encode(map[string]string{"token": s.token}); err != nil {
		t.Fatal(err)
	}
	var auth struct{ OK bool }
	if err := c.decoder.Decode(&auth); err != nil || !auth.OK {
		t.Fatalf("authentication: ok=%v err=%v", auth.OK, err)
	}
	c.request(t, "init", "initialize", map[string]any{
		"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "request-id-test", "version": "1"},
	})
	return c
}

func (c *requestIDClient) request(t *testing.T, id any, method string, params any) json.RawMessage {
	t.Helper()
	if err := c.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := c.encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := c.decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Error) > 0 {
		t.Fatalf("JSON-RPC error: %s", response.Error)
	}
	return response.Result
}

func (c *requestIDClient) call(t *testing.T, id any, name string, args map[string]any, wantError bool) json.RawMessage {
	t.Helper()
	result := c.request(t, id, "tools/call", map[string]any{"name": name, "arguments": args})
	var status struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if status.IsError != wantError {
		t.Fatalf("tool result error=%v want=%v: %s", status.IsError, wantError, result)
	}
	return result
}

func TestToolServerReplaysCallsByRequestID(t *testing.T) {
	s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
	var callMu sync.Mutex
	var calls []native.LoopEvent
	s.gate.Config.Emit = func(event native.LoopEvent) {
		if event.Type == "tool_use" {
			callMu.Lock()
			calls = append(calls, event)
			callMu.Unlock()
		}
	}
	c := newRequestIDClient(t, s)
	path := filepath.Join(work, "artifact")
	args := map[string]any{"path": path, "content": "original"}
	first := c.call(t, "write-1", "Write", args, false)
	if err := os.WriteFile(path, []byte("changed by user"), 0600); err != nil {
		t.Fatal(err)
	}
	if replay := c.call(t, "write-1", "Write", args, false); string(replay) != string(first) {
		t.Fatalf("replay result changed: first=%s replay=%s", first, replay)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "changed by user" {
		t.Fatalf("replay repeated the write: %q %v", content, err)
	}
	c.call(t, "write-1", "Write", map[string]any{"path": path, "content": "changed arguments"}, true)
	c.call(t, "write-1", "Read", args, true)
	if count, unfinished, err := s.Snapshot(); err != nil || unfinished || count != 1 {
		t.Fatalf("replay changed ledger: %d %v %v", count, unfinished, err)
	}
	callMu.Lock()
	if len(calls) != 1 || calls[0].ToolUseID == "" {
		t.Fatalf("replay emitted new call events: %+v", calls)
	}
	callMu.Unlock()
	// Identical arguments are not a deduplication key. Distinct request IDs
	// still execute, and the numeric and string forms of an ID stay distinct.
	for _, id := range []any{"write-2", 1, "1"} {
		c.call(t, id, "Write", args, false)
	}
	if count, unfinished, err := s.Snapshot(); err != nil || unfinished || count != 4 {
		t.Fatalf("distinct calls were conflated: %d %v %v", count, unfinished, err)
	}
	callMu.Lock()
	defer callMu.Unlock()
	seen := make(map[string]bool)
	for _, call := range calls {
		if seen[call.ToolUseID] {
			t.Fatalf("distinct requests shared ToolCall.ID %q", call.ToolUseID)
		}
		seen[call.ToolUseID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("tool-use IDs=%v", seen)
	}
}

func TestToolServerRequestIDsAreIsolatedAcrossSessions(t *testing.T) {
	s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
	var callMu sync.Mutex
	var ids []string
	s.gate.Config.Emit = func(event native.LoopEvent) {
		if event.Type == "tool_use" {
			callMu.Lock()
			ids = append(ids, event.ToolUseID)
			callMu.Unlock()
		}
	}
	path := filepath.Join(work, "artifact")
	args := map[string]any{"path": path, "content": "original"}
	first := newRequestIDClient(t, s)
	first.call(t, 1, "Write", args, false)
	if err := first.conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed by user"), 0600); err != nil {
		t.Fatal(err)
	}
	// A reconnect starts a fresh MCP session. Its restarted request counter
	// cannot identify an earlier model call, even with identical arguments.
	second := newRequestIDClient(t, s)
	second.call(t, 1, "Write", args, false)
	if content, err := os.ReadFile(path); err != nil || string(content) != "original" {
		t.Fatalf("new session lost its distinct write: %q %v", content, err)
	}
	second.call(t, 1, "Write", args, false)
	if count, unfinished, err := s.Snapshot(); err != nil || unfinished || count != 2 {
		t.Fatalf("session IDs were conflated or replayed twice: %d %v %v", count, unfinished, err)
	}
	callMu.Lock()
	defer callMu.Unlock()
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("separate sessions shared ToolCall.ID: %v", ids)
	}
}

func TestToolServerRequestIDsAreIsolatedAcrossRuns(t *testing.T) {
	var callMu sync.Mutex
	var ids []string
	for range 2 {
		s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
		s.gate.Config.Emit = func(event native.LoopEvent) {
			if event.Type == "tool_use" {
				callMu.Lock()
				ids = append(ids, event.ToolUseID)
				callMu.Unlock()
			}
		}
		c := newRequestIDClient(t, s)
		c.call(t, 1, "Write", map[string]any{"path": filepath.Join(work, "artifact"), "content": "once per run"}, false)
		if count, unfinished, err := s.Snapshot(); err != nil || unfinished || count != 1 {
			t.Fatalf("run lost its action: %d %v %v", count, unfinished, err)
		}
	}
	callMu.Lock()
	defer callMu.Unlock()
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("separate runs shared ToolCall.ID: %v", ids)
	}
}

func TestToolServerRejectsMissingOrInvalidRequestIDs(t *testing.T) {
	s, work := newTestServer(t, []authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: "work"}})
	for _, message := range []string{`{}`, `{"id":null}`, `{"id":true}`, `{"id":1.5}`, `{"id":9223372036854775808}`} {
		result := s.call(withRequestID(context.Background(), []byte(message), "test-session"), "Write", map[string]any{"path": filepath.Join(work, "must-not-exist"), "content": "no"})
		if !result.IsError {
			t.Fatalf("invalid ID dispatched: %s", message)
		}
	}
	if result := s.call(withRequestID(context.Background(), []byte(`{"id":1}`), ""), "Write", map[string]any{"path": filepath.Join(work, "must-not-exist"), "content": "no"}); !result.IsError {
		t.Fatal("missing session dispatched")
	}
	if count, unfinished, err := s.Snapshot(); err != nil || unfinished || count != 0 {
		t.Fatalf("invalid request changed ledger: %d %v %v", count, unfinished, err)
	}
}
