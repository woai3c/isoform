package native

import (
	"sort"
	"strconv"
	"strings"
)

const (
	DirectToolCaller       = "direct"
	ProgrammaticToolCaller = "code_execution_20260120"
	codeExecutionToolName  = "code_execution"
	programmaticNudgeAfter = 4
)

func codeExecutionToolDef() ToolDef {
	return ToolDef{
		Type: ProgrammaticToolCaller,
		Name: codeExecutionToolName,
	}
}

func supportsProgrammaticCalling(toolName string) bool {
	switch toolName {
	case "Bash", "Glob", "Grep", "Read", "WebFetch", "Write":
		return true
	default:
		return false
	}
}

func modelSupportsProgrammaticToolCalling(modelID string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	if normalized == "sonnet" || normalized == "opus" {
		return true
	}
	for _, supported := range []string{
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-opus-4-5",
		"claude-sonnet-4-5",
	} {
		if strings.Contains(normalized, supported) {
			return true
		}
	}
	return false
}

func isProgrammaticToolUse(tu toolUseReq) bool {
	return tu.Caller != nil && tu.Caller.Type == ProgrammaticToolCaller
}

func hasProgrammaticToolUse(toolUses []toolUseReq) bool {
	for _, tu := range toolUses {
		if isProgrammaticToolUse(tu) {
			return true
		}
	}
	return false
}

func programmaticBatchingNudgeText(total int, byTool map[string]int) string {
	return "Notice: Programmatic is available for this task and the model has already made " +
		pluralizeCount(total, "direct batchable tool call") + " (" + formatToolCounts(byTool) + "). " +
		"For any further repeated Bash/Glob/Grep/Read/WebFetch/Write probing, call Programmatic once and batch the loop locally. " +
		"Only continue with direct tools for a one-off action that cannot naturally fit in a small script."
}

func formatToolCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+pluralizeCount(counts[name], "call"))
	}
	return strings.Join(parts, ", ")
}

func pluralizeCount(n int, singular string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + singular + "s"
}
