package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

const (
	programmaticDefaultMaxToolCalls = 20
	programmaticHardMaxToolCalls    = 50
	programmaticMaxCodeBytes        = 32 * 1024
	programmaticMaxResultBytes      = 64 * 1024
	programmaticSubcallContentBytes = 12 * 1024
	programmaticMaxExecutionSteps   = 200_000
	programmaticTimeout             = 2 * time.Minute
)

// ProgrammaticTool runs a bounded Starlark script with helper functions that
// delegate to selected local tools. It is meant for small deterministic loops
// and aggregation work that would otherwise burn many model/tool turns.
type ProgrammaticTool struct {
	registry       *Registry
	allowedHelpers map[string]bool
}

// NewProgrammaticTool constructs a local programmatic execution tool. The
// supplied registry should be the scoped per-session registry so helper calls
// see the same tool instances as ordinary model calls.
func NewProgrammaticTool(registry *Registry, allowedHelpers []string) *ProgrammaticTool {
	allowed := map[string]bool{}
	for _, name := range allowedHelpers {
		switch name {
		case "Glob", "Grep", "Read", "Bash", "Write", "WebFetch", "OpenCLI":
			allowed[name] = true
		}
	}
	return &ProgrammaticTool{registry: registry, allowedHelpers: allowed}
}

func (*ProgrammaticTool) Name() string { return "Programmatic" }

func (*ProgrammaticTool) Description() string {
	return "Run a bounded Starlark script locally to batch multiple small workspace operations in one tool call. " +
		"Prefer this over direct Bash/Glob/Grep/Read/WebFetch/Write calls whenever you need a small loop, several similar probes, result filtering, or aggregation. " +
		"Use this for numeric sweeps, repeated probes, filtering, or read/search/shell/write workflows that would otherwise require many separate model turns. " +
		"The script can only use the provided helper functions: glob, grep, read, bash, write, web_fetch, and opencli. " +
		"If a batch needs more helper calls than the per-script cap allows, split it into several consecutive Programmatic calls (e.g. items 1-50, then 51-100) — do NOT fall back to one-tool-call-per-turn."
}

func (*ProgrammaticTool) JSONSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"code"},
		"properties": map[string]any{
			"code": map[string]any{
				"type":        "string",
				"description": "Starlark code. Set a top-level variable named result to the final value to return. Helper calls return structs with .content and .is_error fields.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Short description of the local batch operation.",
			},
			"max_tool_calls": map[string]any{
				"type":        "integer",
				"description": "Optional cap for helper tool calls in this script. Defaults to 20, max 50. Set this explicitly whenever the batch may need more than 20 calls. A task larger than 50 calls is still a Programmatic task: split it into several consecutive Programmatic calls, each processing a slice of the items.",
			},
		},
	}
}

func (*ProgrammaticTool) NeedsPermission(map[string]any) (bool, PermissionPrompt) {
	return false, PermissionPrompt{}
}

type programmaticCallRecord struct {
	Index   int    `json:"index"`
	Tool    string `json:"tool"`
	IsError bool   `json:"is_error"`
	Content string `json:"content"`
}

type programmaticExecution struct {
	tool        *ProgrammaticTool
	ctx         context.Context
	workDir     string
	parentID    string
	maxCalls    int
	callCount   int
	calls       []programmaticCallRecord
	prints      []string
	outputBytes int
	truncated   bool
	authority   *AuthorityRuntime
}

