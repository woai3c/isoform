package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"yansu-agent/agent/tools/guard"
)

func TestProgrammaticTool_ExecutesWorkspaceHelpers(t *testing.T) {
	workDir := t.TempDir()
	inputPath := filepath.Join(workDir, "billing.txt")
	outputPath := filepath.Join(workDir, "result.txt")
	if err := os.WriteFile(inputPath, []byte("target-period=83\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	locks := guard.NewFileLockManager(0)
	bash := NewBashTool()
	reg.Register(NewGlobTool())
	reg.Register(NewReadTool())
	reg.Register(NewWriteTool(locks))
	reg.Register(bash)
	reg.Register(NewProgrammaticTool(reg, []string{"Glob", "Read", "Bash", "Write"}))

	code := `
files = glob("*.txt").content
data = read(` + strconv.Quote(inputPath) + `).content
period = bash("printf 83", description="Print target period").content
write(` + strconv.Quote(outputPath) + `, "files=" + files + "\nperiod=" + period + "\n")
result = {"files": files, "data": data, "period": period}
`
	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: workDir,
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code":           code,
			"max_tool_calls": 8,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Programmatic returned is_error=true:\n%s", res.Content)
	}

	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("result file was not written: %v", err)
	}
	if !strings.Contains(string(written), "period=83") {
		t.Fatalf("result file missing period: %q", string(written))
	}

	var payload struct {
		OK    bool `json:"ok"`
		Calls []struct {
			Tool    string `json:"tool"`
			IsError bool   `json:"is_error"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(res.Content), &payload); err != nil {
		t.Fatalf("decode Programmatic result: %v\n%s", err, res.Content)
	}
	if !payload.OK {
		t.Fatalf("payload ok=false:\n%s", res.Content)
	}
	got := map[string]int{}
	for _, call := range payload.Calls {
		got[call.Tool]++
		if call.IsError {
			t.Fatalf("subcall %s unexpectedly is_error=true:\n%s", call.Tool, res.Content)
		}
	}
	for _, name := range []string{"Glob", "Read", "Bash", "Write"} {
		if got[name] != 1 {
			t.Fatalf("subcall count for %s = %d, want 1; calls=%v\n%s", name, got[name], got, res.Content)
		}
	}
}

func TestProgrammaticTool_GrepHelperDelegatesToRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeTool{
		name: "Grep",
		execFn: func(_ context.Context, call ToolCall) (ToolResult, error) {
			if call.Input["pattern"] != "needle" {
				t.Fatalf("pattern = %v, want needle", call.Input["pattern"])
			}
			if call.Input["output_mode"] != "content" {
				t.Fatalf("output_mode = %v, want content", call.Input["output_mode"])
			}
			return ToolResult{Content: "needle: found"}, nil
		},
	})
	reg.Register(NewProgrammaticTool(reg, []string{"Grep"}))

	workDir := t.TempDir()
	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: workDir,
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `hit = grep("needle", path="` + workDir + `", output_mode="content")
result = hit.content`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Programmatic returned is_error=true:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, `"result": "needle: found"`) {
		t.Fatalf("result missing grep content:\n%s", res.Content)
	}
}

func TestProgrammaticTool_WebFetchHelperDelegatesToRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeTool{
		name: "WebFetch",
		execFn: func(_ context.Context, call ToolCall) (ToolResult, error) {
			if call.Input["url"] != "https://example.com/data.json" {
				t.Fatalf("url = %v, want https://example.com/data.json", call.Input["url"])
			}
			return ToolResult{Content: `{"ok":true}`}, nil
		},
	})
	reg.Register(NewProgrammaticTool(reg, []string{"WebFetch"}))

	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `page = web_fetch("https://example.com/data.json")
result = page.content`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Programmatic returned is_error=true:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, `"tool": "WebFetch"`) || !strings.Contains(res.Content, `\"ok\":true`) {
		t.Fatalf("result missing WebFetch subcall:\n%s", res.Content)
	}
}

func TestProgrammaticTool_OpenCLIHelperUsesQuotedBashCommand(t *testing.T) {
	reg := NewRegistry()
	var gotCommand string
	reg.Register(&fakeTool{
		name: "Bash",
		execFn: func(_ context.Context, call ToolCall) (ToolResult, error) {
			gotCommand, _ = call.Input["command"].(string)
			if call.Input["description"] != "Run quote adapter" {
				t.Fatalf("description = %v, want Run quote adapter", call.Input["description"])
			}
			return ToolResult{Content: "adapter output"}, nil
		},
	})
	reg.Register(NewProgrammaticTool(reg, []string{"OpenCLI"}))

	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `out = opencli("yahoo-finance", "quote", "NVDA; rm -rf /", description="Run quote adapter")
result = out.content`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Programmatic returned is_error=true:\n%s", res.Content)
	}
	want := "opencli 'yahoo-finance' 'quote' 'NVDA; rm -rf /'"
	if gotCommand != want {
		t.Fatalf("Bash command = %q, want %q", gotCommand, want)
	}
	if !strings.Contains(res.Content, `"tool": "OpenCLI"`) {
		t.Fatalf("result should record OpenCLI subcall, got:\n%s", res.Content)
	}
}

