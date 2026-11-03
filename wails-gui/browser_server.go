package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const defaultBrowserPort = 34116

// browserServer is a localhost-only HTTP + WebSocket server that lets the
// frontend run in a regular browser during development: App methods on the
// browser RPC allowlist through POST /api/call, and app events over /ws.
// Every route passes guard (browser_bridge_guard.go); /api/call and /ws also
// need the per-launch bridge token.
type browserServer struct {
	srv         *http.Server
	port        int
	app         *App
	broadcaster *EventBroadcaster

	token          string
	tokenPath      string
	allowedHosts   map[string]bool
	allowedOrigins map[string]bool
}

// browserAPIResponse is the standard JSON envelope for all REST responses.
type browserAPIResponse struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// startBrowserServer starts the browser-mode HTTP server. If the BROWSER_PORT
// env var is set it uses that port; otherwise it defaults to 34116.
func startBrowserServer(app *App, broadcaster *EventBroadcaster) (*browserServer, error) {
	port := defaultBrowserPort
	if envPort := os.Getenv("BROWSER_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			port = p
		}
	}

	token, err := newBrowserBridgeToken()
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("browser server listen: %w", err)
	}

	bs := newBrowserServer(app, broadcaster, port, token)
	bs.tokenPath = browserBridgeTokenPath()
	if err := writeBrowserBridgeToken(bs.tokenPath, token); err != nil {
		// The browser transport reads the token from /api/bridge-token, so
		// the bridge still works; only tools that read the file lose it.
		log.Printf("[browser-server] could not write the bridge token file: %v", err)
		bs.tokenPath = ""
	}

	bs.srv = &http.Server{
		Handler:      bs.routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		log.Printf("[browser-server] listening on 127.0.0.1:%d", port)
		if err := bs.srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("[browser-server] serve error: %v", err)
		}
	}()

	return bs, nil
}

// newBrowserServer builds the server state without listening, so tests can
// serve routes() through httptest.
func newBrowserServer(app *App, broadcaster *EventBroadcaster, port int, token string) *browserServer {
	return &browserServer{
		port:           port,
		app:            app,
		broadcaster:    broadcaster,
		token:          token,
		allowedHosts:   browserBridgeAllowedHosts(port),
		allowedOrigins: browserBridgeAllowedOrigins(port),
	}
}

// routes registers every endpoint behind guard.
func (bs *browserServer) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// WebSocket endpoint for event streaming.
	mux.HandleFunc("/ws", bs.guard(bs.requireToken(true, bs.handleWebSocket)))

	// Generic RPC endpoint: POST /api/call with {method, args}. This is the
	// only way the frontend calls Go from a browser.
	mux.HandleFunc("/api/call", bs.guard(bs.postJSON(bs.requireToken(false, bs.handleGenericCall))))

	// The token for the browser transport.
	mux.HandleFunc("/api/bridge-token", bs.guard(bs.handleBridgeToken))

	// Health check.
	mux.HandleFunc("/api/health", bs.guard(bs.handleHealth))

	// Static file serving for production builds (SPA fallback).
	bs.registerStaticFiles(mux)

	return mux
}

// URL returns the base URL of the browser server.
func (bs *browserServer) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", bs.port)
}

// Stop gracefully shuts down the browser server and removes its token file.
func (bs *browserServer) Stop() {
	if bs.srv == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := bs.srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[browser-server] shutdown error: %v", err)
	}
	removeBrowserBridgeToken(bs.tokenPath, bs.token)
	log.Printf("[browser-server] stopped")
}

// -- Response helpers ---------------------------------------------------------

func (bs *browserServer) writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	json.NewEncoder(w).Encode(browserAPIResponse{OK: true, Data: data})
}

func (bs *browserServer) writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(browserAPIResponse{OK: false, Error: msg})
}

// -- WebSocket ----------------------------------------------------------------

// browserWSReadLimit caps a client message; the client only sends small
// keep-alive and event frames.
const browserWSReadLimit = 64 << 10

