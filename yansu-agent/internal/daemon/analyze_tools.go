package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
	"yansu-agent/agent/tools/guard"
)

const analyzeMCPTools = "mcp__isoform_analyze__Read,mcp__isoform_analyze__Glob,mcp__isoform_analyze__Grep"

// analyzeToolBridge exposes the existing native guards to the external model.
// Each instance has one immutable project scope and one short-lived credential;
// no request can select a different workspace or register additional tools.
type analyzeToolBridge struct {
	projectRoot string
	registry    *tools.Registry
	policy      *authority.Policy
	config      string
	endpoint    string
	token       string
	httpServer  *http.Server
	transport   *server.StreamableHTTPServer
	closeOnce   sync.Once
}

func startAnalyzeToolBridge(ctx context.Context, projectRoot string) (*analyzeToolBridge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := guard.CanonicalPath(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("Isoform analyze requires an absolute project folder: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Isoform analyze project folder is unavailable")
	}
	if err := tools.CheckProtectedPaths("Glob", map[string]any{"path": root, "pattern": "**/*"}, root); err != nil {
		return nil, err
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool())
	registry.Register(tools.NewGlobTool())
	registry.Register(tools.NewGrepTool())
	bridge := &analyzeToolBridge{projectRoot: root, registry: registry}
	var grants []authority.Grant
	for _, capability := range []string{"tool.read", "tool.glob", "tool.grep"} {
		grants = append(grants, authority.Grant{ID: "explicit-project-analysis", Capability: capability, ResourcePrefix: root})
	}
	bridge.policy = authority.NewPolicy(grants)
	protocol := server.NewMCPServer("isoform-analyze", "1.0.0", server.WithToolCapabilities(false))
	for _, name := range []string{"Read", "Glob", "Grep"} {
		tool := registry.Get(name)
		schema, err := json.Marshal(tool.JSONSchema())
		if err != nil {
			return nil, err
		}
		protocol.AddTool(mcp.NewToolWithRawSchema(name, tool.Description(), schema), bridge.callTool)
	}
	bridge.transport = server.NewStreamableHTTPServer(protocol, server.WithStateLess(true), server.WithDisableStreaming(true))
	var credential [32]byte
	if _, err := rand.Read(credential[:]); err != nil {
		return nil, err
	}
	bridge.token = hex.EncodeToString(credential[:])
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	bridge.endpoint = "http://" + listener.Addr().String() + "/mcp"
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"isoform_analyze": map[string]any{
		"type": "http", "url": bridge.endpoint, "headers": map[string]string{"Authorization": "Bearer " + bridge.token},
	}}})
	if err != nil {
		listener.Close()
		return nil, err
	}
	bridge.config = string(config)
	bridge.httpServer = &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      70 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Context().Err() != nil {
				http.Error(w, "analyze task ended", http.StatusServiceUnavailable)
				return
			}
			if r.Host != listener.Addr().String() || r.Header.Get("Origin") != "" || r.URL.Path != "/mcp" {
				http.Error(w, "invalid analyze bridge request", http.StatusForbidden)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+bridge.token)) != 1 {
				http.Error(w, "analyze bridge authorization required", http.StatusUnauthorized)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
			bridge.transport.ServeHTTP(w, r)
		}),
	}
	context.AfterFunc(ctx, bridge.close)
	go bridge.httpServer.Serve(listener)
	return bridge, nil
}

func (b *analyzeToolBridge) close() {
	b.closeOnce.Do(func() {
		_ = b.httpServer.Close()
		_ = b.transport.Shutdown(context.Background())
	})
}

func (b *analyzeToolBridge) callTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcp.NewToolResultError("Isoform analyze task ended"), nil
	}
	name := request.Params.Name
	if b.registry.Get(name) == nil {
		return mcp.NewToolResultError("Isoform analyze permits only Read, Glob and Grep"), nil
	}
	input := make(map[string]any, len(request.GetArguments())+1)
	for key, value := range request.GetArguments() {
		input[key] = value
	}
	path, _ := input["path"].(string)
	if path == "" && (name == "Glob" || name == "Grep") {
		path = b.projectRoot
		input["path"] = path
	}
	// Check canonical containment here as well as inside the native tools.
	// This boundary must survive changes to a tool's default WorkDir policy.
	if err := guard.WithinAllowed(path, []string{b.projectRoot}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := tools.CheckProtectedPaths(name, input, b.projectRoot); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := b.registry.Execute(ctx, name, tools.ToolCall{
		WorkDir: b.projectRoot, Input: input,
		Authority: &tools.AuthorityRuntime{Context: authority.Context{Unattended: true}, Evaluator: b.policy},
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	response := mcp.NewToolResultText(result.Content)
	response.IsError = result.IsError
	if len(result.Blocks) > 0 {
		response.Content = nil
		for _, block := range result.Blocks {
			switch block.Type {
			case "text":
				response.Content = append(response.Content, mcp.NewTextContent(block.Text))
			case "image":
				if block.Source != nil {
					response.Content = append(response.Content, mcp.NewImageContent(block.Source.Data, block.Source.MediaType))
				}
			}
		}
	}
	return response, nil
}
