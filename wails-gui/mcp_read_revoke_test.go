package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mcpReadRevokeFixture(t *testing.T, client string) (string, map[string]any) {
	t.Helper()
	home := isolatedYansuCLIHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	command, err := mcpReadClientCommand(client)
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(bin, command)
	// Inert executable bytes satisfy discovery, even if a regression bypasses
	// the runner seam. No real assistant binary is reachable on this PATH.
	if err := os.WriteFile(cli, []byte("inert assistant fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	mcpReadIssueTestToken(t, client)
	return cli, map[string]any{
		"type": "stdio", "command": filepath.Join(home, "old-install", "yansu"),
		"args": mcpReadServeArgs(client),
	}
}

func mcpReadSetCLIRunner(t *testing.T, runner func(context.Context, string, ...string) ([]byte, error)) {
	t.Helper()
	previous := mcpReadRunCLI
	mcpReadRunCLI = runner
	t.Cleanup(func() { mcpReadRunCLI = previous })
}

func mcpReadAssertRevoked(t *testing.T, client string, row MCPReadConnection) {
	t.Helper()
	if row.Client != client || row.Authorized || row.Connected {
		t.Fatalf("incorrect revoked result: %+v", row)
	}
	path, err := mcpReadTokenPath(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token not deleted: %v", err)
	}
}

func TestMCPReadRevokeOnlyRemovesIsoformRegistration(t *testing.T) {
	for _, client := range []string{"claude-code", "codex"} {
		for _, scenario := range []struct {
			name   string
			change func(map[string]any)
			remove bool
		}{
			{"registered", func(map[string]any) {}, true},
			{"disabled", func(e map[string]any) { e["disabled"] = true }, true},
			{"other-name", func(e map[string]any) { e["name"] = "isoform-other" }, false},
			{"other-command", func(e map[string]any) { e["command"] = "/usr/bin/env" }, false},
			{"relative-command", func(e map[string]any) { e["command"] = "yansu" }, false},
			{"other-client", func(e map[string]any) { e["args"] = mcpReadServeArgs("other") }, false},
			{"other-data-root", func(e map[string]any) {
				args := mcpReadServeArgs(client)
				args[5] = filepath.Join(t.TempDir(), "other-data")
				e["args"] = args
			}, false},
			{"extra-argument", func(e map[string]any) { e["args"] = append(mcpReadServeArgs(client), "--extra") }, false},
			{"missing-data-root", func(e map[string]any) { e["args"] = mcpReadServeArgs(client)[:4] }, false},
			{"remote-type", func(e map[string]any) { e["type"] = "http" }, false},
			{"remote-url", func(e map[string]any) { e["url"] = "https://example.invalid/secret" }, false},
			{"environment", func(e map[string]any) { e["env"] = map[string]string{"TOKEN": "secret"} }, false},
			{"nested-transport", func(e map[string]any) { e["transport"] = map[string]string{"type": "http"} }, false},
		} {
			t.Run(client+"/"+scenario.name, func(t *testing.T) {
				cli, entry := mcpReadRevokeFixture(t, client)
				scenario.change(entry)
				name := "isoform"
				if changed, ok := entry["name"].(string); ok {
					name = changed
					delete(entry, "name")
				}
				config := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
				// The unrelated entry must survive every cleanup, even if its
				// command has exactly the same Isoform shape.
				entries := map[string]any{name: entry, "unrelated": entry}
				data, err := json.Marshal(map[string]any{"mcpServers": entries})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, data, 0o600); err != nil {
					t.Fatal(err)
				}
				removed := 0
				mcpReadSetCLIRunner(t, func(ctx context.Context, gotCLI string, args ...string) ([]byte, error) {
					if gotCLI != cli {
						t.Fatalf("wrong CLI: %s", gotCLI)
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("cleanup command has no timeout")
					}
					// Authority must already be gone before any subprocess runs.
					if _, err := readMCPReadToken(client); !os.IsNotExist(err) {
						t.Fatalf("CLI reached before token deletion: %v", err)
					}
					if client == "codex" && slices.Equal(args, []string{"mcp", "list", "--json"}) {
						listed := []any{map[string]any{"name": "unrelated", "transport": entry}}
						if _, exists := entries[name]; exists {
							listed = append(listed, map[string]any{"name": name, "enabled": entry["disabled"] != true, "transport": entry})
						}
						return json.Marshal(listed)
					}
					want := []string{"mcp", "remove", "isoform"}
					if client == "claude-code" {
						want = append(want, "--scope", "user")
					}
					if !scenario.remove || !slices.Equal(args, want) {
						t.Fatalf("unsafe cleanup command: %v", args)
					}
					removed++
					delete(entries, "isoform")
					updated, _ := json.Marshal(map[string]any{"mcpServers": entries})
					return nil, os.WriteFile(config, updated, 0o600)
				})
				otherClient := "codex"
				if client == "codex" {
					otherClient = "claude-code"
				}
				otherToken := mcpReadIssueTestToken(t, otherClient)
				for range 2 {
					row, err := (&App{}).RevokeMCPReadAssistant(client)
					if err != nil {
						t.Fatal(err)
					}
					mcpReadAssertRevoked(t, client, row)
				}
				wantRemoves := 0
				if scenario.remove {
					wantRemoves = 1
				}
				if removed != wantRemoves {
					t.Fatalf("remove calls = %d, want %d", removed, wantRemoves)
				}
				if got, err := readMCPReadToken(otherClient); err != nil || got != otherToken {
					t.Fatal("revoke changed the other assistant's token")
				}
				after, err := os.ReadFile(config)
				if err != nil {
					t.Fatal(err)
				}
				if !scenario.remove && string(after) != string(data) {
					t.Fatal("unrelated configuration was changed")
				}
				if _, exists := entries["unrelated"]; !exists {
					t.Fatal("unrelated registration removed")
				}
			})
		}
	}
}

func TestMCPReadRevokeReportsCleanupFailureAfterDeletingToken(t *testing.T) {
	for _, client := range []string{"claude-code", "codex"} {
		t.Run(client, func(t *testing.T) {
			_, entry := mcpReadRevokeFixture(t, client)
			data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"isoform": entry}})
			if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			removes := 0
			mcpReadSetCLIRunner(t, func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if slices.Equal(args, []string{"mcp", "list", "--json"}) {
					return json.Marshal([]any{map[string]any{"name": "isoform", "transport": entry}})
				}
				removes++
				if _, err := readMCPReadToken(client); !os.IsNotExist(err) {
					t.Fatalf("remove reached before token deletion: %v", err)
				}
				return []byte("secret-output"), errors.New("secret-error")
			})
			row, err := (&App{}).RevokeMCPReadAssistant(client)
			if err == nil || err.Error() != "access revoked; assistant MCP registration could not be removed" || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe or missing cleanup error: %v", err)
			}
			mcpReadAssertRevoked(t, client, row)
			if removes != 1 {
				t.Fatalf("remove calls = %d, want 1", removes)
			}
		})
	}
}

