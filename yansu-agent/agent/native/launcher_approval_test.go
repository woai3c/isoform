package native

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/tools"
)

func TestLauncherApprovalSurvivesRiskySettingOff(t *testing.T) {
	testMandatoryApprovalWithRiskySettingOff(t, "launchctl submit -l dev.isoform.must-not-run -- /bin/true")
}

func TestPersistenceApprovalSurvivesRiskySettingOff(t *testing.T) {
	testMandatoryApprovalWithRiskySettingOff(t, "printf startup > ~/.zshrc")
}

func testMandatoryApprovalWithRiskySettingOff(t *testing.T, command string) {
	t.Helper()
	tempRoot := os.TempDir()
	if runtime.GOOS == "darwin" {
		tempRoot = "/tmp"
	}
	home, err := os.MkdirTemp(tempRoot, "iso-launch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YANSU_AGENT_HOME", "")
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, nested := range []bool{false, true} {
		registry := tools.NewRegistry()
		registry.Register(tools.NewBashTool())
		registry.Register(tools.NewProgrammaticTool(registry, []string{"Bash"}))
		name := "Bash"
		input := map[string]any{"command": command}
		if nested {
			name = "Programmatic"
			input = map[string]any{"code": "result = bash(" + strconv.Quote(command) + ").content"}
		}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		cfg := &LoopConfig{Registry: registry, WorkDir: project, Emit: func(LoopEvent) {},
			AskBeforeRiskyCommands: false, AuthorityEvaluator: authority.NewPolicy(nil)}
		result, outcome, err := executeToolUse(context.Background(), cfg, toolUseReq{ID: "launcher-approval", Name: name, Input: string(body)})
		if err != nil {
			t.Fatal(err)
		}
		if !nested && outcome != toolOutcomePermissionDenied {
			t.Fatalf("direct launcher did not request consent: %s, %+v", outcome, result)
		}
		if !strings.Contains(result.Content.(string), "approval") {
			t.Fatalf("nested=%v: missing mandatory approval refusal: %+v", nested, result)
		}
	}
}