func TestProgrammaticTool_RecordsHelperFailuresForScriptHandling(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewWebFetchTool())
	reg.Register(NewBashTool())
	reg.Register(NewProgrammaticTool(reg, []string{"WebFetch", "Bash"}))

	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `bad_fetch = web_fetch("http://example.com")
nonzero = bash("exit 7", description="Return nonzero exit")
blocked = bash("rm -rf /", description="Attempt blocked command")
result = {"bad_fetch_error": bad_fetch.is_error, "nonzero_error": nonzero.is_error, "blocked_error": blocked.is_error}`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("script should be able to handle helper failures without aborting:\n%s", res.Content)
	}

	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			BadFetchError bool `json:"bad_fetch_error"`
			NonzeroError  bool `json:"nonzero_error"`
			BlockedError  bool `json:"blocked_error"`
		} `json:"result"`
		Calls []struct {
			Tool    string `json:"tool"`
			IsError bool   `json:"is_error"`
			Content string `json:"content"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(res.Content), &payload); err != nil {
		t.Fatalf("decode Programmatic result: %v\n%s", err, res.Content)
	}
	if !payload.OK || !payload.Result.BadFetchError || !payload.Result.NonzeroError || !payload.Result.BlockedError {
		t.Fatalf("expected script-visible helper errors, got:\n%s", res.Content)
	}
	if len(payload.Calls) != 3 {
		t.Fatalf("helper calls = %d, want 3:\n%s", len(payload.Calls), res.Content)
	}
	for _, call := range payload.Calls {
		if !call.IsError {
			t.Fatalf("expected %s to be recorded as is_error=true:\n%s", call.Tool, res.Content)
		}
	}
}

func TestProgrammaticTool_RefusesPermissionedHelpers(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeTool{
		name:     "Bash",
		needPerm: true,
		execFn: func(_ context.Context, _ ToolCall) (ToolResult, error) {
			t.Fatal("permissioned helper should not execute inside Programmatic")
			return ToolResult{}, nil
		},
	})
	reg.Register(NewProgrammaticTool(reg, []string{"Bash"}))

	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `result = bash("printf nope", description="Should not run").content`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected is_error=true for permissioned helper:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "Bash helper requires permission") {
		t.Fatalf("expected permission refusal, got:\n%s", res.Content)
	}
}

func TestProgrammaticTool_RespectsAllowedHelpers(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewBashTool())
	reg.Register(NewProgrammaticTool(reg, []string{"Read"}))

	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: t.TempDir(),
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"code": `result = bash("printf nope").content`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected is_error=true for disallowed helper:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "undefined: bash") {
		t.Fatalf("expected undefined helper error, got:\n%s", res.Content)
	}
}

func TestProgrammaticTool_EnforcesToolCallLimit(t *testing.T) {
	workDir := t.TempDir()
	inputPath := filepath.Join(workDir, "a.txt")
	if err := os.WriteFile(inputPath, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	reg.Register(NewReadTool())
	reg.Register(NewProgrammaticTool(reg, []string{"Read"}))

	res, err := reg.Execute(context.Background(), "Programmatic", ToolCall{
		WorkDir: workDir,
		ID:      "toolu_programmatic",
		Input: map[string]any{
			"max_tool_calls": 1,
			"code": `first = read(` + strconv.Quote(inputPath) + `).content
second = read(` + strconv.Quote(inputPath) + `).content
result = first + second`,
		},
	})
	if err != nil {
		t.Fatalf("Programmatic Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected is_error=true after helper limit:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "helper call limit reached") {
		t.Fatalf("expected helper limit error, got:\n%s", res.Content)
	}
}
