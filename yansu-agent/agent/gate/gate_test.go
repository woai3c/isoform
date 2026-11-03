package gate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yansu-agent/agent/authority"
	"yansu-agent/agent/native"
	"yansu-agent/agent/tools"
	"yansu-agent/agent/tools/guard"
)

// These are real native tools and the real native gate on both paths. In
// particular the legacy nil-evaluator case differs from a run with policy.
func TestNativeAndNamespacedGateParity(t *testing.T) {
	work := realTempDir(t)
	data := realTempDir(t)
	t.Setenv("YANSU_AGENT_HOME", data)
	file := filepath.Join(work, "read.txt")
	if err := os.WriteFile(file, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool())
	registry.Register(tools.NewWriteTool(guard.NewFileLockManager(0)))
	registry.Register(tools.NewBashTool())
	base := func() *native.LoopConfig {
		return &native.LoopConfig{Registry: registry, WorkDir: work, RunID: "parity", SessionID: "parity", Emit: func(native.LoopEvent) {}}
	}
	for _, tc := range []struct {
		name      string
		tool      string
		input     map[string]any
		configure func(*native.LoopConfig)
		denied    bool
	}{
		{"legacy unattended without evaluator", "Read", map[string]any{"path": file}, func(c *native.LoopConfig) { c.AutoApproveTools = true }, false},
		{"unattended without grant", "Read", map[string]any{"path": file}, func(c *native.LoopConfig) { c.AutoApproveTools = true; c.AuthorityEvaluator = authority.NewPolicy(nil) }, true},
		{"unattended scoped read", "Read", map[string]any{"path": file}, func(c *native.LoopConfig) {
			c.AutoApproveTools = true
			c.AuthorityEvaluator = authority.NewPolicy([]authority.Grant{{ID: "read", Capability: "tool.read", ResourcePrefix: work}})
		}, false},
		{"protected read", "Read", map[string]any{"path": filepath.Join(data, "secret")}, func(c *native.LoopConfig) {}, true},
		{"denied Bash prefix", "Bash", map[string]any{"command": "git push origin main"}, func(c *native.LoopConfig) { c.AutoApproveTools = true; c.DeniedBashPrefixes = []string{"git push"} }, true},
		{"outside write grant", "Write", map[string]any{"path": filepath.Join(realTempDir(t), "write.txt"), "content": "no"}, func(c *native.LoopConfig) {
			c.AutoApproveTools = true
			c.AuthorityEvaluator = authority.NewPolicy([]authority.Grant{{ID: "write", Capability: "tool.write", ResourcePrefix: work}})
		}, true},
		{"missing approval channel", "Bash", map[string]any{"command": "some_unrecognized_command --help"}, func(c *native.LoopConfig) {
			c.AuthorityEvaluator = authority.NewPolicy(nil)
			c.AskBeforeRiskyCommands = true
		}, true},
		{"unattended no approval for unknown command", "Bash", map[string]any{"command": "some_unrecognized_command --help"}, func(c *native.LoopConfig) { c.AutoApproveTools = true; c.AuthorityEvaluator = authority.NewPolicy(nil) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.configure(cfg)
			nativeResult, nativeErr := native.DispatchTool(context.Background(), cfg, tc.tool, "native", tc.input)
			gateResult, gateErr := (&Gate{Config: cfg}).Execute(context.Background(), ToolPrefix+tc.tool, "external", tc.input, nil)
			if (nativeErr != nil) != (gateErr != nil) || nativeResult.IsError != gateResult.IsError || gateResult.IsError != tc.denied {
				t.Fatalf("native=(%+v,%v), gate=(%+v,%v), denied=%v", nativeResult, nativeErr, gateResult, gateErr, tc.denied)
			}
		})
	}
}

func TestToolFiltersAreExecutionBoundaries(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool())
	for _, cfg := range []*native.LoopConfig{
		{Registry: registry, NoTools: true},
		{Registry: registry, AllowedTools: []string{"Write"}},
		{Registry: registry, DisabledTools: []string{"Read"}},
	} {
		called := false
		result, err := (&Gate{Config: cfg}).Execute(context.Background(), ToolPrefix+"Read", "read", map[string]any{"path": "/tmp/no"}, func(string, tools.ToolCall) error { called = true; return nil })
		if err != nil || !result.IsError || called {
			t.Fatalf("filter bypass: result=%+v err=%v started=%v", result, err, called)
		}
	}
}