func (bs *browserServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// guard already refused a disallowed Origin; an empty Origin
			// comes from a non-browser client holding the token.
			origin := r.Header.Get("Origin")
			return origin == "" || bs.allowedOrigins[strings.ToLower(origin)]
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[browser-server] websocket upgrade failed: %v", err)
		return
	}
	conn.SetReadLimit(browserWSReadLimit)

	clientID := uuid.New().String()
	sendCh := bs.broadcaster.AddClient(clientID)

	// Writer goroutine: reads from the channel and writes to the WebSocket.
	go func() {
		defer conn.Close()
		defer bs.broadcaster.RemoveClient(clientID)
		for msg := range sendCh {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("[browser-server] ws write error for %s: %v", clientID, err)
				return
			}
		}
	}()

	// Reader goroutine: drains incoming messages (keep-alive pings).
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				bs.broadcaster.RemoveClient(clientID)
				return
			}
		}
	}()
}

// -- Health -------------------------------------------------------------------

func (bs *browserServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	bs.writeOK(w, nil)
}

// -- Generic RPC handler ------------------------------------------------------

// browserCallMaxBytes caps an /api/call body; pasted images make the largest
// calls.
const browserCallMaxBytes = 64 << 20

// handleGenericCall accepts {method, args} and invokes the named public method
// on the App struct via reflection. This is the main endpoint the frontend
// browser bridge calls for all Go method invocations.
func (bs *browserServer) handleGenericCall(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, browserCallMaxBytes)
	var body struct {
		Method string            `json:"method"`
		Args   []json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		bs.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Method == "" {
		bs.writeError(w, http.StatusBadRequest, "method required")
		return
	}
	if !browserRPCAllowlist[body.Method] {
		bs.writeError(w, http.StatusForbidden, fmt.Sprintf("method not allowed: %s", body.Method))
		return
	}
	if err := checkBrowserRPCArgs(body.Method, body.Args); err != nil {
		bs.writeError(w, http.StatusForbidden, err.Error())
		return
	}

	appVal := reflect.ValueOf(bs.app)
	method := appVal.MethodByName(body.Method)
	if !method.IsValid() {
		bs.writeError(w, http.StatusNotFound, fmt.Sprintf("unknown method: %s", body.Method))
		return
	}

	methodType := method.Type()
	numIn := methodType.NumIn()

	if len(body.Args) < numIn {
		// Pad with zero values for missing optional arguments.
		for len(body.Args) < numIn {
			body.Args = append(body.Args, json.RawMessage("null"))
		}
	}

	// Convert JSON args to the expected Go types.
	in := make([]reflect.Value, numIn)
	for i := 0; i < numIn; i++ {
		paramType := methodType.In(i)
		paramPtr := reflect.New(paramType)
		if err := json.Unmarshal(body.Args[i], paramPtr.Interface()); err != nil {
			bs.writeError(w, http.StatusBadRequest, fmt.Sprintf("arg %d (%s): %v", i, paramType.Name(), err))
			return
		}
		in[i] = paramPtr.Elem()
	}

	// Call the method.
	out := method.Call(in)

	// Parse return values. Convention: (result, error) or (error) or ().
	var result any
	var callErr error

	for _, v := range out {
		if v.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			if !v.IsNil() {
				callErr = v.Interface().(error)
			}
		} else {
			result = v.Interface()
		}
	}

	if callErr != nil {
		bs.writeError(w, http.StatusInternalServerError, callErr.Error())
		return
	}
	bs.writeOK(w, redactBrowserRPCResult(body.Method, result))
}

// -- Static file serving ------------------------------------------------------

// registerStaticFiles serves the production frontend build directly out of
// the embedded assets FS declared in main.go. This lets production .app
// builds serve the UI at http://localhost:34116/ without any on-disk files.
// Falls back to index.html for SPA client-side routing.
func (bs *browserServer) registerStaticFiles(mux *http.ServeMux) {
	sub, err := fs.Sub(frontendAssets(), "frontend/dist")
	if err != nil {
		log.Printf("[browser-server] embed sub fs failed: %v", err)
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("/", bs.guard(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
			http.NotFound(w, r)
			return
		}

		// Try to resolve the requested file inside the embedded tree. If it
		// doesn't exist, fall back to index.html for SPA routing.
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		if _, statErr := fs.Stat(sub, reqPath); statErr != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	}))
}