func (t *ProgrammaticTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	if t.registry == nil {
		return ToolResult{}, fmt.Errorf("programmatic registry is required")
	}
	code, _ := call.Input["code"].(string)
	code = strings.TrimSpace(code)
	if code == "" {
		return ToolResult{}, fmt.Errorf("code is required")
	}
	if len(code) > programmaticMaxCodeBytes {
		return ToolResult{}, fmt.Errorf("code is too large (%d bytes > %d bytes)", len(code), programmaticMaxCodeBytes)
	}

	maxCalls := programmaticDefaultMaxToolCalls
	if n, ok := asInt(call.Input["max_tool_calls"]); ok && n > 0 {
		maxCalls = n
	}
	if maxCalls > programmaticHardMaxToolCalls {
		maxCalls = programmaticHardMaxToolCalls
	}

	execCtx, cancel := context.WithTimeout(ctx, programmaticTimeout)
	defer cancel()

	run := &programmaticExecution{
		tool:      t,
		ctx:       execCtx,
		workDir:   call.WorkDir,
		parentID:  call.ID,
		maxCalls:  maxCalls,
		authority: call.Authority,
	}
	thread := &starlark.Thread{
		Name: "Programmatic",
		Print: func(_ *starlark.Thread, msg string) {
			run.prints = append(run.prints, truncateProgrammaticString(msg, 2000))
		},
		Load: func(_ *starlark.Thread, module string) (starlark.StringDict, error) {
			return nil, fmt.Errorf("load is disabled in Programmatic scripts: %s", module)
		},
	}
	thread.SetMaxExecutionSteps(programmaticMaxExecutionSteps)

	done := make(chan struct{})
	go func() {
		select {
		case <-execCtx.Done():
			thread.Cancel(execCtx.Err().Error())
		case <-done:
		}
	}()
	defer close(done)

	globals, err := starlark.ExecFile(thread, "programmatic.star", code, run.predeclared())
	var resultValue starlark.Value = starlark.None
	if err == nil {
		if v, ok := globals["result"]; ok {
			resultValue = v
		}
	}

	payload := map[string]any{
		"ok":              err == nil,
		"result":          starlarkToInterface(resultValue),
		"prints":          append([]string(nil), run.prints...),
		"tool_calls":      run.calls,
		"execution_steps": thread.ExecutionSteps(),
		"truncated":       run.truncated,
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	contentBytes, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		return ToolResult{}, fmt.Errorf("encode programmatic result: %w", marshalErr)
	}
	content := string(contentBytes)
	if len(content) > programmaticMaxResultBytes {
		content = content[:programmaticMaxResultBytes] + "\n... <programmatic result truncated> ..."
	}
	return ToolResult{Content: content, IsError: err != nil}, nil
}

func (r *programmaticExecution) predeclared() starlark.StringDict {
	predeclared := starlark.StringDict{
		"struct": starlark.NewBuiltin("struct", starlarkstruct.Make),
	}
	if r.tool.allowedHelpers["Glob"] {
		predeclared["glob"] = starlark.NewBuiltin("glob", r.glob)
	}
	if r.tool.allowedHelpers["Grep"] {
		predeclared["grep"] = starlark.NewBuiltin("grep", r.grep)
	}
	if r.tool.allowedHelpers["Read"] {
		predeclared["read"] = starlark.NewBuiltin("read", r.read)
	}
	if r.tool.allowedHelpers["Bash"] {
		predeclared["bash"] = starlark.NewBuiltin("bash", r.bash)
	}
	if r.tool.allowedHelpers["Write"] {
		predeclared["write"] = starlark.NewBuiltin("write", r.write)
	}
	if r.tool.allowedHelpers["WebFetch"] {
		predeclared["web_fetch"] = starlark.NewBuiltin("web_fetch", r.webFetch)
	}
	if r.tool.allowedHelpers["OpenCLI"] {
		predeclared["opencli"] = starlark.NewBuiltin("opencli", r.opencli)
	}
	return predeclared
}

func (r *programmaticExecution) glob(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var pattern, path string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "pattern", &pattern, "path?", &path); err != nil {
		return nil, err
	}
	input := map[string]any{"pattern": pattern}
	if path != "" {
		input["path"] = path
	}
	return r.callTool("Glob", input)
}

func (r *programmaticExecution) grep(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var pattern, path, glob, outputMode string
	var headLimit int
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"pattern", &pattern,
		"path?", &path,
		"glob?", &glob,
		"output_mode?", &outputMode,
		"head_limit?", &headLimit,
	); err != nil {
		return nil, err
	}
	input := map[string]any{"pattern": pattern}
	if path != "" {
		input["path"] = path
	}
	if glob != "" {
		input["glob"] = glob
	}
	if outputMode != "" {
		input["output_mode"] = outputMode
	}
	if headLimit > 0 {
		input["head_limit"] = headLimit
	}
	return r.callTool("Grep", input)
}

func (r *programmaticExecution) read(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path string
	var offset, limit int
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "path", &path, "offset?", &offset, "limit?", &limit); err != nil {
		return nil, err
	}
	input := map[string]any{"path": path}
	if offset > 0 {
		input["offset"] = offset
	}
	if limit > 0 {
		input["limit"] = limit
	}
	return r.callTool("Read", input)
}

