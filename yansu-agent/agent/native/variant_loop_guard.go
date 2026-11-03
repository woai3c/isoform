package native

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const NumericVariantLoopThreshold = 5

var commandNumberRe = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
var commandURLRe = regexp.MustCompile(`https?://[^\s"'<>]+`)

// NumericVariantLoopResult reports whether a Bash command family has already
// produced the same result enough times while only numeric literals changed.
type NumericVariantLoopResult struct {
	Kind   string // "ok" | "hard"
	Family string
	Count  int
}

func CheckForNumericVariantLoop(counts map[string]int, toolName, rawInput string) NumericVariantLoopResult {
	family, ok := bashNumericCommandFamily(toolName, rawInput)
	if !ok {
		return NumericVariantLoopResult{Kind: "ok"}
	}
	maxCount := 0
	prefix := family + "|"
	for key, count := range counts {
		if strings.HasPrefix(key, prefix) && count > maxCount {
			maxCount = count
		}
	}
	if maxCount >= NumericVariantLoopThreshold {
		return NumericVariantLoopResult{Kind: "hard", Family: family, Count: maxCount + 1}
	}
	return NumericVariantLoopResult{Kind: "ok", Family: family, Count: maxCount + 1}
}

func RecordNumericVariantResult(counts map[string]int, toolName, rawInput string, block ContentBlock) {
	family, ok := bashNumericCommandFamily(toolName, rawInput)
	if !ok {
		return
	}
	counts[family+"|"+numericVariantResultDigest(block)]++
}

func SyntheticNumericVariantLoopBlockResult(toolUseID string, count int) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		IsError:   true,
		Content: "[numeric-variant-loop-guard] Similar Bash commands that only change numeric or URL query parameter values have already returned the same result " +
			itoa(count-1) + " times. This invocation was short-circuited; the command was NOT executed. " +
			"Stop sweeping numeric parameters and switch tactics, omit optional numeric parameters, or write your final answer based on what you already know.",
	}
}

func NumericVariantLoopNudgeText(count int) string {
	return "Notice: Bash commands that only change numeric or URL query parameter values have returned the same result " + itoa(count-1) + " times. " +
		"Stop enumerating numeric parameters now. Do not try nearby numbers, pages, periods, ids, offsets, or URL query values unless the user explicitly supplied them or documentation proves they are required. " +
		"Use the successful/available result, omit optional parameters, or explain the blocker in your final answer."
}

func bashNumericCommandFamily(toolName, rawInput string) (string, bool) {
	if !strings.EqualFold(toolName, "Bash") {
		return "", false
	}
	command, ok := bashCommandFromRawInput(rawInput)
	if !ok {
		return "", false
	}
	if isYansuCronMutationCommand(command) {
		return "", false
	}
	normalized, changed := normalizeNumericCommand(command)
	if !changed {
		return "", false
	}
	return normalized, true
}

func bashCommandFromRawInput(rawInput string) (string, bool) {
	if strings.TrimSpace(rawInput) == "" {
		return "", false
	}
	pr, err := Parse(rawInput, ParseOptions{})
	if err != nil {
		return "", false
	}
	m, ok := pr.Value.(map[string]any)
	if !ok {
		return "", false
	}
	command, _ := m["command"].(string)
	command = strings.TrimSpace(command)
	return command, command != ""
}

func normalizeNumericCommand(command string) (string, bool) {
	command, queryChanged := normalizeCommandURLQueryValues(command)
	changed := false
	out := commandNumberRe.ReplaceAllStringFunc(strings.TrimSpace(command), func(string) string {
		changed = true
		return "<num>"
	})
	return out, changed || queryChanged
}

func normalizeCommandURLQueryValues(command string) (string, bool) {
	changed := false
	out := commandURLRe.ReplaceAllStringFunc(command, func(rawURL string) string {
		normalized, ok := normalizeURLQueryValues(rawURL)
		if ok {
			changed = true
			return normalized
		}
		return rawURL
	})
	return out, changed
}

func normalizeURLQueryValues(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.RawQuery == "" {
		return rawURL, false
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(values) == 0 {
		return rawURL, false
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	normalized := url.Values{}
	for _, key := range keys {
		normalized.Set(key, "<param>")
	}
	parsed.RawQuery = normalized.Encode()
	return parsed.String(), true
}

func numericVariantResultDigest(block ContentBlock) string {
	raw, err := json.Marshal(struct {
		Content any  `json:"content"`
		IsError bool `json:"is_error"`
	}{
		Content: block.Content,
		IsError: block.IsError,
	})
	if err != nil {
		raw = []byte(stableJSONString(block.Content))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

func isYansuCronMutationCommand(command string) bool {
	fields := strings.Fields(command)
	for i, field := range fields {
		cleaned := strings.Trim(field, `"'`)
		if !strings.HasSuffix(cleaned, "yansu") && !strings.HasSuffix(cleaned, "yansu.exe") {
			continue
		}
		if i+2 >= len(fields) {
			continue
		}
		if strings.Trim(fields[i+1], `"'`) != "cron" {
			continue
		}
		action := strings.Trim(fields[i+2], `"'`)
		if action == "add" || action == "update" {
			return true
		}
	}
	lower := strings.ToLower(strings.TrimSpace(command))
	return strings.Contains(lower, " cron add ") || strings.HasSuffix(lower, " cron add") ||
		strings.Contains(lower, " cron update ") || strings.HasSuffix(lower, " cron update")
}