func TestMCPReadRevokeCodexInspection(t *testing.T) {
	for _, scenario := range []struct {
		name, output string
		err          error
		wantError    bool
	}{
		{"absent", "[]", nil, false},
		{"failed", "secret-output", errors.New("secret-error"), true},
		{"malformed", "secret-output", nil, true},
		{"null", "null", nil, true},
		{"wrong-shape", "{}", nil, true},
		{"oversized", strings.Repeat(" ", (1<<20)+1), nil, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cli, _ := mcpReadRevokeFixture(t, "codex")
			calls := 0
			mcpReadSetCLIRunner(t, func(_ context.Context, gotCLI string, args ...string) ([]byte, error) {
				calls++
				if gotCLI != cli || !slices.Equal(args, []string{"mcp", "list", "--json"}) {
					t.Fatalf("unexpected command: %s %v", gotCLI, args)
				}
				return []byte(scenario.output), scenario.err
			})
			row, err := (&App{}).RevokeMCPReadAssistant("codex")
			if (err != nil) != scenario.wantError || (err != nil && err.Error() != "access revoked; assistant MCP configuration could not be read") {
				t.Fatalf("inspection error: %v", err)
			}
			mcpReadAssertRevoked(t, "codex", row)
			if calls != 1 {
				t.Fatalf("inspection calls = %d, want 1", calls)
			}
		})
	}
}

func TestMCPReadRevokeCodexPreservesCustomLaunchSettings(t *testing.T) {
	for _, field := range []string{"env_vars", "cwd"} {
		t.Run(field, func(t *testing.T) {
			_, entry := mcpReadRevokeFixture(t, "codex")
			entry[field] = "/custom"
			if field == "env_vars" {
				entry[field] = []string{"TOKEN"}
			}
			mcpReadSetCLIRunner(t, func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if !slices.Equal(args, []string{"mcp", "list", "--json"}) {
					t.Fatalf("custom entry removed: %v", args)
				}
				return json.Marshal([]any{map[string]any{"name": "isoform", "transport": entry}})
			})
			row, err := (&App{}).RevokeMCPReadAssistant("codex")
			if err != nil {
				t.Fatal(err)
			}
			mcpReadAssertRevoked(t, "codex", row)
		})
	}
}

func TestMCPReadRevokeClaudeInspectionFailure(t *testing.T) {
	_, _ = mcpReadRevokeFixture(t, "claude-code")
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), []byte("secret-malformed-config"), 0o600); err != nil {
		t.Fatal(err)
	}
	mcpReadSetCLIRunner(t, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unreadable configuration reached CLI cleanup")
		return nil, nil
	})
	row, err := (&App{}).RevokeMCPReadAssistant("claude-code")
	if err == nil || err.Error() != "access revoked; assistant MCP configuration could not be read" {
		t.Fatalf("inspection error: %v", err)
	}
	mcpReadAssertRevoked(t, "claude-code", row)
}

func TestMCPReadRevokeStopsWhenTokenDeletionFails(t *testing.T) {
	_, _ = mcpReadRevokeFixture(t, "codex")
	path, _ := mcpReadTokenPath("codex")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mcpReadSetCLIRunner(t, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("failed token deletion reached CLI cleanup")
		return nil, nil
	})
	if _, err := (&App{}).RevokeMCPReadAssistant("codex"); err == nil {
		t.Fatal("token deletion failure was not reported")
	}
}
