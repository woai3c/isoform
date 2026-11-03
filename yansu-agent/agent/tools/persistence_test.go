package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"yansu-agent/agent/authority"
)

func TestPersistenceRequiresExplicitApproval(t *testing.T) {
	home, project := hardBlockHome(t)
	commands := []string{
		"printf x >> ~/.zshrc", "touch ~/.zprofile", "rm ~/.bashrc",
		"mkdir -p ~/Library/LaunchAgents", "cp config ~/Library/LaunchAgents/job.plist",
		"printf x > ~/.ssh/config", "printf x > ~/.ssh/authorized_keys", "printf x > ~/.ssh/rc",
		"printf x > ~/.gitconfig", "tee ~/.profile", "ln -s script ~/.zshenv",
		"printf x > " + filepath.Join(home, "other/.git/hooks/pre-commit"),
		"git config --file " + filepath.Join(home, "other/.git/config") + " core.hooksPath /tmp/hooks",
	}
	for _, command := range commands {
		action := NewBashTool().DescribeAuthorityIn(map[string]any{"command": command}, project)
		if action.Metadata[mandatoryPersistenceApproval] != "true" || !action.NoGrant {
			t.Errorf("missing mandatory persistence action: %s: %+v", command, action)
			continue
		}
		for _, unattended := range []bool{false, true} {
			evaluation := authority.NewPolicy(nil).Evaluate(context.Background(), authority.Context{Unattended: unattended}, action)
			ApplyBashApprovalPreference(&evaluation, true)
			want := authority.DecisionRequireApproval
			if unattended {
				want = authority.DecisionDeny
			}
			if evaluation.Decision != want {
				t.Errorf("%s unattended=%v: %s", command, unattended, evaluation.Decision)
			}
		}
	}
	for _, command := range []string{"cat ~/.zshrc", "cat ~/.ssh/config", "cat ~/.gitconfig", "printf x > .git/hooks/pre-commit"} {
		if action := NewBashTool().DescribeAuthorityIn(map[string]any{"command": command}, project); action.Metadata[mandatoryPersistenceApproval] != "" {
			t.Errorf("ordinary read/workspace write requires persistence approval: %s", command)
		}
	}
}

func TestPersistenceFileToolsCannotBypassApproval(t *testing.T) {
	home, _ := hardBlockHome(t)
	path := filepath.Join(home, ".zshrc")
	for _, tool := range []Tool{NewWriteTool(nil), NewEditTool(nil), NewNotebookEditTool(nil)} {
		for _, runtime := range []*AuthorityRuntime{nil, {AllowBashWithoutApproval: true}, {Approved: true, Context: authority.Context{Unattended: true}}, {Approved: true, Context: authority.Context{Mode: authority.ExecutionModeShadow}}} {
			_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"path": path, "content": "bad"}, Authority: runtime})
			var refusal *AuthorityError
			if !errors.As(err, &refusal) {
				t.Errorf("%s bypassed persistence guard: %v", tool.Name(), err)
			}
		}
		if action := DescribeAuthority(tool, map[string]any{"path": path}); action.Metadata[mandatoryPersistenceApproval] != "true" {
			t.Errorf("%s will not request approval in native loop: %+v", tool.Name(), action)
		}
	}
	_, err := NewWriteTool(nil).Execute(context.Background(), ToolCall{Input: map[string]any{"path": path, "content": "approved"}, Authority: &AuthorityRuntime{Approved: true}})
	if err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "approved" {
		t.Fatalf("approved write failed: %q %v", body, err)
	}
}