func (r *programmaticExecution) bash(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var command, description string
	var timeout int
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"command", &command,
		"description?", &description,
		"timeout?", &timeout,
	); err != nil {
		return nil, err
	}
	if description == "" {
		description = "Run programmatic command"
	}
	input := map[string]any{"command": command, "description": description}
	if timeout > 0 {
		input["timeout"] = timeout
	}
	return r.callTool("Bash", input)
}

func (r *programmaticExecution) write(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var path, content string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "path", &path, "content", &content); err != nil {
		return nil, err
	}
	return r.callTool("Write", map[string]any{"path": path, "content": content})
}

func (r *programmaticExecution) webFetch(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var url string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "url", &url); err != nil {
		return nil, err
	}
	return r.callTool("WebFetch", map[string]any{"url": url})
}

func (r *programmaticExecution) opencli(_ *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	argv, err := openCLIArgs(args)
	if err != nil {
		return nil, err
	}

	var description string
	var timeout int
	for _, kw := range kwargs {
		if kw.Len() != 2 {
			return nil, fmt.Errorf("%s: invalid keyword argument", fn.Name())
		}
		key, ok := starlark.AsString(kw.Index(0))
		if !ok {
			return nil, fmt.Errorf("%s: keyword name must be a string", fn.Name())
		}
		switch key {
		case "description":
			value, err := starlarkString(kw.Index(1), key)
			if err != nil {
				return nil, err
			}
			description = value
		case "timeout":
			value, err := starlarkInt(kw.Index(1), key)
			if err != nil {
				return nil, err
			}
			timeout = value
		default:
			return nil, fmt.Errorf("%s: unexpected keyword argument %q", fn.Name(), key)
		}
	}
	if description == "" {
		description = "Run opencli command"
	}

	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	input := map[string]any{
		"command":     "opencli " + strings.Join(quoted, " "),
		"description": description,
	}
	if timeout > 0 {
		input["timeout"] = timeout
	}
	return r.callToolAs("OpenCLI", "Bash", input)
}

func (r *programmaticExecution) callTool(name string, input map[string]any) (starlark.Value, error) {
	return r.callToolAs(name, name, input)
}

func (r *programmaticExecution) callToolAs(displayName, dispatchName string, input map[string]any) (starlark.Value, error) {
	if !r.tool.allowedHelpers[displayName] {
		return nil, fmt.Errorf("%s helper is not enabled for this Programmatic invocation", displayName)
	}
	if r.callCount >= r.maxCalls {
		return nil, fmt.Errorf("programmatic helper call limit reached (%d)", r.maxCalls)
	}
	if r.ctx.Err() != nil {
		return nil, r.ctx.Err()
	}
	dispatchTool := r.tool.registry.Get(dispatchName)
	if dispatchTool == nil {
		return nil, fmt.Errorf("unknown helper tool: %s", dispatchName)
	}
	if needs, prompt := dispatchTool.NeedsPermission(input); needs {
		reason := strings.TrimSpace(prompt.Reason)
		if reason == "" {
			reason = "permission required"
		}
		return nil, fmt.Errorf("%s helper requires permission and cannot run inside Programmatic: %s", displayName, reason)
	}

	r.callCount++
	callID := fmt.Sprintf("%s.programmatic.%02d", r.parentID, r.callCount)
	result, err := r.tool.registry.Execute(r.ctx, dispatchName, ToolCall{
		WorkDir:   r.workDir,
		Input:     input,
		ID:        callID,
		Authority: nestedAuthorityRuntime(r.authority),
	})
	content := toolResultContent(result)
	isError := result.IsError || err != nil || toolResultMetadataIndicatesError(result)
	if err != nil {
		content = fmt.Sprintf("%s failed: %v", displayName, err)
	}
	content = r.truncateSubcallContent(content)
	r.calls = append(r.calls, programmaticCallRecord{
		Index:   r.callCount,
		Tool:    displayName,
		IsError: isError,
		Content: content,
	})
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"content":  starlark.String(content),
		"is_error": starlark.Bool(isError),
	}), nil
}

func nestedAuthorityRuntime(parent *AuthorityRuntime) *AuthorityRuntime {
	if parent == nil {
		return nil
	}
	return &AuthorityRuntime{
		Context:                  parent.Context,
		Evaluator:                parent.Evaluator,
		Record:                   parent.Record,
		DeniedBashPrefixes:       parent.DeniedBashPrefixes,
		AllowBashWithoutApproval: parent.AllowBashWithoutApproval,
	}
}

