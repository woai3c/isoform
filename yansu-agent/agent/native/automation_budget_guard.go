package native

import "strings"

const (
	UIAutomationNudgeThreshold = 40
	UIAutomationHardThreshold  = 60
)

type UIAutomationBudgetResult struct {
	Kind    string // "ok" | "nudge" | "hard"
	Count   int
	Counted bool
}

func CheckUIAutomationBudget(priorCount int, toolName, rawInput string) UIAutomationBudgetResult {
	if !isUIAutomationToolCall(toolName, rawInput) {
		return UIAutomationBudgetResult{Kind: "ok", Count: priorCount}
	}
	count := priorCount + 1
	switch {
	case count >= UIAutomationHardThreshold:
		return UIAutomationBudgetResult{Kind: "hard", Count: count, Counted: true}
	case count == UIAutomationNudgeThreshold:
		return UIAutomationBudgetResult{Kind: "nudge", Count: count, Counted: true}
	default:
		return UIAutomationBudgetResult{Kind: "ok", Count: count, Counted: true}
	}
}

func SyntheticUIAutomationBudgetBlockResult(toolUseID string, count int) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		IsError:   true,
		Content: "[ui-automation-budget-guard] GUI/browser automation has already used " +
			itoa(count) + " tool calls in this task. This invocation was short-circuited; the tool was NOT executed. " +
			"Stop creating more one-off UI inspection or browser-control attempts. Finish from the current state, or explain the blocker to the user.",
	}
}

func UIAutomationBudgetNudgeText(count int) string {
	return "Notice: this task has used " + itoa(count) + " GUI/browser automation tool calls (CUA, screenshots, AppleScript, or browser-control shell scripts). " +
		"Stop cycling through more observe/patch attempts. If the UI state is accessible, perform only the next concrete action needed to finish. " +
		"If the window cannot be observed or the page interaction is not taking effect, stop and write a final answer explaining the blocker; do not keep creating new one-off scripts for the same page."
}

func isUIAutomationToolCall(toolName, rawInput string) bool {
	switch toolName {
	case "cua_driver":
		return true
	case "Bash":
		command, ok := bashCommandFromRawInput(rawInput)
		return ok && isUIAutomationShellCommand(command)
	case "Write":
		return isUIAutomationWrite(rawInput)
	case "Read":
		return isUIAutomationRead(rawInput)
	default:
		return false
	}
}

func isUIAutomationShellCommand(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{
		"osascript",
		"screencapture",
		"cliclick",
		"xdotool",
		"open -a \"google chrome\"",
		"open -g -a \"google chrome\"",
		"open -a google\\ chrome",
		"open -g -a google\\ chrome",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isUIAutomationWrite(rawInput string) bool {
	m, ok := parseRawInputForGuard(rawInput).(map[string]any)
	if !ok {
		return false
	}
	path, _ := m["path"].(string)
	content, _ := m["content"].(string)
	lowerPath := strings.ToLower(strings.TrimSpace(path))
	lowerContent := strings.ToLower(content)
	return strings.HasSuffix(lowerPath, ".scpt") ||
		strings.Contains(lowerContent, "tell application \"google chrome\"") ||
		strings.Contains(lowerContent, "tell application \"system events\"") ||
		strings.Contains(lowerContent, "tell process \"google chrome\"")
}

func isUIAutomationRead(rawInput string) bool {
	m, ok := parseRawInputForGuard(rawInput).(map[string]any)
	if !ok {
		return false
	}
	path, _ := m["path"].(string)
	lowerPath := strings.ToLower(strings.TrimSpace(path))
	if !(strings.HasSuffix(lowerPath, ".png") ||
		strings.HasSuffix(lowerPath, ".jpg") ||
		strings.HasSuffix(lowerPath, ".jpeg")) {
		return false
	}
	return strings.Contains(lowerPath, "screen") ||
		strings.Contains(lowerPath, "screenshot") ||
		strings.Contains(lowerPath, "capture")
}
