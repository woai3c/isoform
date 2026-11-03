package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"yansu-agent/agent/authority"
)

func TestBashLaunchersAlwaysRequireAttendedApproval(t *testing.T) {
	_, project := hardBlockHome(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	commands := []string{
		"open ./report.html", "open file:///tmp/report.html", "open -a Terminal", "open -a Terminal https://example.com",
		"launchctl submit -l dev.isoform.test -- /bin/true", "launchctl bootstrap gui/501 ./job.plist",
		"env launchctl asuser 501 /bin/true", "sh -c 'launchctl kickstart gui/501/dev.isoform.test'",
		`osascript -e 'do shell script "true"'`, `osascript -e 'tell application "Finder" to activate'`,
		"osascript ./script.scpt", "automator ./flow.workflow", "shortcuts run example", "qlmanage -p ./report.pdf",
		"ssh localhost true", "ssh -V", "printf x | xargs -I{} launchctl submit -l test -- /bin/echo {}",
		"launchctl print gui/501; launchctl submit -l test -- /bin/true",
		"open https://example.com; printf done", "launchctl list; printf done",
		`osascript -e 'return 1'`, `osascript -e 'get volume settings'`, "shortcuts list",
		"scp ./file host:/tmp/file", "sftp host", "sshpass -e ssh host true", "mosh host", "autossh host",
		"docker run alpine true", "docker compose up", "docker ps", "docker build -t x .", "podman create alpine", "nerdctl exec example true",
		"colima start", "lima default true", "orbctl start", "container run alpine true", "crontab -l", "at -l",
	}
	for _, command := range commands {
		action := NewBashTool().DescribeAuthorityIn(map[string]any{"command": command}, project)
		if action.Metadata[mandatoryLauncherApproval] != "true" || !action.NoGrant {
			t.Errorf("launcher was not marked mandatory: %s: %+v", command, action)
			continue
		}
		for _, unattended := range []bool{false, true} {
			for _, off := range []bool{false, true} {
				evaluation := authority.NewPolicy([]authority.Grant{{ID: "broad", Capability: action.Capability}}).Evaluate(context.Background(), authority.Context{Unattended: unattended}, action)
				ApplyBashApprovalPreference(&evaluation, off)
				want := authority.DecisionRequireApproval
				if unattended {
					want = authority.DecisionDeny
				}
				if evaluation.Decision != want {
					t.Errorf("%s unattended=%v off=%v: got %s, want %s", command, unattended, off, evaluation.Decision, want)
				}
			}
		}
		forcedAllow := authority.Evaluation{Decision: authority.DecisionAllow, Action: action}
		ApplyBashApprovalPreference(&forcedAllow, true)
		if forcedAllow.Decision != authority.DecisionRequireApproval {
			t.Errorf("launcher evaluator allow bypassed mandatory approval: %s", command)
		}
	}
}

func TestBashLauncherReadOnlyExceptions(t *testing.T) {
	_, project := hardBlockHome(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	for command, name := range map[string]string{
		"open https://example.com": "open", "open -- http://localhost:8080 https://example.com": "open",
		"launchctl list": "launchctl", "launchctl print gui/501": "launchctl", "launchctl getenv PATH": "launchctl",
	} {
		input := map[string]any{"command": command}
		action := NewBashTool().DescribeAuthorityIn(input, project)
		if action.Metadata[mandatoryLauncherApproval] != "" {
			t.Errorf("read-only launcher needs mandatory approval: %s", command)
		}
		if !bashLauncherExecutions(ToolCall{Input: input, WorkDir: project})[name] {
			t.Errorf("read-only launcher missing sandbox allowance: %s", command)
		}
	}
	for _, command := range []string{`open "$URL"`, "open javascript:alert(1)", "open -a Browser https://example.com", `p=launchctl; "$p" submit -l test -- /bin/true`, `launchctl list; python3 -c 'print(1)'`} {
		if allowed := bashLauncherExecutions(ToolCall{Input: map[string]any{"command": command}, WorkDir: project}); len(allowed) != 0 {
			t.Errorf("unsafe/dynamic launcher has sandbox allowance: %s: %v", command, allowed)
		}
	}
}

func TestBashLauncherApprovalEnablesOnlyNeededExecutables(t *testing.T) {
	_, project := hardBlockHome(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	for _, name := range []string{"ssh", "docker", "podman", "nerdctl", "colima", "lima", "orbctl", "container", "crontab", "at"} {
		call := ToolCall{Input: map[string]any{"command": name + " example"}, WorkDir: project,
			Authority: &AuthorityRuntime{Approved: true}}
		allowed := bashLauncherExecutions(call)
		if len(allowed) != 1 || !allowed[name] {
			t.Fatalf("%s approval broadened to unrelated executables: %v", name, allowed)
		}
		call.Authority.Context.Unattended = true
		if allowed := bashLauncherExecutions(call); len(allowed) != 0 {
			t.Fatalf("unattended %s gained an exec exception: %v", name, allowed)
		}
	}
}

func TestBashDirectLauncherRequiresApprovalRuntime(t *testing.T) {
	_, project := hardBlockHome(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	for _, runtime := range []*AuthorityRuntime{
		nil,
		{AllowBashWithoutApproval: true},
		{AllowBashWithoutApproval: true, Approved: true, Context: authority.Context{Unattended: true}},
		{Approved: true, Context: authority.Context{Mode: authority.ExecutionModeShadow}},
	} {
		_, err := NewBashTool().Execute(context.Background(), ToolCall{Input: map[string]any{"command": "launchctl submit -l dev.isoform.not-created -- /bin/true"}, WorkDir: project, Authority: runtime})
		if _, ok := err.(*AuthorityError); !ok {
			t.Errorf("launcher without attended consent: %v", err)
		}
	}
}

func TestBashLauncherLiteralPATHAliasRequiresApproval(t *testing.T) {
	home, project := hardBlockHome(t)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(project, "transport-client")
	if err := os.WriteFile(renamed, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ssh", "transport-alias"} {
		if err := os.Symlink(renamed, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin:/usr/sbin:/sbin")
	for _, command := range []string{renamed + " localhost", "transport-alias localhost"} {
		action := NewBashTool().DescribeAuthorityIn(map[string]any{"command": command}, project)
		if action.Metadata[mandatoryLauncherApproval] != "true" {
			t.Errorf("PATH-resolved launcher alias missed approval: %s: %+v", command, action)
		}
	}
}
