package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHomeDirT redirects HOME (and APPDATA on windows) to a temp dir for
// the duration of the test, so DiscoverLocalMcpServers reads our
// fixtures rather than real user files. Returns the temp dir path.
func fakeHomeDirT(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("APPDATA", filepath.Join(tmp, "AppData", "Roaming"))
	return tmp
}

func writeJSONFixture(t *testing.T, path string, payload any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSanitiseMcpDiscoveryName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"filesystem", "filesystem"},
		{"my_server", "my-server"},
		{"My GitHub Server", "my-github-server"},
		{"---weird---", "weird"},
		{"a__b__c", "a-b-c"},
		{"", ""},
		// Truncation: 32 'a's collapses to 31.
		{strings.Repeat("a", 40), strings.Repeat("a", 31)},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := sanitiseMcpDiscoveryName(tc.in)
			if got != tc.want {
				t.Fatalf("sanitiseMcpDiscoveryName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMcpIDFromDiscoveryNeedsHashWhenIDPatternRejects(t *testing.T) {
	// The dot is rejected by mcpServerIDPattern, so the helper must
	// fall back to a hashed candidate rather than returning the raw
	// sanitised string.
	id, err := mcpIDFromDiscovery("cursor", "my.server")
	if err != nil {
		t.Fatalf("mcpIDFromDiscovery: %v", err)
	}
	if !mcpServerIDPattern.MatchString(id) {
		t.Fatalf("derived id %q does not match mcpServerIDPattern", id)
	}
}

func TestBuildDiscoveredServerStdio(t *testing.T) {
	spec := mcpDiscoverySourceSpec{ID: "claude-desktop", Name: "Claude Desktop"}
	got, ok := buildDiscoveredServer(spec, "/tmp/cfg.json", "filesystem", rawMcpServerEntry{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		Env:     map[string]string{"FOO": "bar"},
	})
	if !ok {
		t.Fatalf("expected stdio entry to be importable")
	}
	if got.Transport != mcpTransportStdio {
		t.Fatalf("transport = %q, want stdio", got.Transport)
	}
	if got.Command != "npx" || len(got.Args) != 3 {
		t.Fatalf("command/args mismatch: %+v", got)
	}
	if got.Env["FOO"] != "bar" {
		t.Fatalf("env not preserved: %+v", got.Env)
	}
}

// TestBuildDiscoveredServerSkipsSSE locks the P2 contract: SSE/HTTP
// transports are dropped from discovery so the GUI never offers an
// Import button for a server validateMcpServerConfig would reject.
// When P2 implements them, restore Transport/URL handling here and add
// a positive case asserting got.URL is preserved.
func TestBuildDiscoveredServerSkipsSSE(t *testing.T) {
	spec := mcpDiscoverySourceSpec{ID: "cursor", Name: "Cursor"}
	if _, ok := buildDiscoveredServer(spec, "/tmp/cfg.json", "remote", rawMcpServerEntry{
		Transport: map[string]any{"type": "sse", "url": "https://example.com/sse"},
	}); ok {
		t.Fatalf("sse entry must be dropped while transport is reserved for P2")
	}
	if _, ok := buildDiscoveredServer(spec, "/tmp/cfg.json", "remote", rawMcpServerEntry{
		Type: "http",
		URL:  "https://example.com/h",
	}); ok {
		t.Fatalf("http entry must be dropped while transport is reserved for P2")
	}
}

func TestBuildDiscoveredServerRejectsEmpty(t *testing.T) {
	spec := mcpDiscoverySourceSpec{ID: "cursor", Name: "Cursor"}
	if _, ok := buildDiscoveredServer(spec, "/tmp/cfg.json", "broken", rawMcpServerEntry{}); ok {
		t.Fatalf("expected entry with no command/url to be rejected")
	}
}

func TestDiscoverLocalMcpServersWalksClaudeDesktopOnDarwin(t *testing.T) {
	home := fakeHomeDirT(t)

	// Claude Desktop fixture (path layout depends on GOOS — we cover the
	// macOS path here; the Linux fallback is exercised by setting HOME
	// without "Library/Application Support" too).
	desktopPath := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	writeJSONFixture(t, desktopPath, map[string]any{
		"mcpServers": map[string]any{
			"filesystem": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			},
			"remote": map[string]any{
				"type": "sse",
				"url":  "https://example.com/sse",
			},
		},
	})
	// Linux fallback path so the test still finds at least one source on
	// non-darwin runners. On darwin both paths exist; that's fine because
	// the report will include both rows but both still parse.
	linuxPath := filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	writeJSONFixture(t, linuxPath, map[string]any{
		"mcpServers": map[string]any{
			"filesystem": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			},
		},
	})

	app := &App{}
	report, err := app.DiscoverLocalMcpServers()
	if err != nil {
		t.Fatalf("DiscoverLocalMcpServers: %v", err)
	}
	if len(report.Servers) == 0 {
		t.Fatalf("expected at least one discovered server, got %+v", report)
	}
	var foundFs bool
	for _, srv := range report.Servers {
		if srv.SourceID != "claude-desktop" {
			continue
		}
		switch srv.Name {
		case "filesystem":
			foundFs = true
			if srv.Transport != mcpTransportStdio {
				t.Fatalf("filesystem transport = %q", srv.Transport)
			}
		case "remote":
			// SSE/HTTP transports are dropped from discovery while
			// support is P2 — see TestBuildDiscoveredServerSkipsSSE.
			t.Fatalf("sse \"remote\" entry leaked into discovery: %+v", srv)
		}
	}
	if !foundFs {
		t.Fatalf("filesystem entry missing from report: %+v", report)
	}
}

