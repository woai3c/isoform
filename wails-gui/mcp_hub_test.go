package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// useIsolatedHome points HOME at a per-test temp dir so loadMcpHubState
// and saveMcpHubState read/write under isolation.
func useIsolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	// Reset the install-locks map so cross-test state doesn't leak
	// (the map is package-global and never cleared in production).
	mcpServerInstallLocksMu.Lock()
	mcpServerInstallLocks = map[string]*sync.Mutex{}
	mcpServerInstallLocksMu.Unlock()
	return home
}

func TestMcpHub_LoadEmptyReturnsBlankState(t *testing.T) {
	useIsolatedHome(t)
	state, err := loadMcpHubState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state.Version != mcpHubStateVersion {
		t.Errorf("version = %d, want %d", state.Version, mcpHubStateVersion)
	}
	if len(state.Servers) != 0 {
		t.Errorf("expected empty servers map, got %d entries", len(state.Servers))
	}
}

func TestMcpHub_SaveLoadRoundTrip(t *testing.T) {
	useIsolatedHome(t)
	cfg := McpServerConfig{
		ID:        "filesystem",
		Name:      "Filesystem",
		Source:    mcpSourceManual,
		Transport: mcpTransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		Env:       map[string]string{"DEBUG": "1"},
		Enabled:   true,
		AutoStart: true,
	}
	if err := withMcpHubState(func(s *mcpHubState) error {
		s.Servers[cfg.ID] = cfg
		return nil
	}); err != nil {
		t.Fatalf("withMcpHubState: %v", err)
	}

	state, err := loadMcpHubState()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := state.Servers[cfg.ID]
	if !ok {
		t.Fatalf("server %q missing after reload", cfg.ID)
	}
	if got.Command != cfg.Command || got.Args[0] != "-y" || !got.Enabled {
		t.Errorf("round-trip mismatch: %#v", got)
	}
}

func TestMcpHub_AtomicSaveLeavesNoTmpFiles(t *testing.T) {
	home := useIsolatedHome(t)

	for i := range 5 {
		if err := withMcpHubState(func(s *mcpHubState) error {
			s.Servers["x"] = McpServerConfig{
				ID: "x", Name: "x", Source: mcpSourceManual, Transport: mcpTransportStdio, Command: "echo",
			}
			return nil
		}); err != nil {
			t.Fatalf("save iteration %d: %v", i, err)
		}
	}

	dir := filepath.Join(home, configDirName, "mcp-hub")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "state.json" || name == "servers" {
			continue
		}
		if strings.Contains(name, ".tmp.") {
			t.Errorf("leftover tmp file: %s", name)
		}
	}
}

func TestMcpHub_ConcurrentMutateNoLostWrites(t *testing.T) {
	useIsolatedHome(t)

	const writers = 16
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		id := serverIDForIndex(i)
		go func(serverID string) {
			defer wg.Done()
			err := withMcpHubState(func(s *mcpHubState) error {
				s.Servers[serverID] = McpServerConfig{
					ID:        serverID,
					Name:      serverID,
					Source:    mcpSourceManual,
					Transport: mcpTransportStdio,
					Command:   "echo",
					Enabled:   true,
				}
				return nil
			})
			if err != nil {
				t.Errorf("writer %s: %v", serverID, err)
			}
		}(id)
	}
	wg.Wait()

	state, err := loadMcpHubState()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(state.Servers) != writers {
		t.Errorf("expected %d servers after concurrent writes, got %d", writers, len(state.Servers))
	}
}

func TestMcpHub_AcquireMcpServerLockSerialises(t *testing.T) {
	useIsolatedHome(t)

	var (
		mu        sync.Mutex
		holders   int
		maxHolder int
	)
	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			release := acquireMcpServerLock("same-id")
			defer release()
			mu.Lock()
			holders++
			if holders > maxHolder {
				maxHolder = holders
			}
			mu.Unlock()
			// Fake some work so the race detector has a chance to
			// expose any overlap between holders.
			for range 1000 {
				// burn cycles so the race detector has a chance to
				// expose any overlap between holders
			}
			mu.Lock()
			holders--
			mu.Unlock()
		}()
	}
	wg.Wait()

	if maxHolder != 1 {
		t.Errorf("expected per-id lock to serialise; saw %d concurrent holders", maxHolder)
	}
}

func TestMcpHub_AcquireMcpServerLockIsPerID(t *testing.T) {
	useIsolatedHome(t)

	releaseA := acquireMcpServerLock("alpha")
	defer releaseA()

	// Different id must NOT block. If it did, this test would deadlock
	// (the only way out is if the second Lock returns).
	done := make(chan struct{})
	go func() {
		releaseB := acquireMcpServerLock("beta")
		releaseB()
		close(done)
	}()
	<-done
}

func TestValidateMcpServerConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  McpServerConfig
		want bool // true == valid
	}{
		{"valid stdio", McpServerConfig{ID: "fs", Transport: mcpTransportStdio, Command: "npx"}, true},
		{"valid http", McpServerConfig{ID: "remote", Transport: mcpTransportHTTP, URL: "https://x.io/mcp"}, true},
		{"sse not yet supported", McpServerConfig{ID: "remote", Transport: mcpTransportSSE, URL: "https://x.io/sse"}, false},
		{"http without url", McpServerConfig{ID: "remote", Transport: mcpTransportHTTP}, false},
		{"invalid id empty", McpServerConfig{ID: "", Transport: mcpTransportStdio, Command: "x"}, false},
		{"invalid id underscore", McpServerConfig{ID: "my_server", Transport: mcpTransportStdio, Command: "x"}, false},
		{"invalid id slash", McpServerConfig{ID: "x/y", Transport: mcpTransportStdio, Command: "x"}, false},
		{"stdio without command", McpServerConfig{ID: "fs", Transport: mcpTransportStdio}, false},
		{"unknown transport", McpServerConfig{ID: "fs", Transport: "ws", Command: "x"}, false},
		{"empty secret_env key", McpServerConfig{ID: "fs", Transport: mcpTransportStdio, Command: "x", SecretEnv: []string{""}}, false},
	}
	for _, c := range cases {
		err := validateMcpServerConfig(c.cfg)
		if c.want && err != nil {
			t.Errorf("%s: expected valid, got %v", c.name, err)
		}
		if !c.want && err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestMcpServerIDPattern_RejectsDoubleUnderscoreAndPathSeparators(t *testing.T) {
	bad := []string{"", "with__double", "has space", "..", "a/b", ".hidden", "with_under"}
	for _, s := range bad {
		if mcpServerIDPattern.MatchString(s) {
			t.Errorf("id %q should be rejected", s)
		}
	}
	good := []string{"fs", "my-server", "server-v2", "Filesystem-10"}
	for _, s := range good {
		if !mcpServerIDPattern.MatchString(s) {
			t.Errorf("id %q should be accepted", s)
		}
	}
}

func TestMcpHubServerDir_RejectsBadID(t *testing.T) {
	useIsolatedHome(t)
	if _, err := mcpHubServerDir("bad/id"); err == nil {
		t.Error("expected error for bad id")
	}
	dir, err := mcpHubServerDir("good-id")
	if err != nil {
		t.Fatalf("good id: %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join("mcp-hub", "servers", "good-id")) {
		t.Errorf("unexpected path layout: %s", dir)
	}
}

func TestMcpHubRejectsRetiredManagedComposioIDs(t *testing.T) {
	useIsolatedHome(t)
	app := &App{}
	managedID := composioGoogleSuperServerID

	if err := rejectRetiredManagedMcp(managedID); err == nil || !strings.Contains(err.Error(), "computer_use_only") {
		t.Fatalf("retired managed id error = %v, want computer_use_only", err)
	}
	if _, err := app.AddMcpServerManual(ManualServerInput{
		ID:        managedID,
		Name:      "Legacy Google",
		Transport: mcpTransportHTTP,
		URL:       "https://example.test/mcp",
	}); err == nil {
		t.Fatal("manual add should not reclaim a retired managed id")
	}
	if _, err := app.SetMcpServerTrusted(managedID, true); err == nil {
		t.Fatal("trust should not be granted to a retired managed id")
	}
	if _, err := app.ListMcpServerTools(managedID); err == nil {
		t.Fatal("tools should not be exposed for a retired managed id")
	}
}

func TestNormalizeMcpServerConfig_FillsDefaults(t *testing.T) {
	got := normalizeMcpServerConfig(McpServerConfig{ID: "x"})
	if got.Name != "x" {
		t.Errorf("Name default = %q, want %q", got.Name, "x")
	}
	if got.Source != mcpSourceManual {
		t.Errorf("Source default = %q, want %q", got.Source, mcpSourceManual)
	}
	if got.Transport != mcpTransportStdio {
		t.Errorf("Transport default = %q, want %q", got.Transport, mcpTransportStdio)
	}
	if got.Env == nil {
		t.Error("Env should be initialised to empty map, not nil")
	}
}

// serverIDForIndex returns a deterministic, ID-pattern-compliant slug
// for the i-th writer in the concurrent-mutate test.
func serverIDForIndex(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	if i < len(letters) {
		return string(letters[i])
	}
	return "x" + string(letters[i%len(letters)])
}