func TestPlanModeLimitsServerTools(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool())
	registry.Register(tools.NewWriteTool(guard.NewFileLockManager(0)))
	g := &Gate{Config: &native.LoopConfig{Registry: registry, PlanMode: tools.NewPlanModeState(true)}}
	if available := g.Tools(); len(available) != 1 || available[0].Name() != "Read" {
		t.Fatalf("plan tool list: %+v", available)
	}
	started := false
	result, err := g.Execute(context.Background(), ToolPrefix+"Write", "write", map[string]any{"path": filepath.Join(realTempDir(t), "no"), "content": "no"}, func(string, tools.ToolCall) error { started = true; return nil })
	if err != nil || !result.IsError || started {
		t.Fatalf("plan write passed: %+v %v started=%v", result, err, started)
	}
}

func TestGatePreservesBashFailureAccounting(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool())
	var event native.LoopEvent
	cfg := &native.LoopConfig{Registry: registry, WorkDir: realTempDir(t), Emit: func(e native.LoopEvent) {
		if e.Type == "tool_result" {
			event = e
		}
	}}
	result, err := (&Gate{Config: cfg}).Execute(context.Background(), ToolPrefix+"Bash", "exit", map[string]any{"command": "exit 7"}, nil)
	if err != nil || !result.IsError || !event.Failed || event.ToolSucceeded {
		t.Fatalf("shell exit failure lost: result=%+v event=%+v err=%v", result, event, err)
	}
}

func TestAttendedRiskyCommandSettingAndApprovalCancellation(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool())
	for _, ask := range []bool{false, true} {
		cfg := &native.LoopConfig{Registry: registry, WorkDir: realTempDir(t), AuthorityEvaluator: authority.NewPolicy(nil), AskBeforeRiskyCommands: ask, Emit: func(native.LoopEvent) {}}
		started := false
		_, _ = (&Gate{Config: cfg}).Execute(context.Background(), ToolPrefix+"Bash", "risky", map[string]any{"command": "isoform_command_that_does_not_exist"}, func(string, tools.ToolCall) error { started = true; return nil })
		if started == ask {
			t.Fatalf("ask=%v dispatched=%v", ask, started)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cfg := &native.LoopConfig{Registry: registry, WorkDir: realTempDir(t), SessionID: "cancel", AuthorityEvaluator: authority.NewPolicy(nil), AskBeforeRiskyCommands: true, Permission: native.NewPermissionManager(), Emit: func(e native.LoopEvent) {
		if e.Type == "permission_request" {
			cancel()
		}
	}}
	started := false
	_, err := (&Gate{Config: cfg}).Execute(ctx, ToolPrefix+"Bash", "cancel", map[string]any{"command": "isoform_command_that_does_not_exist"}, func(string, tools.ToolCall) error { started = true; return nil })
	if !errors.Is(err, context.Canceled) || started {
		t.Fatalf("cancel did not stop approval: err=%v dispatched=%v", err, started)
	}
}

func TestReceiptFailureStopsNativeDispatch(t *testing.T) {
	work := realTempDir(t)
	path := filepath.Join(work, "not-written")
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteTool(guard.NewFileLockManager(0)))
	errReceipt := errors.New("could not persist action receipt")
	cfg := &native.LoopConfig{Registry: registry, WorkDir: work, Emit: func(native.LoopEvent) {}, BeforeToolExecution: func(string, tools.ToolCall) error { return errReceipt }}
	result, err := native.DispatchTool(context.Background(), cfg, "Write", "write", map[string]any{"path": path, "content": "no"})
	if !errors.Is(err, errReceipt) || !result.IsError {
		t.Fatalf("receipt failure was not returned: %+v %v", result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("tool executed without durable receipt: %v", err)
	}
}

type namedTool struct {
	tools.Tool
	name string
}

func (t namedTool) Name() string { return t.name }

func TestUserServerIsoformNamespaceDoesNotAliasBuiltin(t *testing.T) {
	path := filepath.Join(realTempDir(t), "input")
	if err := os.WriteFile(path, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool())
	registry.Register(namedTool{Tool: tools.NewReadTool(), name: ToolPrefix + "Read"})
	g := &Gate{Config: &native.LoopConfig{Registry: registry}}
	dispatched := ""
	result, err := g.Execute(context.Background(), ToolPrefix+ToolPrefix+"Read", "namespaced", map[string]any{"path": path}, func(name string, _ tools.ToolCall) error { dispatched = name; return nil })
	if err != nil || result.IsError || dispatched != ToolPrefix+"Read" {
		t.Fatalf("namespace collision: result=%+v err=%v tool=%q", result, err, dispatched)
	}
	g.Config.DisabledTools = []string{ToolPrefix + "Read"}
	available := g.Tools()
	if len(available) != 1 || available[0].Name() != "Read" {
		t.Fatalf("disabled imported tool changed builtin: %+v", available)
	}
}
