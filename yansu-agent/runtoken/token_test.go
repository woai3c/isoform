package runtoken

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent/authority"
)

func TestRegistryLifetimeAndIsolation(t *testing.T) {
	var registry Registry
	scope := Scope{RunID: "run", SessionID: "chat", Workspace: t.TempDir(), Endpoints: []Endpoint{{"GET", "/memory/list"}}, Grants: []authority.Grant{{ID: "approved", Capability: "memory.read"}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	token, revoke, err := registry.Mint(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	other, releaseOther, err := registry.Mint(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOther()
	scope.Grants[0].ID = "forged"
	verified, err := registry.Verify(token)
	if err != nil || verified.Grants[0].ID != "approved" {
		t.Fatal("issuer scope was not frozen")
	}
	verified.Endpoints[0].Path = "/mcp/install"
	verified, _ = registry.Verify(token)
	if !verified.Allows("GET", "/memory/list") || verified.Allows("POST", "/memory/list") || verified.Allows("POST", "/mcp/install") {
		t.Fatal("endpoint scope broadened")
	}
	revoke()
	revoke()
	if _, err := registry.Verify(token); !errors.Is(err, ErrInvalid) {
		t.Fatal("revoked token accepted")
	}
	if _, err := registry.Verify(other); err != nil {
		t.Fatal("revocation affected a different run")
	}
	cancel()
	if _, err := registry.Verify(other); !errors.Is(err, ErrInvalid) {
		t.Fatal("cancelled token accepted before cleanup")
	}
}

func TestRegistryExpiryAndShutdown(t *testing.T) {
	var registry Registry
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	scope := Scope{RunID: "run", SessionID: "chat", Workspace: t.TempDir()}
	token, release, err := registry.Mint(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	<-ctx.Done()
	if _, err := registry.Verify(token); !errors.Is(err, ErrInvalid) {
		t.Fatal("expired token accepted")
	}
	if _, _, err := registry.Mint(ctx, scope); !errors.Is(err, ErrInvalid) {
		t.Fatal("expired run minted a token")
	}
	if _, _, err := registry.Mint(context.Background(), scope); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded token minted")
	}
	live, stop := context.WithTimeout(context.Background(), time.Hour)
	defer stop()
	token, releaseLive, err := registry.Mint(live, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLive()
	registry.Close()
	if _, err := registry.Verify(token); !errors.Is(err, ErrInvalid) {
		t.Fatal("shutdown did not revoke token")
	}
	if _, _, err := registry.Mint(live, scope); !errors.Is(err, ErrInvalid) {
		t.Fatal("closed issuer minted token")
	}
}

func TestChildEnvRequiresTypedCredential(t *testing.T) {
	amb := []string{"YANSU_CRON_SECRET=master", TokenEnv + "=forged", PortEnv + "=1", ScopedEnv + "=1", "PATH=/bin"}
	if got := strings.Join(ChildEnv(context.Background(), amb), "\n"); got != "PATH=/bin" {
		t.Fatal("ambient credential passed to child")
	}
	ctx := WithCredential(context.Background(), Credential{Token: "run-issued", Port: 12345})
	got := strings.Join(ChildEnv(ctx, amb), "\n")
	if strings.Contains(got, "master") || strings.Contains(got, "forged") || !strings.Contains(got, TokenEnv+"=run-issued") || !strings.Contains(got, PortEnv+"=12345") {
		t.Fatal("child did not receive only issued credential")
	}
}

func TestRegistryFreezesCanonicalFilesystemGrants(t *testing.T) {
	workspace := t.TempDir()
	canonical, err := Workspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	for _, test := range []struct{ name, prefix, want string }{
		{"alias", alias, canonical},
		{"trimmed alias", " " + alias + " ", canonical},
		{"all resources", "", ""},
		{"url", "https://example.com/path/", "https://example.com/path/"},
		{"opencli", "opencli:mail", "opencli:mail"},
		{"bundle id", "com.example.app", "com.example.app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var registry Registry
			scope := Scope{RunID: "run", SessionID: "chat", Workspace: alias, Grants: []authority.Grant{{ID: "approved", Capability: "schedule.read", ResourcePrefix: test.prefix}}}
			token, end, err := registry.Mint(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			defer end()
			verified, err := registry.Verify(token)
			if err != nil || verified.Workspace != canonical || verified.Grants[0].ResourcePrefix != test.want {
				t.Fatalf("canonical claims = %+v, error = %v; want prefix %q", verified, err, test.want)
			}
			if scope.Grants[0].ResourcePrefix != test.prefix {
				t.Fatal("mint mutated the caller's grant")
			}
		})
	}
	missing := filepath.Join(workspace, "missing")
	dangling := filepath.Join(workspace, "dangling")
	if err := os.Symlink(missing, dangling); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{missing, dangling} {
		var registry Registry
		if token, _, err := registry.Mint(ctx, Scope{RunID: "run", SessionID: "chat", Workspace: workspace, Grants: []authority.Grant{{ResourcePrefix: prefix}}}); err == nil || token != "" {
			t.Fatal("unresolved filesystem grant minted a credential")
		}
	}
}
