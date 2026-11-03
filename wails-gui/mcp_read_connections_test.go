package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests use the actual installed assistant CLIs. They only register,
// inspect, and remove an entry in temporary config; they never start an agent
// or the desktop app. The yansu command is the test executable, which get/add
// do not execute.
func TestMCPReadAssistantRegistrationWithRealCLI(t *testing.T) {
	// Each subtest starts the real assistant CLI, so it runs only when asked:
	// YANSU_CLAUDE_INTEGRATION=1 for Claude Code, YANSU_CODEX_INTEGRATION=1
	// for Codex.
	for _, assistant := range []struct{ client, command, optIn string }{
		{"claude-code", "claude", "YANSU_CLAUDE_INTEGRATION"},
		{"codex", "codex", "YANSU_CODEX_INTEGRATION"},
	} {
		t.Run(assistant.client, func(t *testing.T) {
			if os.Getenv(assistant.optIn) != "1" {
				t.Skipf("set %s=1 to run against the real %s CLI", assistant.optIn, assistant.command)
			}
			cli, err := exec.LookPath(assistant.command)
			if err != nil {
				t.Skipf("%s not on PATH", assistant.command)
			}
			app := newMCPReadTestApp(t)
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			for key, name := range map[string]string{
				"CLAUDE_CONFIG_DIR": "claude", "CODEX_HOME": "codex",
				"XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache",
				"XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "TMPDIR": "tmp",
			} {
				dir := filepath.Join(home, name)
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv(key, dir)
			}
			t.Setenv("DISABLE_AUTOUPDATER", "1")
			t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
			t.Chdir(home)
			bin := filepath.Join(home, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			yansu := filepath.Join(bin, bundledYansuCLIExecutableName())
			if err := os.Symlink(executable, yansu); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			newMCPReadHTTPTestServer(t, app)
			runCLI := func(args ...string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, cli, args...)
				cmd.Dir = home
				cmd.WaitDelay = time.Second
				if err := cmd.Run(); err != nil {
					t.Fatalf("temporary assistant configuration command failed: %v", err)
				}
			}

			rowForClient := func() MCPReadConnection {
				t.Helper()
				rows, err := app.GetMCPReadConnections()
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					if row.Client == assistant.client {
						return row
					}
				}
				t.Fatal("assistant missing from connection settings")
				return MCPReadConnection{}
			}
			before := rowForClient()
			if !before.Available || before.Connected || before.Authorized {
				t.Fatalf("unexpected initial connection: %+v", before)
			}
			if !strings.Contains(before.Command, yansu) || !strings.Contains(before.Command, configDirOrFallback()) {
				t.Fatalf("registration command does not describe the local stdio executable: %s", before.Command)
			}
			connected, err := app.ConnectMCPReadAssistant(assistant.client)
			if err != nil {
				t.Fatal(err)
			}
			if !connected.Connected || !connected.Authorized || !connected.Available {
				t.Fatalf("registration not connected: %+v", connected)
			}
			if got := rowForClient(); !got.Connected || got.Command != before.Command {
				t.Fatalf("connection not reflected by the real CLI config: %+v", got)
			}
			token, err := readMCPReadToken(assistant.client)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := app.ConnectMCPReadAssistant(assistant.client); err != nil {
				t.Fatalf("repeat Connect must be idempotent: %v", err)
			}
			if repeated, err := readMCPReadToken(assistant.client); err != nil || repeated != token {
				t.Fatal("repeat Connect unexpectedly changed the existing grant")
			}

			// External assistant config changes must be reflected even while the
			// app still holds a grant, so Settings can always offer Revoke.
			removeArgs := []string{"mcp", "remove", "isoform"}
			if assistant.client == "claude-code" {
				removeArgs = append(removeArgs, "--scope", "user")
			}
			runCLI(removeArgs...)
			if got := rowForClient(); got.Connected || !got.Authorized {
				t.Fatalf("external removal was not reflected: %+v", got)
			}
			if connected, err := app.ConnectMCPReadAssistant(assistant.client); err != nil || !connected.Connected {
				t.Fatalf("reconnect after external removal: %+v, %v", connected, err)
			}

			revoked, err := app.RevokeMCPReadAssistant(assistant.client)
			if err != nil {
				t.Fatal(err)
			}
			if revoked.Connected || revoked.Authorized {
				t.Fatalf("grant still active after revocation: %+v", revoked)
			}
			path, err := mcpReadTokenPath(assistant.client)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("revoked token file still exists: %v", err)
			}
			if mcpReadAssistantMatches(assistant.client, cli, yansu) {
				t.Fatal("revoke left the assistant registration behind")
			}
			if got := rowForClient(); got.Connected || got.Authorized {
				t.Fatalf("revocation not reflected by Settings: %+v", got)
			}
			if _, err := app.ConnectMCPReadAssistant(assistant.client); err != nil {
				t.Fatalf("reconnect after revocation: %v", err)
			}
			if rotated, err := readMCPReadToken(assistant.client); err != nil || rotated == token {
				t.Fatal("reconnect after revocation must issue a fresh grant")
			}
			if assistant.client == "claude-code" {
				// A moved app and a different Isoform data directory both leave
				// recognizable registrations that Connect may replace on click.
				for _, stale := range []struct{ command, data string }{
					{filepath.Join(home, "old-install", "yansu"), configDirOrFallback()},
					{yansu, filepath.Join(home, "other-install-data")},
				} {
					runCLI(removeArgs...)
					runCLI("mcp", "add", "--transport", "stdio", "--scope", "user", "isoform", "--", stale.command, "mcp", "serve", "--client", "claude-code", "--data-dir", stale.data)
					configPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
					beforeView, err := os.ReadFile(configPath)
					if err != nil {
						t.Fatal(err)
					}
					if row := rowForClient(); row.Connected || row.Issue != "" {
						t.Fatalf("stale Isoform registration not recognized: %+v", row)
					}
					afterView, err := os.ReadFile(configPath)
					if err != nil || !bytes.Equal(beforeView, afterView) {
						t.Fatal("viewing stale connection changed assistant config")
					}
					if row, err := app.ConnectMCPReadAssistant(assistant.client); err != nil || !row.Connected {
						t.Fatalf("replace stale Isoform registration: %+v, %v", row, err)
					}
				}

				// An unrelated server with the fixed name is never overwritten,
				// and its arguments do not cross the Settings binding boundary.
				if _, err := app.RevokeMCPReadAssistant(assistant.client); err != nil {
					t.Fatal(err)
				}
				const secret = "do-not-disclose-test-credential"
				runCLI("mcp", "add", "--transport", "stdio", "--scope", "user", "isoform", "--", "/usr/bin/env", "--token", secret)
				configPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
				beforeConflict, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := app.ConnectMCPReadAssistant(assistant.client); err == nil || err.Error() != "assistant MCP server name conflict" {
					t.Fatalf("expected distinct name conflict, got %v", err)
				}
				row := rowForClient()
				if row.Issue != "name_conflict" || row.ConflictCommand != "env" || row.Authorized || row.Connected {
					t.Fatalf("unsafe conflict state: %+v", row)
				}
				encoded, err := json.Marshal(row)
				if err != nil || strings.Contains(string(encoded), secret) {
					t.Fatal("conflict status disclosed command arguments")
				}
				afterConflict, err := os.ReadFile(configPath)
				if err != nil || !bytes.Equal(beforeConflict, afterConflict) {
					t.Fatal("refused conflict changed the assistant config")
				}
				if _, err := readMCPReadToken(assistant.client); !os.IsNotExist(err) {
					t.Fatalf("conflict issued an access grant: %v", err)
				}
			}
		})
	}
}

