// Package toolserver serves one run's native tools over an authenticated Unix
// socket. The assistant's stdio shim carries MCP messages; tool execution and
// authority remain in the desktop process.
package toolserver

import (
	"bufio"
	"container/list"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"yansu-agent/agent/gate"
	"yansu-agent/agent/native"
	"yansu-agent/agent/tools"
)

type Ticket struct {
	SocketPath string `json:"socket_path"`
	TokenFile  string `json:"token_file"`
}

type Config struct {
	// RunDir is the private parent directory for sockets and ledgers. Empty
	// uses the app's run folder, honoring YANSU_AGENT_HOME.
	RunDir string
	Gate   *gate.Gate
	// OnActionStarted persists the action receipt after the ledger fsync and
	// before dispatch. A failed receipt prevents the action from executing.
	OnActionStarted func(count int) error
}

type Server struct {
	ctx             context.Context
	cancel          context.CancelFunc
	gate            *gate.Gate
	onActionStarted func(int) error
	listener        *net.UnixListener
	dir             string
	ticket          Ticket
	token           string
	ledger          *ledger
	lease           *runLease
	assistantPID    atomic.Int64
	pidReady        chan struct{}
	pidOnce         sync.Once
	unlocked        chan struct{}
	unlockOnce      sync.Once
	closeOnce       sync.Once
	closeErr        error
	acceptDone      chan struct{}
	wg              sync.WaitGroup
	mu              sync.Mutex
	connections     map[*net.UnixConn]bool
	// Calls are serialized, as in the native loop. Concurrent assistants
	// have separate gates, ledgers and session permission state.
	callMu      sync.Mutex
	calls       map[string]completedCall
	results     list.List
	resultBytes int
}

func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.Gate == nil || cfg.Gate.Config == nil || cfg.Gate.Config.Registry == nil {
		return nil, errors.New("tool server requires a run gate")
	}
	for _, tool := range cfg.Gate.Tools() {
		if !ValidToolName(tool.Name()) {
			return nil, errors.New("tool name cannot be exposed to the assistant")
		}
		if _, err := json.Marshal(tool.JSONSchema()); err != nil {
			return nil, err
		}
	}
	root := cfg.RunDir
	if root == "" {
		root = os.Getenv("YANSU_AGENT_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			root = filepath.Join(home, ".yansu-agent")
		}
		root = filepath.Join(root, "run")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	if err := pruneRunDirectories(root, time.Now(), defaultLedgerRetention); err != nil {
		log.Printf("[toolserver] ledger retention: %v", err)
	}
	dir, err := os.MkdirTemp(root, "assistant-")
	if err != nil {
		return nil, err
	}
	success := false
	var lease *runLease
	defer func() {
		if !success {
			_ = lease.close()
			discardRunDirectory(dir)
		}
	}()
	lease, err = lockRunDirectory(dir)
	if err != nil {
		return nil, err
	}
	ticket := Ticket{SocketPath: filepath.Join(dir, "tools.sock"), TokenFile: filepath.Join(dir, "token")}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(random)
	if err := os.WriteFile(ticket.TokenFile, []byte(token), 0600); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(ticket)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "ticket.json"), raw, 0600); err != nil {
		return nil, err
	}
	log, err := newLedger(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: ticket.SocketPath, Net: "unix"})
	if err != nil {
		_ = log.close()
		return nil, err
	}
	if err := os.Chmod(ticket.SocketPath, 0600); err != nil {
		_ = listener.Close()
		_ = log.close()
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	s := &Server{ctx: runCtx, cancel: cancel, gate: cfg.Gate, onActionStarted: cfg.OnActionStarted, listener: listener, dir: dir, ticket: ticket, token: token, ledger: log, lease: lease, pidReady: make(chan struct{}), unlocked: make(chan struct{}), acceptDone: make(chan struct{}), connections: map[*net.UnixConn]bool{}, calls: map[string]completedCall{}}
	go s.accept()
	go func() { <-runCtx.Done(); _ = s.Close() }()
	success = true
	return s, nil
}

