//go:build darwin

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/agent/authority"
)

func TestProgrammaticBashUsesSandboxWithRiskyPromptOff(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "iso-nsb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("YANSU_AGENT_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "NESTED_SANDBOX_PRIVATE_KEY_SENTINEL"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(NewBashTool())
	reg.Register(NewProgrammaticTool(reg, []string{"Bash"}))
	result, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: home, ID: "nested-sandbox",
		Input:     map[string]any{"code": `result = bash("p=$HOME; s=.ss; cat \"$p/${s}h/id_rsa\"").content`},
		Authority: &AuthorityRuntime{Evaluator: authority.NewPolicy(nil), AllowBashWithoutApproval: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Content, secret) {
		t.Fatalf("nested Bash printed a private key: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Operation not permitted") {
		t.Fatalf("expected a sandbox refusal from nested Bash: %s", result.Content)
	}
}