func TestDiscoverLocalMcpServersTolerantOfMissingFiles(t *testing.T) {
	fakeHomeDirT(t)
	app := &App{}
	report, err := app.DiscoverLocalMcpServers()
	if err != nil {
		t.Fatalf("DiscoverLocalMcpServers: %v", err)
	}
	if len(report.Servers) != 0 {
		t.Fatalf("expected zero servers when no fixtures present, got %d", len(report.Servers))
	}
	for _, src := range report.Sources {
		if src.Found {
			t.Fatalf("source %s should not be found in empty home: %+v", src.ID, src)
		}
	}
}

func TestDiscoverLocalMcpServersRecordsParseError(t *testing.T) {
	home := fakeHomeDirT(t)
	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cursorPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cursorPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	app := &App{}
	report, err := app.DiscoverLocalMcpServers()
	if err != nil {
		t.Fatalf("DiscoverLocalMcpServers: %v", err)
	}
	var sawError bool
	for _, src := range report.Sources {
		if src.ID == "cursor" {
			if src.Error == "" {
				t.Fatalf("expected parse error for cursor source: %+v", src)
			}
			if src.Found {
				t.Fatalf("cursor with parse error should report Found=false: %+v", src)
			}
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("cursor source not present in report: %+v", report)
	}
}

func TestImportLocalMcpServerEndToEnd(t *testing.T) {
	home := fakeHomeDirT(t)

	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	writeJSONFixture(t, cursorPath, map[string]any{
		"mcpServers": map[string]any{
			"filesystem": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
				"env":     map[string]string{"FOO": "bar"},
			},
		},
	})

	app := &App{}
	view, err := app.ImportLocalMcpServer("cursor", "filesystem", false)
	if err != nil {
		t.Fatalf("ImportLocalMcpServer: %v", err)
	}
	if view.ID != "filesystem" {
		t.Fatalf("expected id=filesystem, got %q", view.ID)
	}
	if view.Source != mcpSourceLocal {
		t.Fatalf("expected source=local, got %q", view.Source)
	}
	if view.SourceURL != "mcp-discovery://cursor/filesystem" {
		t.Fatalf("unexpected source url: %q", view.SourceURL)
	}
	if view.Command != "npx" || len(view.Args) != 3 {
		t.Fatalf("command/args not copied: %+v", view)
	}

	// Re-running discovery should mark the entry as installed.
	report, err := app.DiscoverLocalMcpServers()
	if err != nil {
		t.Fatalf("DiscoverLocalMcpServers: %v", err)
	}
	var matched bool
	for _, srv := range report.Servers {
		if srv.SourceID == "cursor" && srv.Name == "filesystem" {
			matched = true
			if !srv.Installed {
				t.Fatalf("expected filesystem to be marked installed: %+v", srv)
			}
			if srv.InstalledID != "filesystem" {
				t.Fatalf("expected installed_id=filesystem, got %q", srv.InstalledID)
			}
		}
	}
	if !matched {
		t.Fatalf("filesystem entry missing from second discovery run: %+v", report)
	}

	// Importing the same entry again should error rather than create a
	// duplicate row.
	if _, err := app.ImportLocalMcpServer("cursor", "filesystem", false); err == nil {
		t.Fatalf("expected duplicate import to fail")
	}
}