func (s *Server) TicketPath() string { return filepath.Join(s.dir, "ticket.json") }
func (s *Server) LedgerPath() string { return s.ledger.path }
func (s *Server) ToolNames() []string {
	var names []string
	for _, tool := range s.gate.Tools() {
		names = append(names, tool.Name())
	}
	sort.Strings(names)
	return names
}
func (s *Server) SetAssistantPID(pid int) {
	if pid <= 0 {
		return
	}
	s.pidOnce.Do(func() { s.assistantPID.Store(int64(pid)); close(s.pidReady) })
}
func (s *Server) Unlock()                      { s.unlockOnce.Do(func() { close(s.unlocked) }) }
func (s *Server) Snapshot() (int, bool, error) { return s.ledger.snapshot() }

func (s *Server) accept() {
	defer close(s.acceptDone)
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.connections[conn] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			defer func() { s.mu.Lock(); delete(s.connections, conn); s.mu.Unlock() }()
			s.serve(conn)
		}()
	}
}

func (s *Server) serve(conn *net.UnixConn) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > 1024 {
		return
	}
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(line, &auth) != nil || subtle.ConstantTimeCompare([]byte(auth.Token), []byte(s.token)) != 1 {
		return
	}
	select {
	case <-s.pidReady:
	case <-s.ctx.Done():
		return
	}
	if err := verifyPeer(conn, int(s.assistantPID.Load())); err != nil {
		return
	}
	if _, err := conn.Write([]byte("{\"ok\":true}\n")); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	protocol := mcpserver.NewMCPServer("Isoform run tools", "1.0.0", mcpserver.WithToolCapabilities(false))
	for _, tool := range s.gate.Tools() {
		name := tool.Name()
		schema, err := json.Marshal(tool.JSONSchema())
		if err != nil {
			return
		}
		protocol.AddTool(mcp.NewToolWithRawSchema(name, tool.Description(), schema), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.call(ctx, name, request.GetArguments()), nil
		})
	}
	// The SDK's StdioServer has a package-global session in v0.48. Use its
	// protocol handler with an isolated session and bounded newline framing.
	session := &socketSession{id: uuid.NewString(), notifications: make(chan mcp.JSONRPCNotification, 16)}
	ctx := protocol.WithContext(s.ctx, session)
	if err := protocol.RegisterSession(ctx, session); err != nil {
		return
	}
	defer protocol.UnregisterSession(ctx, session.SessionID())
	defer s.releaseSessionResults(session.SessionID())
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		if s.ctx.Err() != nil {
			return
		}
		message := json.RawMessage(scanner.Bytes())
		response := protocol.HandleMessage(withRequestID(ctx, message, session.SessionID()), message)
		if response != nil {
			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	}
}

func (s *Server) call(ctx context.Context, name string, input map[string]any) *mcp.CallToolResult {
	select {
	case <-s.unlocked:
	case <-ctx.Done():
		return mcp.NewToolResultError(ctx.Err().Error())
	}
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	if err := s.ctx.Err(); err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	id, ok := s.toolCallID(ctx)
	if !ok {
		return mcp.NewToolResultError("tool call requires a session and a string or integer request ID")
	}
	argumentHash := hash(input)
	if previous, ok := s.calls[id]; ok {
		if previous.name != name || previous.argumentHash != argumentHash {
			return mcp.NewToolResultError("tool call ID was reused with different arguments or a different tool")
		}
		if tool := s.gate.Config.Registry.Get(name); tool != nil {
			if fresh, ok := tool.(interface{ RequiresFreshRead() bool }); ok && fresh.RequiresFreshRead() {
				return mcp.NewToolResultError("This source read requires a fresh call ID; cached personal data cannot be replayed.")
			}
		}
		return previous.replay()
	}
	if len(s.calls) >= maxReplayCalls {
		return mcp.NewToolResultError("tool call ID limit reached for this run; new calls are refused")
	}
	// Reserve the ID before any event or dispatch. Even if execution cannot
	// produce a completed result, this ID must never run a second time.
	s.calls[id] = completedCall{name: name, argumentHash: argumentHash, outcome: callPending}
	started := false
	// The shared native gate emits tool_result; the transport emits tool_use
	// before entering it, matching the native loop's event sequence.
	if s.gate.Config.Emit != nil {
		emitUse(s.gate.Config, name, id, input)
	}
	result, err := s.gate.Execute(ctx, gate.ToolPrefix+name, id, input, func(name string, call tools.ToolCall) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The attempt timer and this authorized action must choose a winner
		// before the durable ledger claims that any action has started.
		if err := admitAction(ctx, func() error {
			if started {
				return errors.New("action admission repeated its ledger start")
			}
			if err := s.ledger.start(name, call); err != nil {
				return err
			}
			started = true
			return nil
		}); err != nil {
			return err
		}
		if !started {
			return errors.New("action admission did not record the action")
		}
		if s.onActionStarted != nil {
			count, _, err := s.ledger.snapshot()
			if err != nil {
				return err
			}
			if err := s.onActionStarted(count); err != nil {
				return err
			}
		}
		return nil
	})
	sourceReader, sourceRead := s.gate.Config.Registry.Get(name).(tools.SourceReadTool)
	releasePublication := func() {}
	if sourceRead && !result.IsError && err == nil {
		release, publicationErr := sourceReader.LockResultPublication(ctx, tools.ToolCall{ID: id, Input: input})
		if publicationErr != nil {
			result = tools.ToolResult{IsError: true, Content: "Isoform source context changed before publication; retry with a new call ID."}
		} else {
			releasePublication = release
		}
	}
	defer func() { releasePublication() }()

	if err != nil {
		result.IsError = true
		result.Content = err.Error()
	}
	if started {
		if err := s.ledger.finish(id, result); err != nil {
			result.IsError = true
			result.Content = "could not finish action ledger: " + err.Error()
		}
	}
	out := resultToMCP(name, result)
	identity := ctx.Value(requestIDKey{}).(requestIdentity)
	s.completeCall(id, identity.sessionID, out)
	// Construct the immutable transport result before releasing source fences.
	// UI persistence may write the same source DB; never emit under its fence.
	releasePublication()
	releasePublication = func() {}
	if sourceRead && s.gate.Config.Emit != nil {
		s.gate.Config.Emit(native.LoopEvent{Type: "tool_result", ToolName: name, ToolUseID: id, Content: result.Content, ToolSucceeded: !result.IsError, Failed: result.IsError})
	}
	return out
}