func TestMCPReadCLIResolutionIgnoresWorkingDirectory(t *testing.T) {
	home := isolatedYansuCLIHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	t.Chdir(home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	link := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(executable, path); err != nil {
			t.Fatal(err)
		}
	}
	link(filepath.Join(home, yansuCLIBundleDirName, "bin", bundledYansuCLIExecutableName()))
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got := mcpReadCLIPath(); got != "" {
		t.Fatalf("untrusted working-directory bundle selected: %s", got)
	}
	if row, err := mcpReadConnection("codex"); err != nil || row.AvailabilityReason != "assistant_missing" {
		t.Fatalf("assistant availability reason: %+v, %v", row, err)
	}
	// This tests executable discovery only. No assistant process is started.
	link(filepath.Join(bin, "codex"))
	if row, err := mcpReadConnection("codex"); err != nil || row.Available || row.AvailabilityReason != "isoform_missing" {
		t.Fatalf("Isoform availability reason: %+v, %v", row, err)
	}
	fallback := filepath.Join(bin, bundledYansuCLIExecutableName())
	link(fallback)
	if got := mcpReadCLIPath(); got != fallback {
		t.Fatalf("PATH fallback: got %s, want %s", got, fallback)
	}
	dataRoot, err := flavorDataDir()
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(dataRoot, yansuCLIInstallRoot, bundledYansuCLIExecutableName())
	link(installed)
	if got := mcpReadCLIPath(); got != installed {
		t.Fatalf("installed CLI priority: got %s, want %s", got, installed)
	}
}