func toolResultMetadataIndicatesError(result ToolResult) bool {
	if result.Metadata == nil {
		return false
	}
	if exitCode, ok := asInt(result.Metadata["exit_code"]); ok && exitCode != 0 {
		return true
	}
	if timedOut, ok := result.Metadata["timed_out"].(bool); ok && timedOut {
		return true
	}
	return false
}

func openCLIArgs(args starlark.Tuple) ([]string, error) {
	if args.Len() == 0 {
		return nil, fmt.Errorf("opencli requires at least one argument")
	}
	if args.Len() == 1 {
		switch value := args.Index(0).(type) {
		case *starlark.List:
			return starlarkListToStrings(value)
		case starlark.Tuple:
			return starlarkTupleToStrings(value)
		}
	}
	return starlarkTupleToStrings(args)
}

func starlarkListToStrings(list *starlark.List) ([]string, error) {
	out := make([]string, 0, list.Len())
	iter := list.Iterate()
	defer iter.Done()
	var item starlark.Value
	for iter.Next(&item) {
		value, err := starlarkString(item, "opencli arg")
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func starlarkTupleToStrings(tuple starlark.Tuple) ([]string, error) {
	out := make([]string, 0, tuple.Len())
	for _, item := range tuple {
		value, err := starlarkString(item, "opencli arg")
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func starlarkString(v starlark.Value, name string) (string, error) {
	value, ok := starlark.AsString(v)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %s", name, v.Type())
	}
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("%s cannot contain NUL bytes", name)
	}
	return value, nil
}

func starlarkInt(v starlark.Value, name string) (int, error) {
	value, ok := v.(starlark.Int)
	if !ok {
		return 0, fmt.Errorf("%s must be an int, got %s", name, v.Type())
	}
	n, ok := value.Int64()
	if !ok {
		return 0, fmt.Errorf("%s is out of range", name)
	}
	return int(n), nil
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func (r *programmaticExecution) truncateSubcallContent(content string) string {
	content = truncateProgrammaticString(content, programmaticSubcallContentBytes)
	r.outputBytes += len(content)
	if r.outputBytes > programmaticMaxResultBytes {
		r.truncated = true
		return truncateProgrammaticString(content, 2000)
	}
	return content
}

func toolResultContent(result ToolResult) string {
	content := result.Content
	if content == "" && len(result.Blocks) > 0 {
		content = fmt.Sprintf("[structured tool_result blocks omitted: %d]", len(result.Blocks))
	}
	if len(result.Warnings) > 0 {
		content += "\nWarnings: " + strings.Join(result.Warnings, "; ")
	}
	return content
}

func truncateProgrammaticString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 64 {
		return s[:max]
	}
	half := (max - 40) / 2
	return s[:half] + "\n... <programmatic output truncated> ...\n" + s[len(s)-half:]
}

func starlarkToInterface(v starlark.Value) any {
	switch x := v.(type) {
	case starlark.NoneType:
		return nil
	case starlark.Bool:
		return bool(x)
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			return i
		}
		return x.String()
	case starlark.Float:
		return float64(x)
	case starlark.String:
		return x.GoString()
	case starlark.Tuple:
		out := make([]any, 0, x.Len())
		for _, item := range x {
			out = append(out, starlarkToInterface(item))
		}
		return out
	case *starlark.List:
		out := make([]any, 0, x.Len())
		iter := x.Iterate()
		defer iter.Done()
		var item starlark.Value
		for iter.Next(&item) {
			out = append(out, starlarkToInterface(item))
		}
		return out
	case *starlark.Dict:
		out := map[string]any{}
		for _, item := range x.Items() {
			if len(item) != 2 {
				continue
			}
			key, ok := starlark.AsString(item[0])
			if !ok {
				key = item[0].String()
			}
			out[key] = starlarkToInterface(item[1])
		}
		return out
	case *starlarkstruct.Struct:
		out := map[string]any{}
		for _, name := range x.AttrNames() {
			attr, err := x.Attr(name)
			if err == nil {
				out[name] = starlarkToInterface(attr)
			}
		}
		return out
	default:
		return x.String()
	}
}
