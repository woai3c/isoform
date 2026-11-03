package terminal

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Info is returned to the frontend when a terminal is created.
type Info struct {
	ID     string `json:"id"`
	WSPort int    `json:"wsPort"`
}

// Manager manages all active terminals and the shared WebSocket server.
type Manager struct {
	mu        sync.Mutex
	terminals map[string]*terminalSession
	wsPort    int
	wsServer  *http.Server
	upgrader  websocket.Upgrader
	emitExit  func(ctx context.Context, terminalID string)
}

// ThemeColors carries foreground/background hex colors for terminal query responses.
type ThemeColors struct {
	Foreground string `json:"foreground"` // e.g. "#c0caf5"
	Background string `json:"background"` // e.g. "#0f0f14"
}

// RemoteEnv describes a non-local execution environment for the terminal.
// Nil means local (default behavior).
type RemoteEnv struct {
	Type        string `json:"type"`                  // "wsl" or "ssh"
	Distro      string `json:"distro,omitempty"`      // WSL distribution name (empty = default)
	SSHConfigID string `json:"sshConfigId,omitempty"` // SSH connection config ID
}

type terminalSession struct {
	id        string
	pty       gopty.Pty
	cmd       *gopty.Cmd
	resp      *Responder
	done      chan struct{}
	closeOnce sync.Once
}

// NewManager constructs a terminal manager.
func NewManager(emitExit func(ctx context.Context, terminalID string)) *Manager {
	return &Manager{
		terminals: make(map[string]*terminalSession),
		upgrader: websocket.Upgrader{
			CheckOrigin: AllowWSRequest,
		},
		emitExit: emitExit,
	}
}

// IsLoopbackRemoteAddr returns true when remoteAddr belongs to loopback.
func IsLoopbackRemoteAddr(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if host == "" {
		return false
	}

	if parsedHost, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = parsedHost
	}

	host = strings.Trim(host, "[]")
	if idx := strings.Index(host, "%"); idx >= 0 {
		host = host[:idx]
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}

	return strings.EqualFold(host, "localhost")
}

// IsAllowedTerminalOrigin validates websocket origin.
func IsAllowedTerminalOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		// Desktop webviews may omit the Origin header.
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}

	switch parsed.Scheme {
	case "http", "https", "ws", "wss", "wails":
	default:
		return false
	}

	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1", "wails.localhost", "wails":
		return true
	default:
		return false
	}
}

// AllowWSRequest validates terminal websocket requests by remote address + origin.
func AllowWSRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !IsLoopbackRemoteAddr(r.RemoteAddr) {
		return false
	}
	return IsAllowedTerminalOrigin(r.Header.Get("Origin"))
}

// CreateTerminal starts a new terminal shell in the given directory.
// colors is optional — when provided, the responder uses them for OSC 10/11 replies.
// env is optional — when provided, the terminal runs in the specified remote environment
// (WSL or SSH). Nil means local (default behavior).
func (tm *Manager) CreateTerminal(ctx context.Context, workDir string, colors *ThemeColors, env *RemoteEnv) (*Info, error) {
	if err := tm.ensureWSServer(); err != nil {
		return nil, fmt.Errorf("start terminal server: %w", err)
	}

	t, err := tm.createTerminal(workDir, colors, env)
	if err != nil {
		return nil, err
	}

	// Monitor shell exit → notify frontend.
	go func() {
		_ = t.cmd.Wait()
		close(t.done) // Signal all goroutines that terminal has exited.
		tm.removeTerminal(t.id)
		t.shutdown() // Idempotent via sync.Once.
		if ctx != nil && tm.emitExit != nil {
			tm.emitExit(ctx, t.id)
		}
	}()

	return &Info{ID: t.id, WSPort: tm.port()}, nil
}

// ResizeTerminal updates the PTY window size.
// Note: go-pty Resize takes (width, height) = (cols, rows).
func (tm *Manager) ResizeTerminal(id string, rows, cols uint16) error {
	tm.mu.Lock()
	t, ok := tm.terminals[id]
	tm.mu.Unlock()
	if !ok {
		return fmt.Errorf("terminal %s not found", id)
	}
	return t.pty.Resize(int(cols), int(rows))
}

// CloseTerminal kills the terminal shell and cleans up.
func (tm *Manager) CloseTerminal(id string) {
	tm.mu.Lock()
	t, ok := tm.terminals[id]
	if ok {
		delete(tm.terminals, id)
	}
	tm.mu.Unlock()

	if ok {
		t.shutdown()
	}
	tm.maybeStopWSServer()
}

// ListTerminals returns info for all active terminals.
func (tm *Manager) ListTerminals() []Info {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	wsPort := tm.wsPort
	list := make([]Info, 0, len(tm.terminals))
	for id := range tm.terminals {
		list = append(list, Info{ID: id, WSPort: wsPort})
	}
	return list
}

