package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestMcpOAuthSpecInjectsBearerHeaderAndKeepsTokenOutOfState(t *testing.T) {
	useIsolatedHome(t)
	cfg := McpServerConfig{
		ID:        "remote",
		Name:      "Remote",
		Source:    mcpSourceManual,
		Transport: mcpTransportHTTP,
		URL:       "https://example.test/mcp",
		AuthMode:  mcpAuthModeOAuth,
		OAuth: McpOAuthConfig{
			AuthURL:  "https://auth.example.test/authorize",
			TokenURL: "https://auth.example.test/token",
			ClientID: "client-1",
			Resource: "https://example.test/mcp",
		},
	}
	if err := saveMcpOAuthToken(cfg.ID, mcpOAuthToken{AccessToken: "access-token", ClientID: "client-1"}); err != nil {
		t.Fatalf("save token: %v", err)
	}
	app := &App{}
	spec, err := app.mcpSpecFromConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("mcpSpecFromConfig: %v", err)
	}
	if got := spec.Headers["Authorization"]; got != "Bearer access-token" {
		t.Fatalf("Authorization header = %q", got)
	}
	if err := saveMcpHubState(&mcpHubState{Servers: map[string]McpServerConfig{cfg.ID: cfg}}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	path, err := mcpHubStatePath()
	if err != nil {
		t.Fatalf("state path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(data), "access-token") {
		t.Fatalf("state.json contains OAuth access token: %s", data)
	}
}

func TestMcpOAuthSpecRequiresAuthorizationWhenTokenMissing(t *testing.T) {
	useIsolatedHome(t)
	app := &App{}
	_, err := app.mcpSpecFromConfig(context.Background(), McpServerConfig{
		ID:        "remote",
		Transport: mcpTransportHTTP,
		URL:       "https://example.test/mcp",
		AuthMode:  mcpAuthModeOAuth,
		OAuth:     McpOAuthConfig{AuthURL: "https://auth.example.test/authorize", TokenURL: "https://auth.example.test/token"},
	})
	if err == nil || !strings.Contains(err.Error(), "requires OAuth authorization") {
		t.Fatalf("expected authorization-required error, got %v", err)
	}
}

func TestBuildMcpOAuthAuthorizeURLUsesPKCEAndResource(t *testing.T) {
	got, err := buildMcpOAuthAuthorizeURL(McpOAuthConfig{
		AuthURL:  "https://auth.example.test/authorize?existing=1",
		ClientID: "client-1",
		Scopes:   []string{"email", "profile"},
		Resource: "https://mcp.example.test/mcp",
	}, "http://127.0.0.1:1234/mcp/oauth/callback", "state-1", "challenge-1")
	if err != nil {
		t.Fatalf("build auth url: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := parsed.Query()
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "client-1",
		"redirect_uri":          "http://127.0.0.1:1234/mcp/oauth/callback",
		"state":                 "state-1",
		"code_challenge":        "challenge-1",
		"code_challenge_method": "S256",
		"scope":                 "email profile",
		"resource":              "https://mcp.example.test/mcp",
	} {
		if got := q.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestDiscoverMcpOAuthConfigFromProtectedResourceMetadata(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+server.URL+`/.well-known/oauth-protected-resource/mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource/mcp":
			writeOAuthJSON(t, w, map[string]any{
				"resource":              server.URL + "/mcp",
				"authorization_servers": []string{server.URL + "/issuer"},
				"scopes_supported":      []string{"email"},
			})
		case "/.well-known/oauth-authorization-server/issuer":
			writeOAuthJSON(t, w, map[string]any{
				"issuer":                           server.URL + "/issuer",
				"authorization_endpoint":           server.URL + "/authorize",
				"token_endpoint":                   server.URL + "/token",
				"registration_endpoint":            server.URL + "/register",
				"code_challenge_methods_supported": []string{"S256"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg, meta, err := discoverMcpOAuthConfig(context.Background(), server.URL+"/mcp")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if cfg.AuthURL != server.URL+"/authorize" || cfg.TokenURL != server.URL+"/token" {
		t.Fatalf("unexpected endpoints: %+v", cfg)
	}
	if cfg.Resource != server.URL+"/mcp" {
		t.Fatalf("resource = %q", cfg.Resource)
	}
	if meta == nil || meta.RegistrationEndpoint != server.URL+"/register" {
		t.Fatalf("metadata not returned: %+v", meta)
	}
}

func TestRunMcpOAuthAuthorizationRejectsMissingPKCESupport(t *testing.T) {
	useIsolatedHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+r.Host+`/.well-known/oauth-protected-resource/mcp"`)
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/.well-known/oauth-protected-resource/mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource/mcp":
			writeOAuthJSON(t, w, map[string]any{
				"resource":              "http://" + r.Host + "/mcp",
				"authorization_servers": []string{"http://" + r.Host + "/issuer"},
			})
		case "/.well-known/oauth-authorization-server/issuer":
			writeOAuthJSON(t, w, map[string]any{
				"issuer":                 "http://" + r.Host + "/issuer",
				"authorization_endpoint": "http://" + r.Host + "/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err := app.runMcpOAuthAuthorization(ctx, McpServerConfig{
		ID:        "remote",
		Name:      "Remote",
		Transport: mcpTransportHTTP,
		URL:       server.URL + "/mcp",
		AuthMode:  mcpAuthModeOAuth,
	})
	if err == nil || !strings.Contains(err.Error(), "does not advertise PKCE S256 support") {
		t.Fatalf("expected PKCE S256 metadata error, got %v", err)
	}
}

func TestDiscoverMcpOAuthServerMetadataUsesOIDCPathAppending(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tenant/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		writeOAuthJSON(t, w, map[string]any{
			"issuer":                           server.URL + "/tenant",
			"authorization_endpoint":           server.URL + "/tenant/authorize",
			"token_endpoint":                   server.URL + "/tenant/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
	}))
	defer server.Close()

	meta, err := discoverMcpOAuthServerMetadata(context.Background(), server.URL+"/tenant")
	if err != nil {
		t.Fatalf("discover metadata: %v", err)
	}
	if meta.AuthorizationEndpoint != server.URL+"/tenant/authorize" {
		t.Fatalf("authorization endpoint = %q", meta.AuthorizationEndpoint)
	}
}

func TestMcpOAuthReadyForBackgroundStartRequiresFreshOrRefreshableToken(t *testing.T) {
	useIsolatedHome(t)
	if mcpOAuthReadyForBackgroundStart("remote") {
		t.Fatal("missing token should not be ready")
	}
	if err := saveMcpOAuthToken("remote", mcpOAuthToken{AccessToken: "expired", Expiry: 1}); err != nil {
		t.Fatalf("save expired token: %v", err)
	}
	if mcpOAuthReadyForBackgroundStart("remote") {
		t.Fatal("expired token without refresh token should not be ready")
	}
	if err := saveMcpOAuthToken("remote", mcpOAuthToken{AccessToken: "expired", RefreshToken: "refresh", Expiry: 1}); err != nil {
		t.Fatalf("save refreshable token: %v", err)
	}
	if !mcpOAuthReadyForBackgroundStart("remote") {
		t.Fatal("expired token with refresh token should be ready for refresh attempt")
	}
	if err := saveMcpOAuthToken("remote", mcpOAuthToken{AccessToken: "expired", RefreshToken: "refresh", Expiry: 1, RefreshFailedAt: 42}); err != nil {
		t.Fatalf("save failed refresh token: %v", err)
	}
	if mcpOAuthReadyForBackgroundStart("remote") {
		t.Fatal("token with failed refresh marker should not be ready for background start")
	}
	if got := mcpOAuthStatus("remote"); got != "token_error" {
		t.Fatalf("status = %q, want token_error", got)
	}
}

func TestMcpOAuthAccessTokenMarksRefreshFailure(t *testing.T) {
	useIsolatedHome(t)
	var tokenServer *httptest.Server
	tokenServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			writeOAuthJSON(t, w, map[string]any{
				"issuer":                           tokenServer.URL,
				"authorization_endpoint":           tokenServer.URL + "/authorize",
				"token_endpoint":                   tokenServer.URL + "/token",
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/token":
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer tokenServer.Close()
	cfg := McpServerConfig{
		ID:        "remote",
		Transport: mcpTransportHTTP,
		URL:       "http://127.0.0.1/mcp",
		AuthMode:  mcpAuthModeOAuth,
		OAuth: McpOAuthConfig{
			AuthURL:  tokenServer.URL + "/authorize",
			TokenURL: tokenServer.URL + "/token",
			ClientID: "client-1",
		},
	}
	if err := saveMcpOAuthToken("remote", mcpOAuthToken{AccessToken: "expired", RefreshToken: "bad-refresh", Expiry: 1, ClientID: "client-1"}); err != nil {
		t.Fatalf("save token: %v", err)
	}
	app := &App{}
	_, err := app.mcpOAuthAccessToken(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "refresh OAuth token") {
		t.Fatalf("expected refresh failure, got %v", err)
	}
	token, err := loadMcpOAuthToken("remote")
	if err != nil {
		t.Fatalf("load token: %v", err)
	}
	if token.RefreshFailedAt == 0 {
		t.Fatal("refresh failure marker was not persisted")
	}
	if mcpOAuthReadyForBackgroundStart("remote") {
		t.Fatal("failed refresh token should not be background-start ready")
	}
}

func TestExchangeMcpOAuthCodeSendsResourceAndVerifierInBodyOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Fatalf("token request should not put OAuth parameters in query: %s", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("public client should not use Authorization header, got %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		for key, want := range map[string]string{
			"grant_type":    "authorization_code",
			"code":          "code-1",
			"client_id":     "client-1",
			"code_verifier": "verifier-1",
			"resource":      "https://mcp.example.test/mcp",
		} {
			if got := r.Form.Get(key); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
		writeOAuthJSON(t, w, map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	resp, err := exchangeMcpOAuthCode(context.Background(), McpOAuthConfig{
		TokenURL: server.URL,
		ClientID: "client-1",
		Resource: "https://mcp.example.test/mcp",
	}, "code-1", "http://127.0.0.1/callback", "verifier-1")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if resp.AccessToken != "access-token" {
		t.Fatalf("access token = %q", resp.AccessToken)
	}
}

func writeOAuthJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
