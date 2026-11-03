package native

import (
	"regexp"
	"strings"
)

const SearchNarrowingLoopThreshold = 5

var shellRedirectNoiseRe = regexp.MustCompile(`\s+(?:2>\s*/dev/null|2>&1|1>\s*/dev/null)\b`)

// SearchNarrowingLoopState tracks empty results for one Bash search family.
// The family is intentionally coarse: "same base file enumeration, followed
// by grep-style filters". Once repeated narrower variants all return empty,
// another narrower variant is no longer useful work.
type SearchNarrowingLoopState struct {
	EmptyCount     int
	MaxFilterCount int
}

type SearchNarrowingLoopResult struct {
	Kind        string // "ok" | "hard"
	Family      string
	Count       int
	FilterCount int
}

func CheckForSearchNarrowingLoop(states map[string]SearchNarrowingLoopState, toolName, rawInput string) SearchNarrowingLoopResult {
	family, filterCount, ok := bashSearchNarrowingFamily(toolName, rawInput)
	if !ok {
		return SearchNarrowingLoopResult{Kind: "ok"}
	}
	state := states[family]
	if state.EmptyCount >= SearchNarrowingLoopThreshold && filterCount >= state.MaxFilterCount {
		return SearchNarrowingLoopResult{
			Kind:        "hard",
			Family:      family,
			Count:       state.EmptyCount + 1,
			FilterCount: filterCount,
		}
	}
	return SearchNarrowingLoopResult{
		Kind:        "ok",
		Family:      family,
		Count:       state.EmptyCount + 1,
		FilterCount: filterCount,
	}
}

func RecordSearchNarrowingResult(states map[string]SearchNarrowingLoopState, toolName, rawInput string, block ContentBlock) {
	family, filterCount, ok := bashSearchNarrowingFamily(toolName, rawInput)
	if !ok {
		return
	}
	content, _ := block.Content.(string)
	if block.IsError || strings.TrimSpace(content) != "" {
		delete(states, family)
		return
	}
	state := states[family]
	state.EmptyCount++
	if filterCount > state.MaxFilterCount {
		state.MaxFilterCount = filterCount
	}
	states[family] = state
}

func SyntheticSearchNarrowingLoopBlockResult(toolUseID string, count int) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		IsError:   true,
		Content: "[search-narrowing-loop-guard] Similar Bash search pipelines have already returned empty output " +
			itoa(count-1) + " times while adding more filters. This invocation was short-circuited; the command was NOT executed. " +
			"Stop adding grep/find filters to the same empty search. Broaden the search, inspect likely files directly, use a different tool, or write your final answer based on available evidence.",
	}
}

func SearchNarrowingLoopNudgeText(count int) string {
	return "Notice: Bash search pipelines in this task have returned empty output " + itoa(count-1) +
		" times while adding more filters. Stop narrowing the same empty search now. Broaden the search, inspect likely files directly, use Programmatic for bounded batching, or explain the blocker in your final answer."
}

func bashSearchNarrowingFamily(toolName, rawInput string) (string, int, bool) {
	if !strings.EqualFold(toolName, "Bash") {
		return "", 0, false
	}
	command, ok := bashCommandFromRawInput(rawInput)
	if !ok || isYansuCronMutationCommand(command) {
		return "", 0, false
	}
	segments := splitShellPipeline(command)
	if len(segments) < 3 {
		return "", 0, false
	}

	firstFilter := -1
	filterCount := 0
	for i, segment := range segments {
		if isSearchFilterSegment(segment) {
			if firstFilter < 0 {
				firstFilter = i
			}
			filterCount++
		}
	}
	if firstFilter <= 0 || filterCount < 2 {
		return "", 0, false
	}
	base := normalizeSearchPipelineBase(segments[:firstFilter])
	if !looksLikeFileEnumeration(base) {
		return "", 0, false
	}
	return "bash-search-narrowing|" + base, filterCount, true
}

func splitShellPipeline(command string) []string {
	raw := strings.Split(command, "|")
	segments := make([]string, 0, len(raw))
	for _, segment := range raw {
		normalized := normalizeSearchPipelineSegment(segment)
		if normalized != "" {
			segments = append(segments, normalized)
		}
	}
	return segments
}

func normalizeSearchPipelineBase(segments []string) string {
	normalized := make([]string, 0, len(segments))
	for _, segment := range segments {
		if s := normalizeSearchPipelineSegment(segment); s != "" {
			normalized = append(normalized, s)
		}
	}
	return strings.Join(normalized, " | ")
}

func normalizeSearchPipelineSegment(segment string) string {
	segment = shellRedirectNoiseRe.ReplaceAllString(segment, "")
	segment = strings.Join(strings.Fields(segment), " ")
	return strings.TrimSpace(segment)
}

func isSearchFilterSegment(segment string) bool {
	fields := strings.Fields(segment)
	for i, field := range fields {
		base := strings.Trim(field, `"'`)
		if !isGrepCommandName(base) {
			continue
		}
		for _, arg := range fields[i+1:] {
			cleaned := strings.Trim(arg, `"'`)
			if cleaned == "-l" || cleaned == "--files-with-matches" || strings.Contains(cleaned, "l") && strings.HasPrefix(cleaned, "-") && !strings.HasPrefix(cleaned, "--") {
				return true
			}
		}
	}
	return false
}

func isGrepCommandName(name string) bool {
	return name == "grep" || strings.HasSuffix(name, "/grep") || name == "ggrep" || strings.HasSuffix(name, "/ggrep")
}

func looksLikeFileEnumeration(base string) bool {
	fields := strings.Fields(base)
	if len(fields) == 0 {
		return false
	}
	first := strings.Trim(fields[0], `"'`)
	switch {
	case first == "find" || strings.HasSuffix(first, "/find"):
		return true
	case first == "fd" || strings.HasSuffix(first, "/fd"):
		return true
	case first == "rg" || strings.HasSuffix(first, "/rg"):
		return strings.Contains(base, "--files") || strings.Contains(base, " -l ") || strings.Contains(base, " --files-with-matches")
	default:
		return false
	}
}
