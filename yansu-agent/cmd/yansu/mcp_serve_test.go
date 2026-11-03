package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestMCPServeCommandRequiresScopedExplicitDataDirectory(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--client", "codex"},
		{"--data-dir", t.TempDir()},
		{"--client", "other", "--data-dir", t.TempDir()},
		{"--client", "codex", "--data-dir", "relative"},
	} {
		cmd := newMCPServeCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted incomplete or unsafe args: %v", args)
		}
	}
}

func TestMCPServeCommandInitializesWithoutOpeningAppData(t *testing.T) {
	cmd := newMCPServeCommand()
	cmd.SetArgs([]string{"--client", "claude-code", "--data-dir", t.TempDir()})
	cmd.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"cli-test","version":"1"}}}` + "\n"))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Result.ServerInfo.Name != "Isoform" {
		t.Fatalf("stdout is not a clean MCP initialize response: %q, %v", output.String(), err)
	}
	registered, _, err := mcpCmd.Find([]string{"serve"})
	if err != nil || registered.Name() != "serve" {
		t.Fatalf("mcp serve is not registered: %v", err)
	}
}