// CloseAll kills all terminals and shuts down the websocket server.
func (tm *Manager) CloseAll() {
	tm.mu.Lock()
	terminals := make([]*terminalSession, 0, len(tm.terminals))
	for _, t := range tm.terminals {
		terminals = append(terminals, t)
	}
	tm.terminals = make(map[string]*terminalSession)
	server := tm.wsServer
	tm.wsServer = nil
	tm.wsPort = 0
	tm.mu.Unlock()

	for _, t := range terminals {
		t.shutdown()
	}
	if server != nil {
		_ = server.Close()
	}
}

func (tm *Manager) ensureWSServer() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.wsServer != nil {
		return nil
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	tm.wsPort = listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/terminal/", tm.handleTerminalWS)

	tm.wsServer = &http.Server{Handler: mux}
	go func() {
		if err := tm.wsServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("terminal WS server error: %v", err)
		}
	}()

	log.Printf("Terminal WebSocket server started on 127.0.0.1:%d", tm.wsPort)
	return nil
}

func (tm *Manager) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/terminal/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	tm.mu.Lock()
	t, ok := tm.terminals[id]
	tm.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	conn, err := tm.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("terminal WS upgrade error: %v", err)
		return
	}

	// Monitor terminal done signal — close WS when terminal exits.
	go func() {
		<-t.done
		_ = conn.Close()
	}()

	// PTY -> WebSocket.
	// The responder intercepts terminal queries (DA1, DA2, OSC 10/11, DECRQM,
	// kitty keyboard, DCS, etc.), writes responses directly back to the PTY,
	// and strips the queries from the data so xterm.js never sees them and
	// never generates duplicate (delayed) responses.
	go func() {
		defer conn.Close()
		buf := make([]byte, 32*1024)
		for {
			n, err := t.pty.Read(buf)
			if err != nil {
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shell exited"))
				return
			}
			data := buf[:n]
			if t.resp != nil {
				data = t.resp.Filter(data)
			}
			if len(data) == 0 {
				continue // entire chunk was query sequences — nothing to forward
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		}
	}()

	// WebSocket -> PTY.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if t.resp != nil {
			t.resp.LogInput(msg)
		}
		if _, err := t.pty.Write(msg); err != nil {
			return
		}
	}
}

func (tm *Manager) createTerminal(workDir string, colors *ThemeColors, env *RemoteEnv) (*terminalSession, error) {
	id := uuid.NewString()

	shell, args, effectiveWorkDir := resolveShellCommand(workDir, env)

	ptyObj, err := gopty.New()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}

	cmd := ptyObj.Command(shell, args...)
	if effectiveWorkDir != "" {
		cmd.Dir = effectiveWorkDir
	}

	cmdEnv := append(os.Environ(),
		"TERM=xterm-256color",
		"TERM_PROGRAM=xterm.js",
	)

	// Provide COLORFGBG so vim/nvim can infer background without OSC queries.
	if colors != nil && colors.Background != "" {
		bg := "0" // dark
		if !isDarkHex(colors.Background) {
			bg = "15" // light
		}
		cmdEnv = append(cmdEnv, "COLORFGBG=15;"+bg)
	}
	cmd.Env = cmdEnv

	if err := cmd.Start(); err != nil {
		_ = ptyObj.Close()
		return nil, fmt.Errorf("start pty: %w", err)
	}

	// Create a responder to intercept terminal queries and reply directly on the PTY.
	fg := "#ffffff"
	bg := "#000000"
	if colors != nil {
		if colors.Foreground != "" {
			fg = colors.Foreground
		}
		if colors.Background != "" {
			bg = colors.Background
		}
	}
	resp := NewResponder(ptyObj, fg, bg)

	t := &terminalSession{
		id:   id,
		pty:  ptyObj,
		cmd:  cmd,
		resp: resp,
		done: make(chan struct{}),
	}

	tm.mu.Lock()
	tm.terminals[id] = t
	tm.mu.Unlock()

	return t, nil
}

func (tm *Manager) removeTerminal(id string) {
	tm.mu.Lock()
	delete(tm.terminals, id)
	tm.mu.Unlock()
	tm.maybeStopWSServer()
}

func (tm *Manager) maybeStopWSServer() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if len(tm.terminals) > 0 || tm.wsServer == nil {
		return
	}

	_ = tm.wsServer.Close()
	tm.wsServer = nil
	tm.wsPort = 0
	log.Println("Terminal WebSocket server stopped (no active terminals)")
}

func (tm *Manager) port() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.wsPort
}

func (t *terminalSession) shutdown() {
	t.closeOnce.Do(func() {
		t.shutdownProcess() // Platform-specific process termination.
		// Close PTY to trigger WS goroutine Read failure.
		_ = t.pty.Close()
		if t.resp != nil {
			t.resp.Close()
		}
	})
}