func resultToMCP(name string, result tools.ToolResult) *mcp.CallToolResult {
	out := &mcp.CallToolResult{IsError: result.IsError}
	if name == "cua_driver" && !result.IsError {
		text, images := cuaImageContent(result.Content)
		result.Content = text
		out.Content = append(out.Content, images...)
	}
	if result.Content != "" {
		out.Content = append(out.Content, mcp.NewTextContent(result.Content))
	}
	for _, block := range result.Blocks {
		switch block.Type {
		case "text":
			out.Content = append(out.Content, mcp.NewTextContent(block.Text))
		case "image":
			if block.Source != nil {
				out.Content = append(out.Content, mcp.NewImageContent(block.Source.Data, block.Source.MediaType))
			}
		}
	}
	if len(out.Content) == 0 {
		out.Content = append(out.Content, mcp.NewTextContent(""))
	}
	return out
}

// Close revokes access and waits for all accepted calls to finish before a
// fallback can inspect the ledger. The ledger and its retention lease file
// remain on disk for the inspection window.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.listener.Close()
		<-s.acceptDone
		s.mu.Lock()
		for conn := range s.connections {
			_ = conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
		s.callMu.Lock()
		s.calls = nil
		s.results.Init()
		s.resultBytes = 0
		s.callMu.Unlock()
		s.closeErr = s.ledger.close()
		for _, path := range []string{s.ticket.SocketPath, s.ticket.TokenFile, s.TicketPath(), filepath.Join(s.dir, "mcp.json"), filepath.Join(s.dir, "claude-mcp.json")} {
			_ = os.Remove(path)
		}
		if err := s.lease.close(); err != nil {
			// Retention bookkeeping must not change a completed run's outcome.
			log.Printf("[toolserver] release ledger retention lease: %v", err)
		}
	})
	return s.closeErr
}

type socketSession struct {
	id            string
	initialized   atomic.Bool
	notifications chan mcp.JSONRPCNotification
}

func (s *socketSession) Initialize()                                         { s.initialized.Store(true) }
func (s *socketSession) Initialized() bool                                   { return s.initialized.Load() }
func (s *socketSession) SessionID() string                                   { return s.id }
func (s *socketSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.notifications }

// ValidToolName enforces the assistant namespace's remaining name budget.
// Tools that cannot be exposed losslessly keep their task on the native path.
func ValidToolName(name string) bool {
	if name == "" || len(gate.ToolPrefix)+len(name) > 128 {
		return false
	}
	for _, c := range name {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_", c) {
			return false
		}
	}
	return true
}
