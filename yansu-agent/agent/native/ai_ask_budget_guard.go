package native

import (
	"regexp"
	"strings"
)

// AI-ask budget guard: deterministic enforcement of the "at most ONE
// `opencli <ai> ask` per question" routing rule. The rule already exists in
// the injected system prompt AND in the smart-search skill, but prompt-only
// enforcement fails in practice — the model rationalizes "one more chatbot
// might know" and fans the same question out to grok/doubao/gemini/kimi/...,
// which share training data and add latency, not signal.
//
// Thresholds are intentionally one looser than the prompt's "ONE" so that a
// legitimate environment failure on the first ask (auth, extension not
// connected) still leaves one retry/alternative before the hard block:
//   - call 1: ok (the budget)
//   - call 2: executes, but a stop-nudge text block is appended to the same
//     user message that carries the tool_results
//   - call 3+: short-circuited with a synthetic error tool_result; the
//     command is NOT executed
const (
	AIAskNudgeThreshold = 2
	AIAskHardThreshold  = 3
)

type AIAskBudgetResult struct {
	Kind    string // "ok" | "nudge" | "hard"
	Count   int
	Counted bool
}

// aiAskCommandRe matches a real `opencli <adapter> ask|chat ...` execution.
// `web read`, `list`, vertical-site search commands and similar are not AI
// chatbot asks and stay unbudgeted here.
var aiAskCommandRe = regexp.MustCompile(`\bopencli\s+(\S+)\s+(ask|chat)\b`)

// helpFlagRe matches a standalone -h / --help flag token so preflight
// probes (`opencli doubao ask -h`) are not budgeted as real asks.
var helpFlagRe = regexp.MustCompile(`\s(-h|--help)(\s|$)`)

// knownAIAdapterNames are the AI-chatbot adapter names a user can call out
// in a prompt to explicitly request a multi-source comparison. Latin names
// are matched on word boundaries; Chinese aliases by substring (Chinese has
// no word boundaries). Keep this list aligned with the opencli registry's
// AI adapters.
var knownAIAdapterLatin = regexp.MustCompile(`(?i)\b(grok|gemini|doubao|kimi|qwen|yuanbao|chatgpt|deepseek)\b`)

var knownAIAdapterChinese = []string{"豆包", "千问", "通义", "元宝"}

// chineseAliasCanonical maps a Chinese alias to its canonical adapter so
// "千问" and "qwen" in the same prompt count as ONE source, not two.
var chineseAliasCanonical = map[string]string{
	"豆包": "doubao",
	"千问": "qwen",
	"通义": "qwen",
	"元宝": "yuanbao",
}

// AIAskBudgetAllowance inspects the user's prompt for an explicit
// multi-AI-source comparison request. When the prompt names two or more
// distinct AI adapters ("问问 grok 和 gemini 对比下"), the user has opted in
// to multiple asks, so the budget thresholds are raised by the number of
// named sources. A single incidental mention (or none) returns 0 — the
// default budget applies. The allowance can only RAISE the budget, never
// lower it.
func AIAskBudgetAllowance(prompt string) int {
	named := map[string]bool{}
	for _, m := range knownAIAdapterLatin.FindAllString(prompt, -1) {
		named[strings.ToLower(m)] = true
	}
	for _, alias := range knownAIAdapterChinese {
		if strings.Contains(prompt, alias) {
			named[chineseAliasCanonical[alias]] = true
		}
	}
	if len(named) < 2 {
		return 0
	}
	return len(named)
}

// CheckAIAskBudget evaluates one tool call against the per-run AI-ask budget.
// allowance (from AIAskBudgetAllowance) raises both thresholds when the user
// explicitly requested a multi-source comparison; pass 0 for the default.
func CheckAIAskBudget(priorCount int, allowance int, toolName, rawInput string) AIAskBudgetResult {
	if !isAIAskToolCall(toolName, rawInput) {
		return AIAskBudgetResult{Kind: "ok", Count: priorCount}
	}
	if allowance < 0 {
		allowance = 0
	}
	count := priorCount + 1
	switch {
	case count >= AIAskHardThreshold+allowance:
		return AIAskBudgetResult{Kind: "hard", Count: count, Counted: true}
	case count == AIAskNudgeThreshold+allowance:
		return AIAskBudgetResult{Kind: "nudge", Count: count, Counted: true}
	default:
		return AIAskBudgetResult{Kind: "ok", Count: count, Counted: true}
	}
}

func SyntheticAIAskBudgetBlockResult(toolUseID string, count int) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		IsError:   true,
		Content: "[ai-ask-budget-guard] This task has already sent " + itoa(count-1) +
			" `opencli <ai> ask` queries. This invocation was short-circuited; the command was NOT executed. " +
			"AI chatbots share training data — asking another one adds latency, not signal. " +
			"Switch to a primary data source instead (a REST endpoint, `opencli web read --url`, or a vertical-site adapter), " +
			"or answer from what you already have.",
	}
}

func AIAskBudgetNudgeText(count int) string {
	return "Notice: this task has now sent " + itoa(count) + " `opencli <ai> ask` queries. " +
		"The routing budget is ONE AI-source ask per question — chatbots share training data, so fanning the same " +
		"question out to more of them adds latency, not signal. Do not ask another AI source in this task. " +
		"If the answer is still missing, switch to a primary data source (REST endpoint, `opencli web read --url`, " +
		"or a vertical-site adapter) or answer from what you already have."
}

func isAIAskToolCall(toolName, rawInput string) bool {
	if toolName != "Bash" {
		return false
	}
	command, ok := bashCommandFromRawInput(rawInput)
	if !ok {
		return false
	}
	// Help/preflight probes (`opencli doubao ask -h`) are not real asks.
	if helpFlagRe.MatchString(command) {
		return false
	}
	return aiAskCommandRe.MatchString(command)
}
