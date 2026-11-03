package daemon

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/internal/config"
)

type projectPromptAdapter struct{ prompt chan string }

func (*projectPromptAdapter) Listen(context.Context, func(Event)) error { return nil }
func (*projectPromptAdapter) Reply(Event, string)                       {}
func (a *projectPromptAdapter) PostAndStream(_ Event, output <-chan string) {
	var lines []string
	for line := range output {
		lines = append(lines, line)
	}
	a.prompt <- strings.Join(lines, "\n")
}

func TestProjectRunDispatchCarriesContext(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".something"), 0755); err != nil {
		t.Fatal(err)
	}
	// A child test process echoes stdin. This exercises actual dispatch without
	// invoking an assistant or inheriting a signed-in tool configuration.
	t.Setenv("ISOFORM_PROJECT_PROMPT_CHILD", "1")
	adapter := &projectPromptAdapter{prompt: make(chan string, 1)}
	sessions := NewSessionManager()
	defer sessions.Close()
	router := &Router{daemonCfg: &config.DaemonConfig{}, sessions: sessions, adapter: adapter}
	router.runAITaskRoute(context.Background(), Event{UserID: "fixture"}, root, "Fix the project", false, aiTaskRoute{
		command: func(ctx context.Context, _, _ string, _ bool) (*exec.Cmd, error) {
			return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProjectPromptChild$"), nil
		},
		systemPrompt: "Task contract", syncKnowledge: true,
	})
	select {
	case prompt := <-adapter.prompt:
		for _, want := range []string{"# Isoform Project Context", ".something/context.md", ".something/knowledge/_index.md", "Task contract", "Fix the project"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("dispatched project prompt lost %q", want)
			}
		}
	case <-time.After(30 * time.Second):
		t.Fatal("project dispatch did not finish")
	}
}

func TestProjectPromptChild(t *testing.T) {
	if os.Getenv("ISOFORM_PROJECT_PROMPT_CHILD") != "1" {
		t.Skip("subprocess fixture only")
	}
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