func TestMCPReadClaudeConflictIsConservative(t *testing.T) {
	args := []string{"mcp", "serve", "--client", "claude-code"}
	for _, entry := range []rawMcpServerEntry{
		{Command: "/bin/echo", Args: args},
		{Command: "yansu", Args: append(append([]string{}, args...), "--unexpected", "credential")},
		{Command: "yansu", Args: args, Type: "http"},
		{Command: "yansu", Args: args, URL: "https://example.invalid/?token=credential"},
	} {
		if mcpReadIsIsoformClaudeEntry(entry) {
			t.Fatalf("unrelated entry marked replaceable: %s", entry.Command)
		}
	}
	if got := mcpReadConflictCommand(rawMcpServerEntry{Command: "node --token=credential", Args: []string{"secret"}, Env: map[string]string{"TOKEN": "secret"}}); got != "" {
		t.Fatalf("malformed executable exposed: %s", got)
	}
}

func TestMCPReadCLIRejectsRelativePATHResults(t *testing.T) {
	home := isolatedYansuCLIHome(t)
	project := filepath.Join(home, "project")
	safeBin := filepath.Join(home, "safe-bin")
	if err := os.Mkdir(safeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{project, filepath.Join(project, "bin"), filepath.Join(home, "adjacent-bin")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		// Real executable permissions exercise LookPath without ever running
		// this inert working-directory payload or any assistant command.
		if err := os.WriteFile(filepath.Join(dir, bundledYansuCLIExecutableName()), []byte("untrusted PATH payload"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(project)
	for _, input := range []struct {
		name, path, godebug string
	}{
		{"dot", ".", "execerrdot=1"},
		{"empty-entry", string(os.PathListSeparator) + safeBin, "execerrdot=1"},
		{"relative-bin", "bin", "execerrdot=1"},
		{"relative-parent", filepath.Join("..", "adjacent-bin"), "execerrdot=1"},
		{"empty-path", "", "execerrdot=1"},
		{"dot-with-errdot-disabled", ".", "execerrdot=0"},
		{"relative-with-errdot-disabled", "bin", "execerrdot=0"},
	} {
		t.Run(input.name, func(t *testing.T) {
			t.Setenv("PATH", input.path)
			t.Setenv("GODEBUG", input.godebug)
			path, err := exec.LookPath("yansu")
			if input.path != "" && path == "" {
				t.Fatalf("fixture did not expose a PATH candidate: %v", err)
			}
			if got := mcpReadCLIPath(); got != "" {
				t.Fatalf("unsafe CLI resolution = %q; LookPath returned %q, %v", got, path, err)
			}
		})
	}
}

func TestMCPReadAssistantRejectsRelativePATHResults(t *testing.T) {
	home := isolatedYansuCLIHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	project := filepath.Join(home, "project")
	safeBin := filepath.Join(home, "safe-bin")
	if err := os.Mkdir(safeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(safeBin, bundledYansuCLIExecutableName()), []byte("unused absolute Isoform command"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{project, filepath.Join(project, "bin"), filepath.Join(home, "adjacent-bin")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{"claude", "codex"} {
			// Executable permissions exercise real PATH resolution. These inert
			// bytes cannot start an assistant, including on a failing regression.
			if err := os.WriteFile(filepath.Join(dir, command), []byte("untrusted assistant PATH payload"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Chdir(project)
	for _, client := range []string{"claude-code", "codex"} {
		mcpReadCredentialMu.Lock()
		_, err := issueMCPReadToken(client)
		mcpReadCredentialMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []struct {
		name, path, godebug string
	}{
		{"dot", ".", "execerrdot=1"},
		{"empty-entry", "", "execerrdot=1"},
		{"relative-bin", "bin", "execerrdot=1"},
		{"relative-parent", filepath.Join("..", "adjacent-bin"), "execerrdot=1"},
		{"dot-with-errdot-disabled", ".", "execerrdot=0"},
		{"relative-with-errdot-disabled", "bin", "execerrdot=0"},
	} {
		t.Run(input.name, func(t *testing.T) {
			t.Setenv("PATH", input.path+string(os.PathListSeparator)+safeBin)
			t.Setenv("GODEBUG", input.godebug)
			for _, client := range []string{"claude-code", "codex"} {
				command, err := mcpReadClientCommand(client)
				if err != nil {
					t.Fatal(err)
				}
				if candidate, err := exec.LookPath(command); candidate == "" {
					t.Fatalf("fixture did not expose an assistant PATH candidate: %v", err)
				}
				row, err := mcpReadConnection(client)
				if err != nil {
					t.Fatal(err)
				}
				if row.Available || row.Connected || !row.Authorized || row.AvailabilityReason != "assistant_missing" {
					t.Fatalf("relative assistant considered available: %+v", row)
				}
				if _, err := (&App{}).ConnectMCPReadAssistant(client); err == nil || err.Error() != "assistant CLI unavailable" {
					t.Fatalf("relative assistant reached registration: %v", err)
				}
			}
		})
	}
}
