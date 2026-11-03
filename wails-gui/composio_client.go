package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// composio_client.go — the composioClient interface + its two implementations.
//
// Until Path B, the only Composio touchpoints were ad-hoc apiGet/apiPost to the
// Yansu backend (+ an MCP-only fetcher). This abstracts all of them behind one
// interface so the desktop can run either:
//   - backendComposioClient: proxy through Yansu (normal build, gateway path)
//   - directComposioClient:  direct to Composio with the user's own key (Path B)
//
// a.composioClient() picks based on useDirectComposio() (a local key is saved).

// composioClient covers every Composio operation the App needs. Status returns
// the backend status shape (composioStatusResponse) so buildComposioStatus is
// shared by both implementations.
type composioClient interface {
	Status(ctx context.Context) (composioStatusResponse, error)
	Connect(ctx context.Context, app string) (redirectURL string, err error)
	Disconnect(ctx context.Context, app string) error
	MCPConfig(ctx context.Context, app string) (mcpURL string, headers map[string]string, err error)
}

// composioClient returns the active implementation. Direct (BYO key) when a
// local Composio API key is configured — the only path in standalone (backend
// proxy is denied) and an explicit opt-in in normal builds.
func (a *App) composioClient() composioClient {
	if useDirectComposio() {
		return &directComposioClient{app: a}
	}
	return &backendComposioClient{app: a}
}

// ── status projection (shared) ──

const (
	composioStatusConnected    = "connected"
	composioStatusConnecting   = "connecting"
	composioStatusDisconnected = "disconnected"
)

// composioProjectStatus maps Composio's connected_account status enum
// (ACTIVE / INITIATED / INITIALIZING / EXPIRED / FAILED / INACTIVE, plus any
// future/unknown value) onto the three states the UI understands. Unknown
// values are treated as not-ready (connecting), never connected.
func composioProjectStatus(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "ACTIVE":
		return composioStatusConnected
	case "INITIATED", "INITIALIZING", "PENDING", "":
		return composioStatusConnecting
	case "EXPIRED", "FAILED", "INACTIVE", "DELETED":
		return composioStatusDisconnected
	default:
		return composioStatusConnecting
	}
}

// composioDisplayName returns the human label for a managed app slug.
func composioDisplayName(app string) string {
	switch app {
	case composioGoogleSuperApp:
		return "Google Super"
	case composioSlackBotApp:
		return "Slackbot"
	case composioNotionApp:
		return "Notion"
	case composioGitHubApp:
		return "GitHub"
	case composioLinearApp:
		return "Linear"
	default:
		return app
	}
}

// ── backend implementation (proxy through Yansu) ──

type backendComposioClient struct{ app *App }

func (c *backendComposioClient) Status(ctx context.Context) (composioStatusResponse, error) {
	var resp composioStatusResponse
	token, err := loadYansuToken()
	if err != nil || token == "" {
		return resp, fmt.Errorf("not authenticated")
	}
	if err := apiGet(loadAPIBaseURL(), token, "/api/composio/status", &resp); err != nil {
		return resp, err
	}
	return resp, nil
}

func (c *backendComposioClient) Connect(ctx context.Context, app string) (string, error) {
	token, err := loadYansuToken()
	if err != nil || token == "" {
		return "", fmt.Errorf("not authenticated")
	}
	var resp composioConnectResponse
	if err := apiPost(loadAPIBaseURL(), token, "/api/composio/connect", map[string]string{"app": app}, &resp); err != nil {
		return "", fmt.Errorf("backend connect: %w", err)
	}
	if resp.RedirectURL == "" {
		return "", fmt.Errorf("backend did not return a redirect URL")
	}
	return resp.RedirectURL, nil
}

func (c *backendComposioClient) Disconnect(ctx context.Context, app string) error {
	token, err := loadYansuToken()
	if err != nil || token == "" {
		return nil
	}
	if err := apiDelete(loadAPIBaseURL(), token, "/api/composio/connect?app="+url.QueryEscape(app)); err != nil {
		return fmt.Errorf("backend disconnect: %w", err)
	}
	return nil
}

func (c *backendComposioClient) MCPConfig(ctx context.Context, app string) (string, map[string]string, error) {
	return (&composioBackendFetcher{app: c.app}).GetMCPConfig(ctx, app)
}
